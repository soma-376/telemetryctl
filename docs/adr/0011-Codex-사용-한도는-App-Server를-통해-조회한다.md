# 0011. Codex 사용 한도는 App Server를 통해 조회한다.

## Status
Accepted

## Context

`internal/vendorlimit`은 Claude Code와 Codex의 구독 사용 한도를 공통 모델로 정규화한다.
현재 Codex 어댑터는 `~/.codex/auth.json`을 직접 읽어 access token과 account ID를 꺼내고,
문서화되지 않은 `GET https://chatgpt.com/backend-api/codex/usage`를 Go HTTP 클라이언트로 호출한다.

이 경로에는 다음 문제가 있다.

- Pulsemetry가 Codex 자격증명 파일 형식, Bearer 인증, 계정 헤더를 모두 알아야 한다.
- 요청에 `User-Agent`를 지정하지 않아 Go 기본값이 전송된다. 비공개 엔드포인트의 봇 탐지와
  클라이언트 식별 정책을 Pulsemetry가 정확히 재현할 근거가 없다.
- 토큰 갱신을 안전상의 이유로 하지 않으므로, Codex가 갱신할 수 있는 세션도 Pulsemetry에서는
  만료로 보일 수 있다.
- 비공개 HTTP 응답이 바뀔 때 Pulsemetry가 그 변경을 직접 추적해야 한다.

설치된 Codex CLI는 실험적 App Server 프로토콜을 제공한다. 프로토콜의
`account/rateLimits/read` 요청은 구독 플랜, 사용률, 창 길이와 초기화 시각을 정규화해 반환한다.
App Server가 인증과 상위 HTTP 통신을 소유하므로 Pulsemetry가 원본 자격증명을 다룰 필요가 없다.

결정하지 않으면 현재 비공개 HTTP 호출의 인증·식별·응답 호환성 책임이 계속 Pulsemetry에 남는다.

## Decision

- Codex 사용 한도는 `codex app-server --stdio`의 `account/rateLimits/read`로 조회한다.
- Codex App Server 프로토콜과 프로세스 수명은 새 `internal/codexapp` 패키지가 소유한다.
  이 패키지는 다음 책임만 가진다.
  - 자식 프로세스 시작과 종료
  - `initialize` 요청과 `initialized` 알림
  - 줄 단위 JSON 요청·응답 처리와 요청 ID 매칭
  - `account/rateLimits/read` 응답 타입
  - 취소, 타임아웃, 프로세스 종료와 프로토콜 오류의 분류
- `internal/vendorlimit`의 Codex 어댑터는 `internal/codexapp` 응답을 기존 `Result`, `Window`,
  `ExtraAllowance`로 변환하는 책임만 가진다. App Server의 전송 타입을 GUI 계약에 노출하지 않는다.
- `internal/codexapp`은 `internal/vendorlimit` 아래에 두지 않는다. 이후 다른 Codex 기능이 App Server를
  사용하더라도 구독 한도 도메인에 의존하지 않게 한다.
- App Server 프로세스는 조회마다 새로 만들지 않는다. **데몬의** 장기 실행
  `vendorlimit.Collector`가 `codexapp.Client` 하나를 소유하고 여러 조회에서 재사용하며,
  데몬 종료 시 `Close`한다.
- 운영 조회 표면은 장기 실행 `Collector.CollectVendor`로 제한한다. 조회마다 App Server를
  만들고 닫는 단발 `vendorlimit.Collect` 호환 함수는 두지 않는다.
  운영 GUI 경로는 Collector를 만들지 않고 데몬이 SQLite에 upsert한 최신 스냅샷을 읽는다.
- Codex 자격증명 파일을 Pulsemetry가 직접 읽는 코드와 Codex 비공개 HTTP fallback은 제거한다.
  fallback을 남겨 인증·식별 책임을 이중으로 유지하지 않는다.
- 기존 공용 `transport.go`는 제거한다. HTTP를 계속 사용하는 Claude의 요청·인증·응답 제한은
  `claude_http.go`가 소유하고, Claude 자격증명 파일 해석은 `claude_credentials.go`가 소유한다.
  두 벤더는 같은 어댑터 결과 계약을 지키되 인증과 전송 구현을 억지로 대칭으로 만들지 않는다.
- Codex 실행 파일 부재, 로그인되지 않은 상태, App Server 프로토콜 불일치와 프로세스 장애는
  Codex 결과 하나만 `unavailable`로 만든다. Claude Code 결과와 로컬 대시보드 조회는 유지한다.
