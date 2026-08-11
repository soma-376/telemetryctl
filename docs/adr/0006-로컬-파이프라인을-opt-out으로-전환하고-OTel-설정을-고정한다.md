# 0006. 로컬 파이프라인을 opt-out 으로 전환하고 로컬 OTel 설정을 고정한다.

## Status
Accepted

ADR [0001](0001-로컬-OTLP-수신기-인라인-프록시-토폴로지.md) 의 "재배선은 opt-in, 기본 OFF" 결정(40행)을 대체한다.
같은 ADR 의 나머지 — 인라인 프록시 토폴로지, `localhost` 표기 제약, §5.4 상한선 — 는 그대로 유효하다.

## Context

PROJ-36 이 만든 로컬 파이프라인은 두 가지 이유로 절반만 살아 있었다.

**첫째, 아무도 켜지 않는다.** 재배선은 `telemetryctl local enable` 을 사용자가 직접 실행해야 하는 opt-in 이다
(`internal/installer/state.go` 의 `DefaultLocal`). enroll 만 한 사용자에게는 로컬 저장·세션 조립·집계가 전부 없는
것과 같고, PROJ-35 의 GUI 대시보드는 채울 데이터가 없다. ADR 0001 이 opt-in 을 고른 이유는 "이 저장소에서 처음으로
기존 사용자 설정을 바꾸는 코드"라는 신중함이었는데, 12단계가 이미 배포되어 enable/disable 왕복이 바이트 단위로
검증된 지금은 그 신중함의 대가만 남았다.

**둘째, 회사가 수집 범위를 좁히면 로컬 기능도 함께 죽는다.** `localManifest` 는 회사 manifest 를 깊은 복사한 뒤
endpoint 와 privacy 두 항목만 고쳐 썼다 (`internal/installer/local.go`, PROJ-36 시점). 그래서

- 회사 `signals.traces = false` → 로컬도 트레이스를 받지 못해 툴 타임라인이 빈다.
- 회사 `collect_tool_content = false` → 원문 보관이 껍데기가 된다.
- 회사 `otlp.protocol = http/json` → 로컬 구간까지 JSON 으로 돈다.

ADR [0003](0003-원문과-tool-details를-로컬에만-보관.md) 이 정한 방향은 원래 "로컬은 전부 받아 두고 상위 전달에서만
거른다" 였다. 위 목록은 그 방향이 privacy 두 필드에만 적용되고 나머지에는 적용되지 않았다는 뜻이다.

전제 하나가 이미 갖춰져 있다. 포워더는 회사 manifest 의 Privacy 로 `otlpdecode.Scrub` 을 태우는 프라이버시 집행
지점이다 (`internal/forward/forward.go`). 즉 로컬에서 무엇을 켜든 회사로 나가는 것은 회사가 정한 범위로 이미
좁혀지고 있었다 — **Privacy 에 한해서만.** Signals 는 아무도 보지 않았고, 지금까지 문제가 없었던 이유는 로컬
사본이 회사 signals 를 그대로 물려받아 애초에 벤더가 그 시그널을 내보내지 않았기 때문이다. 고정 프로필은 그
우연한 방어선을 없앤다.

## Decision

1. **로컬 파이프라인은 enroll 시 자동 배선된다.** `installer.Apply` 가 벤더 설정을 로컬 수신기로 돌리고
   `state.Local.Enabled` 를 켠다. 사용자의 탈출구는 `telemetryctl local disable` 이다.
2. **로컬 OTel 설정은 회사 manifest 와 무관하게 고정한다** (`installer.localProfile`). endpoint 는
   `http://localhost:<port>`, protocol 은 `http/protobuf`, compression 은 없음, signals 는 셋 다 켬,
   privacy 는 `collect_assistant_responses` 만 끄고 나머지 전부 켬. 값은 PROJ-45 티켓의 참고 자료를 따른다.
   응답 원문만 끄는 이유는 로컬 파이프라인이 그것을 쓰지 않으면서 배치 크기만 키우기 때문이다.
3. **회사 manifest 준수는 전적으로 `internal/forward` 가 집행한다.** 축이 둘이다.
   - `Signals` — 회사가 끈 시그널은 `Enqueue` 가 큐에 넣지 않는다 (신규).
   - `Privacy` — 회사가 금지한 원문은 `Scrub` 이 지운 뒤 보낸다 (기존).
4. **`state.json` 에는 회사 manifest 원본을 저장한다.** 고정 프로필은 벤더 설정 생성에만 쓰고 어디에도 남기지
   않는다. 3번의 두 집행 기준이 전부 이 값에서 나오므로, 여기가 오염되면 집행이 통째로 무력화된다.
5. **기존 설치자는 자동 전환하지 않는다.** state schema 는 4 를 유지하고 마이그레이션을 추가하지 않는다.
   전환은 `local enable` 로 명시적으로 한다. 마이그레이션은 메모리에서만 일어나므로 거기서 `Enabled` 만 켜면
   상태는 "로컬" 인데 설정 파일은 회사 직결인 불일치가 생긴다.
6. **회사 telemetry token 은 키링(`credential.AccountTelemetry`)에 대피한다.** 벤더 설정에는 로컬 ingest
   토큰만 남으므로 이것이 유일한 사본이다. enroll 은 enrollment 응답에서 직접, `local enable` 은 벤더 파일에서
   되읽어 넣는다. `reconnect` 는 배선된 설치의 벤더 설정을 건드리지 않고 이 대피본만 갱신한다.

