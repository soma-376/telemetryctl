# 0013. GUI는 데몬의 로컬 API로 대시보드를 조회한다

## Status

Accepted (ADR 0004의 GUI 직접 SQLite 조회 결정을 대체한다)

## Context

ADR 0004는 데몬과 GUI가 같은 Go 모듈에 있다는 점을 이용해 GUI가 `internal/dashboard`를
호출하고 SQLite를 read-only로 직접 열도록 정했다. 이후 벤더 한도 갱신은 데몬이 외부 벤더와
통신해 SQLite에 쓰고, GUI의 수동 새로고침은 localhost HTTP로 데몬에 명령한 뒤 GUI가 다시
SQLite를 읽는 두 경로로 구현되었다.

이 구조에서는 하나의 화면 동작이 데몬 IPC와 GUI의 SQLite 접근으로 갈라지고, GUI 프로세스도
DB 경로와 스키마를 알아야 한다. 저장소 수명과 스키마를 데몬 내부 구현으로 두려면 조회 역시
데몬을 통과해야 한다.

## Decision

- 데몬이 SQLite의 읽기와 쓰기를 모두 소유한다. GUI 프로세스는 SQLite를 열지 않는다.
- GUI Go 경계는 Wails 요청을 인증된 localhost HTTP 요청으로 변환하고 데몬의 JSON 응답을
  프런트엔드에 전달한다.
- 트레이 조회는 `GET /v1/tray`, 수동 갱신은 `POST /v1/tray/refresh`를 사용한다.
- `GET /v1/tray`는 외부 벤더를 조회하지 않고 SQLite의 최신 상태로 트레이 스냅샷을 조립한다.
- `POST /v1/tray/refresh`는 벤더 갱신과 SQLite 저장을 마친 뒤 새 트레이 스냅샷을 `200`으로
  반환한다. GUI가 후속 GET의 순서를 조정하지 않는다.
- 두 경로는 기존 local ingest bearer token과 `X-Pulsemetry-Local: 1`을 요구하고 CORS를
  허용하지 않는다. endpoint는 `runtime.json`에서 찾되 localhost HTTP가 아니면 토큰을 보내지 않는다.
- GUI는 마지막 정상 응답을 메모리에 캐시한다. 데몬이 꺼졌을 때 SQLite 직접 조회로 우회하지 않는다.
- Wails 의존성을 `cmd/pulsemetry-gui` 경계에만 두고, 화면 계약을 순수 Go 타입으로 유지한다는
  ADR 0004의 나머지 결정은 유효하다.

## Alternatives Considered

### A. GUI가 SQLite를 read-only로 직접 조회한다

- 장점: 데몬이 꺼져도 과거 데이터를 볼 수 있고 HTTP 직렬화가 필요 없다.
- 단점: GUI가 DB 경로와 스키마를 알고, 수동 갱신 한 번이 데몬 IPC와 GUI DB 조회로 갈라진다.
- 탈락 이유: 데몬을 로컬 데이터의 단일 소유자로 만드는 경계보다 구현 책임이 넓게 퍼진다.

### B. 갱신은 204로 끝내고 GUI가 별도 GET을 보낸다

- 장점: 명령과 조회의 HTTP 의미가 분리된다.
- 단점: 왕복이 두 번이고 POST 성공 뒤 GET 실패가 가능하며 GUI가 호출 순서를 알아야 한다.
- 탈락 이유: 갱신 버튼은 갱신된 화면 한 장을 원하는 단일 사용자 동작이다.

## Consequences/Tradeoffs

### Positive

- SQLite 경로, 연결, SQL과 스키마 지식이 데몬에만 남는다.
- 수동 갱신과 그 결과가 하나의 요청·응답으로 묶인다.
- GUI는 로컬 API 계약과 화면 캐시만 관리한다.

### Negative

- 데몬이 꺼지면 GUI를 재시작한 뒤 과거 데이터를 새로 조회할 수 없다.
- 데몬과 GUI 사이의 JSON 계약, 인증, 타임아웃과 버전 호환성을 관리해야 한다.
- 기존 ingest token을 조회에도 재사용하므로 세션 정보를 읽을 수 있는 전용 토큰보다 권한이 넓다.
  별도 권한 경계가 필요해지면 조회 전용 키링 계정을 도입한다.

## Acceptance Criteria

- GUI 프로세스가 SQLite를 열지 않는다.
- 인증된 `GET /v1/tray`가 데몬이 조립한 스냅샷을 반환한다.
- 인증된 `POST /v1/tray/refresh`가 SQLite 저장 완료 뒤 스냅샷을 반환한다.
- 인증 누락, OPTIONS와 localhost가 아닌 endpoint가 거부된다.

## References

- [ADR 0004](0004-GUI-연동을-Go-패키지-공유로.md)
- [ADR 0011](0011-Codex-사용-한도는-App-Server를-통해-조회한다.md)
- `internal/localapi`
- `internal/dashboard/tray`