- GUI의 수동 새로고침은 기존 loopback HTTP 수신기의 `POST /v1/tray/refresh`로 데몬에
  명령한다. 데몬은 SQLite upsert를 마친 뒤 갱신된 트레이 스냅샷을 응답한다(ADR 0013).
- 제어 요청은 기존 local ingest bearer token과 `X-Pulsemetry-Local: 1` 인증을 그대로 사용한다.
  제어 기능이 계정 설정 변경·파일 접근·데몬 종료로 넓어지기 전에는 별도 control token을 만들지 않는다.
- 수동 갱신의 첫 요청은 두 벤더를 즉시 조회한다. 동시에 들어온 자동·수동 요청은 진행 중인
  한 번의 결과를 공유하며, 성공 완료 뒤 10초 동안의 요청은 추가 외부 호출 없이 최신 스냅샷을 반환한다.
- App Server stdout은 프로토콜 입력으로만 취급한다. stderr와 디코드 실패 원문은 토큰이나 내부
  응답을 포함할 수 있으므로 GUI 응답과 일반 로그에 그대로 싣지 않는다.

예상 디렉터리 경계는 다음과 같다.

```text
internal/
  codexapp/
    client.go                 # 공개 Client와 요청 수명
    process.go                # codex app-server 시작·종료
    protocol.go               # JSONL envelope, handshake, 요청 ID 매칭
    ratelimits.go             # account/rateLimits/read 전송 타입
    errors.go                 # 프로세스·프로토콜 오류 분류
    client_test.go
    process_test.go
    protocol_test.go
    ratelimits_test.go
    testdata/
      fakeserver/             # 실제 Codex와 네트워크를 쓰지 않는 모의 프로세스
  vendorlimit/
    types.go                  # Result, Window, Snapshot 등 공통 모델
    collector.go              # 벤더별 조회와 codexapp.Client 수명
    refresh.go                # 자동·수동 갱신, 동시 요청 병합·10초 제한과 저장 조정
    adapter.go                # 벤더 어댑터 계약과 부분 장애 격리
    normalize.go              # 비율·기간·초기화 시각 정규화
    claude.go                 # Claude 응답 → 공통 모델
    claude_credentials.go     # Claude 자격증명 파일 읽기
    claude_http.go            # Claude 전용 HTTP·Bearer·응답 제한
    claude_token.go           # Claude 토큰 원문 비노출 타입
    codex.go                  # codexapp 응답 → 공통 모델
    *_test.go
```

파일 분리는 구현 중 책임 크기에 따라 합치거나 나눌 수 있지만, `codexapp`의 프로토콜·프로세스 책임과
`vendorlimit`의 도메인 정규화 책임 경계는 유지한다. `vendorlimit` 아래에 벤더별 하위 패키지는 만들지
않는다. 공통 모델과 정규화 함수가 하위 패키지 사이를 오가는 공개 API로 커지는 것을 피하고,
파일명 접두사로 벤더별 구현 경계를 드러낸다.

## Constraints

- Codex App Server는 현재 CLI에서 `experimental`로 표시된다. 버전별 메서드와 필드 변화에 대비해야 한다.
- `codex` 실행 파일이 PATH에 없거나 사용자가 로그인하지 않은 상태는 정상적인 미사용 상태다.
- 벤더별 부분 장애와 non-nil 결과 슬라이스 계약을 깨지 않는다. 조회 표면은 장기 실행
  `Collector.CollectVendor`로 제한한다.
- 데몬 기동 직후 한 번, 이후 5분마다 모든 벤더를 갱신하며 트레이는 저장된 최신 값을 읽는다.
- 수동 제어 HTTP는 loopback 리스너에만 존재하고 CORS를 허용하지 않는다.
- 자격증명, Authorization 헤더, App Server의 원문 오류와 응답 본문을 SQLite·GUI·로그에 저장하지 않는다.
- Windows를 포함한 지원 OS에서 자식 프로세스가 데몬 종료 뒤 고아 프로세스로 남지 않아야 한다.

## Alternatives Considered

### A. 직접 HTTP 호출에 Codex와 비슷한 User-Agent만 추가한다

- 장점: 현재 코드 변경이 작고 별도 프로세스 관리가 필요 없다.
- 단점: 비공개 엔드포인트, 자격증명 파일 형식, 토큰 갱신, 계정 헤더와 응답 스키마 책임은 그대로 남는다.
- 탈락 이유: 관측된 차단 한 건만 우회할 뿐 근본적인 소유권 문제를 해결하지 못한다.