## Alternatives

### A. opt-in 을 유지하고 설치 안내에서 enable 을 권한다
- 장점: 기존 사용자 설정을 건드리는 코드 경로가 늘지 않는다. ADR 0001 을 그대로 둘 수 있다.
- 단점: 실제 채택률은 0 에 수렴한다. GUI 티켓(PROJ-35)이 데이터 없는 화면을 만들게 된다.
- 탈락 이유: 로컬 화면이 제품의 산출물인데 그 데이터 공급을 사용자의 추가 행동에 맡기는 구조다.

### B. 고정 프로필 대신 manifest 에 "로컬 수집 범위" 필드를 추가한다
- 장점: 회사가 로컬 수집까지 정책으로 통제할 수 있다.
- 단점: 서버 계약(`contracts/*.schema.json`) 변경이 필요하고 서버 팀과의 동기 배포가 걸린다. 게다가 로컬 수집은
  **사용자 본인의 데이터를 본인 기계에 두는 일**이라 회사 정책의 대상으로 삼을 근거가 약하다.
- 탈락 이유: 범위 대비 비용이 크고, ADR 0003 이 이미 "로컬 보관과 상위 전달은 다른 문제"로 갈라 두었다.

### C. 시그널 게이팅을 `Enqueue` 가 아니라 `deliver` 에서 한다
- 장점: 큐에 들어간 것과 실제 보낸 것의 차이를 통계로 볼 수 있다.
- 단점: 상위가 받지도 않을 페이로드가 큐의 항목·바이트 예산을 먹고, 그만큼 실제로 보낼 배치가 포화로 버려진다.
- 탈락 이유: 큐 예산은 유계이고 (`DefaultQueueSize`, `DefaultMaxQueueBytes`) 그것을 낭비할 이유가 없다.
  통계는 `Stats.DroppedSignalDisabled` 로 따로 남긴다.

## Consequences/Tradeoffs

### Positive
- enroll 한 모든 사용자가 로컬 데이터를 갖는다. PROJ-35 의 GUI 가 채울 데이터가 생긴다.
- 회사가 수집 정책을 좁혀도 로컬 기능이 온전하다. 두 범위가 독립적으로 움직인다.
- 로컬 구간의 인코딩·프로토콜·압축이 고정되어 재현 가능해진다. 회사 manifest 를 몰라도 로컬 동작을 예측할 수 있다.
- `reconnect` 가 재배선을 조용히 되돌리던 버그가 함께 고쳐졌다 — opt-out 전환이 아니었다면 드물게만 드러났을 문제다.

### Negative
- **데몬이 떠 있지 않은 채 배선된 상태는 텔레메트리가 로컬에도 회사에도 남지 않는 상태다.** opt-in 시절에는
  `local enable` 을 친 사람만 그 상태를 지나갔지만, 이제 enroll 한 모든 사람이 지나간다.
  - 완화: `enroll` 이 `warnDaemonNotRunning` 으로 크게 알리고 `telemetryctl daemon` 실행을 안내한다.
  - **근본 해결은 데몬 자동 실행 등록이고, 그것이 이 결정의 진짜 선행 조건이다.** 아래 Follow-up 참고.
- 로컬에 원문·raw API body 가 기본으로 쌓인다. ADR 0003 의 보관·삭제 장치(`--no-store-content`, `purge --content`,
  보존일)가 그대로 대응하지만, 기본값이 "쌓는다" 라는 사실 자체는 감수하는 것이다.
- `local disable` 결과가 PROJ-45 이전 바이너리의 출력과 바이트 동일하지 않다 — 관리 키가 늘었기 때문이다
  (`OTEL_TRACES_EXPORT_INTERVAL`). 불변식 3 이 요구하는 것은 "같은 입력 → 같은 출력" 이지 버전 간 동일성이 아니다.
- 벤더 설정을 바꾸는 코드 경로가 `local enable` 하나에서 `Apply` 까지 둘로 늘었다. 두 경로가 같은 결과를 내는지는
  `TestEnroll배선과enable배선이같은설정을만든다` 가 바이트 단위로 지킨다.

## Follow-up
- **데몬 자동 실행 등록 (launchd·systemd·Task Scheduler).** 이 ADR 의 Negative 첫 항목을 없애는 유일한 방법이고,
  `README.md` 와 `docs/local-pipeline.md` 가 이미 차단성 선행 조건으로 지목해 둔 항목이다. 지금은 경고가 최선이다.
- **기존 설치자 전환 정책.** 자동 실행 등록이 끝난 뒤 state schema 5 로 일괄 전환할지 다시 판단한다.
  지금 결정한 "건드리지 않는다" 는 그때까지의 잠정 상태다.
- **grpc 상위 전달.** grpc 테넌트는 배선 대상에서 빠진다 (`forward.ErrGRPCUnsupported`). 지원이 생기면
  `Apply` 의 강등 분기를 걷어낸다.
- **Codex `log_user_prompt` 와 `environment`.** 티켓 참고 자료는 각각 `false` 와 `"e2e"` 였으나 전자는 Claude 와의
  대칭을 위해 `true` 로, 후자는 `resource_attributes` 파생을 유지하기로 했다. Codex 프롬프트 수집이 실제로
  필요한지는 세션 조립 결과를 보고 다시 본다.