### B. 직접 HTTP 호출을 기본으로 두고 App Server를 fallback으로 추가한다

- 장점: App Server가 없는 구버전 Codex에서도 현재 조회를 유지할 수 있다.
- 단점: 두 전송 경로와 두 응답 모델을 계속 시험해야 하며, Pulsemetry가 자격증명을 직접 다루는 위험도 남는다.
- 탈락 이유: 실패 원인에 따라 경로가 달라져 동작을 예측하기 어렵고 제거하려는 책임이 사라지지 않는다.

### C. App Server 연동을 `internal/vendorlimit/codexapp`에 둔다

- 장점: 현재 유일한 소비자인 Codex 한도 어댑터와 코드가 가까이 있다.
- 단점: 범용 Codex 프로토콜 클라이언트가 구독 한도 도메인의 하위 구현으로 고정된다.
- 탈락 이유: 프로토콜 전송과 도메인 정규화의 변경 이유가 다르므로 독립 패키지가 더 명확하다.

### D. 조회할 때마다 App Server 프로세스를 실행한다

- 장점: 공유 상태와 종료 수명 관리가 단순하다.
- 단점: 5분 주기와 수동 새로고침마다 프로세스 시작·인증 초기화 비용이 발생하고, 동시 조회 시 프로세스가 늘어난다.
- 탈락 이유: 데몬이 이미 장기 실행되므로 같은 수명에 클라이언트 하나를 두는 편이 비용과 동시성 모두 예측 가능하다.

## Consequences/Tradeoffs

### Positive

- Pulsemetry 프로세스와 타입에서 Codex access token과 account ID가 사라진다.
- Codex가 소유한 인증 갱신과 상위 요청 식별 방식을 그대로 사용한다.
- 비공개 HTTP 응답 변화의 대부분을 Codex App Server가 흡수한다.
- App Server 프로토콜을 다른 기능에서 재사용해도 `vendorlimit`에 결합되지 않는다.
- Codex 장애가 기존 벤더별 `unavailable` 계약 안에 격리된다.

### Negative

- 데몬이 외부 `codex` 실행 파일과 실험적 프로토콜에 런타임 의존한다.
- 자식 프로세스 수명, 동시 요청 ID, stdout reader, 취소와 종료를 새로 구현하고 시험해야 한다.
- 설치된 Codex 버전에 따라 메서드가 없거나 응답 필드가 달라질 수 있다.
- `Collector` 도입으로 현재의 단순한 패키지 함수 호출보다 생성·종료 배선이 늘어난다.
- 직접 HTTP fallback을 제거하므로 App Server를 지원하지 않는 구버전에서는 Codex 한도를 표시하지 못한다.

## Follow-up

- 지원할 최소 Codex CLI 버전과 프로토콜 호환성 정책을 정한다.
- App Server가 알리는 `account/rateLimits/updated` 알림을 사용할지는 초기 구현의 요청/응답 경로를 검증한 뒤 결정한다.
- `Reason`에 실행 파일 부재와 프로토콜 불일치를 별도 값으로 노출할지, 기존 `internal_error`로 접을지 UI 문구와 함께 결정한다.
- 프로세스 종료가 Windows에서 고아 프로세스를 남기지 않는지 제품 빌드 환경에서 검증한다.

## Acceptance Criteria

- Codex 한도 정상 조회에서 Pulsemetry가 `~/.codex/auth.json`을 읽지 않는다.
- Codex 한도 조회에서 Pulsemetry가 `chatgpt.com/backend-api/codex/usage`를 직접 호출하지 않는다.
- 하나의 Collector가 여러 조회 동안 App Server 프로세스 하나를 재사용한다.
- App Server가 없거나 중간에 종료돼도 Claude Code 결과와 대시보드 스냅샷은 반환된다.
- 모의 App Server를 사용한 테스트가 초기화 순서, 요청 ID 매칭, 타임아웃, 비정상 종료와 비밀 원문 비노출을 검증한다.

## References

- `internal/vendorlimit`
- `internal/dashboard.TrayMonitor`
- [ADR 0004. GUI 연동을 Go 패키지 공유로 하고 HTTP 조회 API를 두지 않는다](0004-GUI-연동을-Go-패키지-공유로.md)
- `docs/local-pipeline.md` §6.10
