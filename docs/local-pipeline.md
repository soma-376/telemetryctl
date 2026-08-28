# 로컬 데이터 파이프라인 — 아키텍처 및 GUI 계약

## 1. 문서 목적

이 문서는 PROJ-36 이 추가한 **로컬 계층**의 구현을 서술한다. 데몬이 loopback OTLP 수신기를 띄워
Claude Code·Codex 의 시그널을 직접 받고, 정규화·집계해 로컬 SQLite 에 저장한 뒤 회사 Collector 로
전달하는 구조다.

> **v3 전환 중:** SQLite 스키마는 새 `vendors → sessions → turns → events` 모델로 교체됐지만,
> 이 문서가 설명하는 쓰기·집계·조회 런타임은 아직 기존 모델을 사용한다. DB를 열면 v3 마이그레이션이
> 기존 도메인 데이터를 삭제하므로 후속 런타임 전환 전에는 데몬·CLI 기능이 실패한다. 현재 동작을
> 설명하는 아래 절과 ADR은 전환 전 구현 기록이며, 새 DDL 계약은 [SQLite 스키마](sqlite-schema/README.md)를
> 우선한다.

독자는 둘이다.

- 이 저장소를 이어받는 개발자 — 2·3·4·5·7절
- GUI(PROJ-35)를 만드는 사람 — 6절이 계약이고 5절이 그 배경이다

결정의 **배경과 대안**은 ADR 에 있다. 이 문서는 "무엇이 어떻게 구현돼 있는가" 만 다룬다.

| ADR | 내용 |
|---|---|
| [0001](adr/0001-로컬-OTLP-수신기-인라인-프록시-토폴로지.md) | loopback 수신기 + 상위 전달 (재배선 opt-in 부분은 0006 이 대체) |
| [0002](adr/0002-로컬-집계-저장소로-SQLite-채택.md) | `modernc.org/sqlite` + WAL/FTS5 |
| [0003](adr/0003-원문과-tool-details를-로컬에만-보관.md) | 원문·tool details 는 로컬에만, 포워더가 제거해 전달 |
| [0004](adr/0004-GUI-연동을-Go-패키지-공유로.md) | Wails v3 가 `internal/dashboard` 직접 import |
| [0005](adr/0005-세션을-1급-엔티티로-조립.md) | 이벤트를 `session.id` 로 묶어 세션을 1급 엔티티로 |
| [0006](adr/0006-로컬-파이프라인을-opt-out으로-전환하고-OTel-설정을-고정한다.md) | 배선은 opt-out 기본 ON, 로컬 OTel 설정 고정, 회사 준수는 forward 가 집행 |
| [0007](adr/0007-데몬은-비정상-종료일-때만-자동-재시작한다.md) | 자동 실행 등록의 재시작 정책 — 비정상 종료일 때만 되살린다 |
| [0008](adr/0008-로컬-데이터를-400일간-보존한다.md) | 모든 로컬 데이터 400일 고정 보존 |

기존 설치 아키텍처는 [설치 아키텍처](installation-architecture.md)에 있다. 이 문서의 `§4.5`·`§5.4`
같은 표기는 그 문서의 절 번호다.

---

## 2. 토폴로지와 데이터 흐름

```text
Claude Code / Codex
  (OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:<port>,
   OTEL_LOG_USER_PROMPTS=1, OTEL_LOG_TOOL_DETAILS=1)
        │ OTLP/HTTP  (protobuf | json, identity | gzip)
        ▼
telemetryctl daemon
  ├── receiver     127.0.0.1 + [::1] 두 리스너, bearer 인증, 4 MiB 캡, 유계 큐 → 워커 2개
  │                   └─ 워커가 otlpdecode.Decode 로 정규화
  │
  ├── forward      Batch.Body(원본 바이트) → otlpdecode.Scrub → 회사 Collector
  │                   (여기만 네트워크로 나간다)
  │
  └── pipeline     Batch.Result → dedup 창 → session.Assembler + rollup.Aggregator
                                            → store.Batch (한 트랜잭션)
        │
        ▼ (read-only, WAL)
internal/dashboard  ←  telemetryctl stats·sessions·status
                    ←  gui/ (Wails v3, 별도 go.mod)
```

한 배치가 두 갈래로 갈린다는 것이 핵심이다.

- **원본 바이트**(`receiver.Batch.Body`)는 포워더로 간다. 포워더가 회사 manifest 의 `Privacy` 기준으로
  원문·tool details 를 제거하고 재인코딩해 상위로 보낸다.
- **정규화 결과**(`receiver.Batch.Result`)는 세션 조립기·롤업 집계기·저장소로 간다. 원문은 여기서
  `event_content` 로만 흐른다.

`daemon/pipeline.go` 의 `Consume` 은 **`forward.Enqueue` 를 직렬화 지점 밖에서 먼저 호출한다.**
SQLite 가 느려도 상위 전달이 막히지 않아야 하기 때문이다(§5.4).

집계기 두 개는 `pipeline.run` 이 소유하는 고루틴 하나가 **같은 순서로** 먹인다. 순서가 갈리면
`sessions` 합계와 `rollup_hourly` 합계가 갈린다.

---

## 3. 패키지 레이아웃

```text
internal/
  event/        정규화 이벤트 타입 · DedupKey · Content · Path · CumulativeState  (표준 라이브러리만, IO 없음)
  otlpdecode/   protobuf·protojson 디코드, content 제거 재인코딩(Scrub)          (proto 의존 격리)
  receiver/     loopback OTLP/HTTP 수신기 + Sink 인터페이스
  forward/      상위 Collector 전달 (유계 큐 · 제한된 재시도 · 토큰 갱신)
  session/      이벤트 → 세션 조립, 제목 휴리스틱, 파일·툴 추출                   (순수 함수, 시계 미접근)
  rollup/       시간 버킷 집계, delta/cumulative 분기                             (순수 함수, IO 없음)
  store/        SQLite 스키마·마이그레이션·쓰기·보존 정책·read-only 열기
  dashboard/    화면별 조회 API                                                   (Wails 의존 없음)
  runtimeinfo/  runtime.json (비밀 없음: 주소·pid·데이터 경로)
  autostart/    로그인 시 데몬 자동 실행 등록 (launchd LaunchAgent · systemd user unit)
  daemon/       위 여덟 개를 잇는 배선 + 틱 루프 + graceful shutdown
gui/            Wails v3 앱 (별도 go.mod, 아직 없음 — PROJ-35)
```

### 3.1 의존 방향

`event` 가 파이프라인 전체의 공용 어휘이고 아무것도 import 하지 않는다. 나머지는 전부 `event` 를
향한다. `daemon` 만이 다른 패키지를 여럿 import 한다 — 배선이 그 패키지의 유일한 임무다.

`receiver` 는 `store`·`session`·`rollup`·`forward` 중 어느 것도 import 하지 않고 `Sink` 인터페이스만
노출한다. 그래서 "OTLP 를 받는다" 는 관심사를 파이프라인 없이 `httptest` 로 전부 테스트할 수 있다.

`installer` 는 `receiver`·`store` 를 import 하지 않는다. 하면 `enroll`·`status` 경로까지 protobuf
디코더와 SQLite 드라이버를 끌고 들어온다. 그래서 `installer.DefaultLocalPort`가
`receiver.DefaultPort`와 같은 값을 따로 갖는다. 두 값이 어긋나면 `cmd/telemetryctl`의 테스트가 잡는다.

### 3.2 proto 의존성은 `otlpdecode` 에 격리돼 있다 (계약)

**`go.opentelemetry.io/proto/otlp` 와 `google.golang.org/protobuf` 를 import 하는 패키지는
`internal/otlpdecode` 하나뿐이다.** 이것은 지켜야 할 계약이다.

- 파이프라인의 다른 패키지는 protobuf 타입을 보지 않는다. `receiver` 조차 보지 않는다.
- 그 대가로 `otlpdecode` 는 인코딩 두 가지(`EncodingProtobuf`·`EncodingJSON`)를 모두 다루고,
  같은 골든 픽스처가 양쪽 경로에서 같은 결과를 내는지 테스트가 단언한다.
- OTLP/JSON 의 `traceId`·`spanId` 는 hex 인데 protojson 은 base64 를 기대한다. 32자 hex 는 base64
  로도 **에러 없이** 해독돼 엉뚱한 ID 가 만들어지므로 JSON 경로에서만 앞뒤로 변환한다
  (`otlpdecode/jsonids.go`).

### 3.3 어휘가 `event` 로 통합돼 있다

2~5단계를 병렬로 만들면서 패키지 경계의 어휘가 세 벌로 갈렸고, `store` 가 그것을 SQLite 스키마로
굳히기 전에 합쳤다(`PROJ-36 refactor: 원문·경로·cumulative 어휘를 event 패키지로 통합`). 결과:

| 어휘 | 소유자 | 이유 |
|---|---|---|
| `event.Content` · `event.ContentKind` | `event` | `otlpdecode` 가 뽑고 `session` 이 제목을 만들고 `store` 가 `event_content.kind` 에 쓴다. 세 지점이 같은 타입이어야 어긋남을 컴파일러가 잡는다 |
| `event.Path` · `event.NormalizePath` | `event` | `project_hash`+`project_name`, `file_path_hash`+`file_name`, `target_hash`+`target_name` 세 쌍의 **유일한 생산자**. 전체 경로가 이 타입을 통과할 자리가 없다 |
| `event.CumulativeState` | `event` | `session` 과 `rollup` 이 각자 갖고 있던 리셋 판정이 이미 갈라져 있었다. `Step` 하나로 합치고 계열 저장·용량 정책만 각자에게 남겼다 |

`otlpdecode.Content`·`otlpdecode.Target` 은 남아 있지만 `event.Content`/`event.Path` 를 싣는
껍데기다 — `EventIndex`·`DedupKey` 는 한 번의 디코드 결과 안에서만 뜻이 있기 때문이다.

이 리팩터가 실제 버그 두 건을 고쳤다. (a) 디코더가 `tool_input` 의 `file_path` 를 버리고 있어
「파일 변경」 화면이 영원히 비어 있었을 것이고, (b) cumulative 리셋 판정이 `start_time` 을 보지 않아
벤더 재시작 후 누적분을 잃거나 순서 뒤집힘을 리셋으로 오인해 이중 집계했다.

### 3.4 `daemon` 이 중복 제거를 한 겹 더 한다

같은 이벤트를 세 소비자가 받는데 중복에 대한 태도가 셋 다 다르다.

| 소비자 | 중복 처리 |
|---|---|
| `store` | `events.dedup_key` UNIQUE. 영구적이고 재시작에도 유효하다 |
| `rollup` | 자체 유계 FIFO 창(16384). `rollup_hourly` 는 UPSERT 누적이라 한 번 더한 값을 되돌릴 수 없다 |
| `session` | **없다.** 조립기는 받은 것을 그대로 세고 타임라인에 붙인다 |

그래서 배선이 걸러 주지 않으면 exporter 재전송 한 번에 툴 타임라인이 부풀고 `sessions` 합계가
`rollup_hourly` 와 갈린다 — `internal/session/agreement_test.go` 가 막으려는 바로 그 어긋남이다.
`internal/daemon/dedup.go` 의 `dedupWindow` 가 세 소비자 앞에서 한 번만 거른다. 창 크기는 `rollup`
과 같은 **16384** 다. 다르면 그 사이 구간에서만 판정이 갈리는, 가장 재현하기 어려운 불일치가 생긴다.

창 밖으로 밀려난 중복은 통과하지만 `store` 의 UNIQUE 가 잡는다. 즉 창 크기는 정확성이 아니라
"얼마나 일찍 거르느냐" 의 문제이고, 창을 지나친 중복의 유일한 실질 피해는 세션 타임라인 중복이다.

---

## 4. 프라이버시 불변식

ADR 0003 이 정한 규칙의 구현 형태다. **로컬 저장 규칙과 상위 전달 규칙 두 벌이 있다.**

### 4.1 무엇이 어디에 남는가

| 데이터 | `events` | `event_content` | 상위 Collector |
|---|---|---|---|
| 전체 작업·파일 경로 | **없음** (해시+basename 만) | `tool_input` 원문에 **그대로 남는다** | 없음 (`tool_details` 제거) |
| 프롬프트·응답 원문 | 길이만 (`prompt_length`·`response_length`) | 16KB 캡으로 저장 | 없음 (`user_prompts`·`assistant_responses` 제거) |
| `user.email`·`user.id`·`user.account_uuid`·`organization.id` | **없음** | 해당 속성이 원문에 실려 오지 않는 한 없음 | 회사 manifest 가 정한다 |
| 토큰(인증) | 없음 | 없음 | Authorization 헤더로만 |

`events` 에 전체 경로가 없는 것은 **스키마로 보장된다.** 속성은 allowlist 컬럼 21개로만 받고
catch-all `map[string]string` 컬럼이 없다(`internal/store/schema.go` 의 `events` DDL,
`internal/event/event.go` 의 `Attributes`). 경로를 `Attributes` 에 넣는 유일한 통로가
`event.NormalizePath` 이고 `event.Path` 에는 해시·basename·확장자 자리밖에 없다.

**`event_content.body` 는 설계상 원본 그대로다.** `tool_input` 원문에는 전체 경로가 **반드시** 남아야
한다 — 그게 없으면 `session_files` 를 만들 수 없고 「파일 변경」 화면이 영원히 빈다. ADR 0003 이
이 자리를 16KB 캡·400일 보존·상위 미전달 세 조건으로 허용한다(ADR 0008). `internal/otlpdecode/target.go` 와
`internal/event/content.go` 의 주석이 이 구분을 명시적으로 못박고, 프라이버시 회귀 테스트가
`Targets`·조립된 `Session` 에는 전체 경로가 없고 `Content.Body` 에는 있다는 것을 양쪽으로 단언한다.

### 4.2 상위 전달의 제거

**회사 manifest 준수는 전부 `internal/forward` 가 집행한다** (PROJ-45, ADR 0006). 로컬 배선의 벤더 설정은
회사 manifest 와 무관하게 고정이므로(§7.2), 여기가 유일한 방어선이다. 축이 둘이다.

**축 1 — `Signals`.** 회사가 끈 시그널은 `Enqueue` 가 큐에 넣지 않는다 (`forward.signalEnabled`).
`deliver` 가 아니라 `Enqueue` 에서 막는 이유는, 상위가 받지도 않을 페이로드가 큐의 항목·바이트 예산을
먹으면 그만큼 실제로 보낼 배치가 포화로 버려지기 때문이다. 알 수 없는 kind 는 보내지 않는다 —
새 시그널의 기본값이 "보낸다" 이면 회사가 동의한 적 없는 데이터가 조용히 나간다.
집계는 `Stats.DroppedSignalDisabled` 이고 데몬 종료 요약의 `시그널차단` 항목이다.

**축 2 — `Privacy`.** 포워더에 제거 규칙이 하나도 하드코딩돼 있지 않다. `forward.New` 가
`otlpdecode.PolicyFromPrivacy(manifest.Privacy)` 로 정책을 한 번 만들고, 워커가 전송 직전
`otlpdecode.Scrub` 을 한 번 호출한다. manifest 가 항목을 허용으로 바꾸면 그 항목은 자동으로 통과한다.

- 기준은 **회사 manifest 원본**이다. `localProfile` 이 만드는 고정 프로필이 아니다
  (`daemon/runner.go` 의 `forward.Options{Manifest: d.state.Manifest}`). `state.json` 에 회사 원본이
  저장되는 것도 이 때문이다 — 거기가 오염되면 두 축이 통째로 무력화된다.
- `Scrub` 실패는 전송하지 않는다. 정리되지 않은 본문을 흘려보내느니 버린다(`DroppedScrub`).
- denylist 원칙이다 — 정책이 지목한 키만 빼고 나머지는 순서까지 그대로 흘린다.
- 입력 인코딩을 보존한다. 큐 항목이 `Encoding` 을 들고 다닌다.

그래서 **회사로 나가는 데이터는 재배선 전후로 동일하다.** 이것이 이 파이프라인의 가장 중요한 불변식이고,
`localProfile` 이 회사 manifest 원본을 절대 오염시키지 않는 이유이기도 하다 (`cloneManifest`).

### 4.3 회귀 검증 절차

> **계획서 「검증」 5번은 틀렸다.** 계획서는 `sqlite3 .dump | grep -c '/Users/'` 로 0 을 기대하지만,
> `.dump` 는 `event_content` 를 포함하고 그쪽에는 전체 경로가 **있어야 정상**이다. 검증은
> `events` 테이블로 좁혀야 한다.

```sh
DB=/tmp/pm-test/pulsemetry.db

# (1) events 에 전체 경로가 없어야 한다 — 0 이어야 통과
sqlite3 "$DB" ".mode list" "SELECT * FROM events;" | grep -c '/Users/'

# (2) session_files·tool_events 도 basename 만 있어야 한다 — 0 이어야 통과
sqlite3 "$DB" "SELECT file_name FROM session_files;"   | grep -c '/'
sqlite3 "$DB" "SELECT target_name FROM tool_events;"   | grep -c '/'

# (3) 이메일·조직 ID 는 어디에도 없어야 한다 — 0 이어야 통과 (event_content 포함)
sqlite3 "$DB" ".dump" | grep -ciE 'user\.(email|id|account_uuid)|organization\.id'

# (4) event_content 의 tool_input 에는 전체 경로가 "있어야" 한다 — 0 이면 오히려 회귀다
sqlite3 "$DB" "SELECT body FROM event_content WHERE kind='tool_input';" | grep -c '/'
```

(4)를 함께 확인하는 것이 중요하다. "다 지워 버려서 통과하는" 구현을 (1)~(3)만으로는 잡을 수 없다.
같은 원칙이 `daemon` 의 엔드투엔드 테스트에도 적용돼 있다 — 상위 본문 검증이 사라져야 할 것과
남아야 할 것을 양쪽 다 단언한다.

---

## 5. SQLite 스키마

DDL, 테이블 관계, PRAGMA, 보존 계층, 마이그레이션 규칙은
[SQLite 스키마 문서](sqlite-schema/README.md)로 분리했다. 테이블별 문서는 해당 목차에서 찾을 수 있다.

스키마 버전 3은 기존 도메인 데이터를 보존하거나 백필하지 않는다. `meta`와 DB 파일만 유지하며,
쓰기·조회·보존 런타임 전환은 이 변경의 범위 밖이다.

이 절 번호는 GUI 계약과 운영 절을 가리키는 기존 링크가 깨지지 않도록 유지한다.

---

## 6. GUI 계약 (PROJ-35)

ADR 0004 의 구현이다. **`internal/dashboard` 는 순수 Go 이고 Wails 를 import 하지 않는다.**

### 6.1 공개 API

```go
func Open(dbPath string) (*Reader, error)

func (r *Reader) Today(ctx context.Context, tz string) (TodaySummary, error)
func (r *Reader) Sessions(ctx context.Context, q SessionQuery) ([]SessionRow, error)
func (r *Reader) Session(ctx context.Context, id string) (SessionDetail, error)
func (r *Reader) Breakdown(ctx context.Context, q BreakdownQuery) ([]Row, error)
func (r *Reader) Search(ctx context.Context, q SearchQuery) ([]Hit, error)
func (r *Reader) Vendors(ctx context.Context) ([]VendorStatus, error)
func (r *Reader) MCPUsage(ctx context.Context, lastNSessions int) ([]MCPRow, error)
func (r *Reader) Status(ctx context.Context) (Status, error)

func (r *Reader) Available() bool     // DB 파일이 실제로 열렸는가
func (r *Reader) Reopen() error       // 아직 못 연 DB 를 다시 시도
func (r *Reader) Close() error        // 열린 적 없어도 안전 (ServiceShutdown)
func (r *Reader) Path() string
func (r *Reader) DataDir() string
```

계획서에 없던 것이 셋 추가됐다. `Available()`·`Reopen()`·`Close()` 다. `Reopen` 은 **정상 시나리오**를
위해 있다 — GUI 가 먼저 뜨고 나중에 `telemetryctl local enable` 로 데몬이 DB 를 만드는 순서가 정상이고,
이 메서드가 없으면 그 사용자는 앱을 껐다 켜야 데이터를 본다.

화면 대응:

| 화면 요소 | 메서드 |
|---|---|
| Today 4개 카드 + 어제 대비 %, 상단 "N agents active" | `Today(tz)` |
| 오늘의 활동 / 세션 리스트 | `Sessions(q)` |
| 세션 상세 (수치 + 파일 변경 + 툴 타임라인 + MCP) | `Session(id)` |
| Agent 사용 비율 · 시간대별 집중도 · 일별 추이 | `Breakdown(q)` |
| 검색 (제목·파일명·원문) | `Search(q)` |
| Settings 연결 상태 | `Vendors()` |
| Insights MCP 카드 | `MCPUsage(n)` |
| Settings 저장소·데몬 상태 | `Status()` |

`Today` 의 `Cards` 는 `cost_usd`·`tokens`·`sessions_started`·`active_seconds` 네 장이다
(`dashboard.MetricCostUSD` 등 상수). `Breakdown` 은 `Dim` 여섯 가지 × `BucketBy` 세 가지
(`BucketKey`=""·`BucketHourOfDay`·`BucketDay`) 조합이다.

### 6.2 `json` 태그 규약

- **모든 공개 구조체 필드에 `json` 태그가 붙어 있다. 규약은 snake_case.** 태그가 곧 TS 필드명이라
  이름을 바꾸면 프런트엔드가 조용히 `undefined` 를 읽는다.
- **nullable 은 포인터다.** `SessionRow.EndedAt *int64`, `ToolRow.DurationMS *int64`,
  `ToolRow.Success *bool`. 0 으로 눕히면 "1970년에 끝난 세션" 과 "진행 중", "0ms 툴 호출" 과
  "소요 시간 미상" 이 구분되지 않는다. JSON 에서는 `null` 이 된다.
- **밖으로 나가는 시각은 전부 UTC unix 초다.** `events.ts` 만 나노초이고 그 값은 이 패키지를
  지나가지 않는다.
- **슬라이스는 비어 있되 `nil` 이 아니다.** JSON `null` 에 `.map` 을 걸면 프런트엔드가 터진다.
- **에러 메시지에 SQL 이 들어가지 않는다.** Go 의 `error` 가 Promise reject 로 전파돼 사용자에게
  그대로 보인다. `queryErr` 가 이 규칙을 소유하고, 닫힌 핸들로 호출해 `SELECT`·`FROM`·`JOIN` 부재를
  단언하는 테스트가 있다.
- `Card.HasBaseline` 이 `false` 면 어제 값이 0 이라 증감률이 정의되지 않는다는 뜻이다. 화면은
  `+∞%` 대신 "신규" 같은 표시를 골라야 한다. (0 으로 나눈 `+Inf` 는 `encoding/json` 이 직렬화하지
  못해 조회 전체가 실패한다.)
- `SessionDetail.ToolsTruncated` 가 `true` 면 타임라인이 1000행에서 잘렸다는 뜻이다.

### 6.3 DB 없음은 error 가 아니라 빈 결과다

`ServiceStartup` 이 error 를 반환하면 **앱 기동 자체가 중단된다.** 그런데 DB 가 없는 상태(미설치 ·
`local enable` 전 · 데몬 첫 실행 전)는 정상이다.

- `Open` 은 DB 부재를 error 로 만들지 않고 "비어 있는 `Reader`" 를 돌려준다(`Available()` 이 `false`).
- 모든 조회가 **모양을 유지한 채** 빈 결과를 준다. `Today` 는 카드 4장을 채우고,
  `Breakdown(hour_of_day)` 는 0값 24행 골격을 주며, 슬라이스는 전부 비어 있되 `nil` 이 아니다.
- `Session(id)` 는 없는 세션에 `Found: false` 를 준다. 보존 정책이 지운 세션의 id 를 화면이 아직
  들고 있는 것은 정상 상황이고, 그때 앱이 에러 토스트를 띄울 이유가 없다.
- **진짜 실패는 여전히 에러다.** 빈 경로, 디렉터리를 지목한 경로, 잘못된 시간대 문자열.

### 6.4 `Today(tz)` 의 시간대 처리

`rollup_hourly.hour` 는 **UTC** 시간 버킷이고 화면은 **사용자 시간대**의 "오늘" 을 묻는다.
`Asia/Seoul` 의 오늘은 UTC 로 어제 15:00 에 시작한다. UTC 자정으로 잘라 답하면 매일 9시간이
어긋나고, 그 오차는 아침에 크고 저녁에 사라져 "가끔 숫자가 이상하다" 로만 보고된다.

구현(`internal/dashboard/timezone.go`):

- 경계 계산은 전부 `time.Location` 위에서 한다. 어제는 `AddDate` 달력 연산이라 DST 로 하루가
  23/25시간인 날에도 맞는다.
- 시간 버킷은 **자기 시작 시각이 속한 현지 날짜에 통째로** 귀속된다. 겹치지도 비지도 않는 규칙이라
  합계가 두 번 세지지 않는 대신, UTC+5:30 같은 30·45분 오프셋에서는 하루 경계에 최대 한 버킷만큼의
  근사가 남는다. 정시 오프셋에서는 정확하다.
- `hour_of_day`·`day` 축은 SQL 이 아니라 Go 에서 묶는다. SQL 로 하려면 고정 오프셋을 박아야 하고
  그러면 DST 전환일이 한 시간 밀린다.
- 빈 문자열은 UTC 다. **잘못된 시간대 문자열은 명확한 에러로 거부한다** — 조용히 UTC 로 떨어지면
  사용자는 자기 시간대가 무시된 줄 모른 채 9시간 어긋난 숫자를 본다.
- `TodaySummary.TZ` 로 실제 적용된 시간대 이름이 되돌아온다. 화면이 무엇을 기준으로 계산됐는지
  확인할 수 있어야 한다.
- 패키지가 `_ "time/tzdata"` 를 import 한다. Windows·최소 컨테이너에는 시스템 tz DB 가 없어
  `LoadLocation` 이 실패하고, 그러면 이 API 의 핵심 인자가 통째로 못 쓰인다. 표준 라이브러리라
  `go.mod` 는 바뀌지 않고 대가는 바이너리 수백 KB 다.

### 6.5 `gui/go.mod` 의 모듈 경로 제약 — 먼저 읽어라

**`internal/` 은 Go 언어 차원의 접근 제한이다.** `gui/` 가 별도 모듈이므로 그 규칙을 만족시키지
못하면 `internal/dashboard` import 가 **컴파일 단계에서 거부된다.** 모르고 시작하면 첫 빌드에서 막힌다.

상위 모듈 경로는 `github.com/your-org/pulsemetry` 다. 따라서:

```text
gui/go.mod
  module github.com/your-org/pulsemetry/gui      ← 반드시 상위 모듈 경로 아래
  require github.com/your-org/pulsemetry v0.0.0
  replace github.com/your-org/pulsemetry => ../   ← 로컬 소스를 가리킨다
```

`module pulsemetry-gui` 나 `module github.com/your-org/pulsemetry-gui` 처럼 상위 경로 밖에 두면
`use of internal package ... not allowed` 로 거부된다. 이 배치를 벗어날 방법은 없다 — 유일한 대안은
`dashboard` 를 `internal/` 밖으로 옮기는 것인데, 그러면 공개 API 표면이 하나 늘어난다.

### 6.6 Wails 쪽

```go
// gui 쪽 — 여기서만 Wails
func (s *DashboardService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error
func (s *DashboardService) ServiceShutdown() error
func (s *DashboardService) Today(tz string) (dashboard.TodaySummary, error)
```

`application.NewService(&DashboardService{})` 로 등록하고 `wails3 generate bindings` 로 TS 바인딩을
생성하면 JS 에서 `await DashboardService.Today("Asia/Seoul")` 이 된다. Go 의 `error` 는 Promise
reject 로 전파된다.

DB 경로는 `runtime.json`(7.4절)의 `database_path` 에서 얻거나, `~/.pulsemetry/pulsemetry.db` 를
직접 쓴다. **GUI 는 SQLite 를 직접 열지 않는다** — 스키마 지식은 `internal/dashboard` 밖으로 나가지
않는다.

읽기 커넥션은 최대 4개, 유휴 30초에 닫힌다. 화면을 오래 안 보는 동안 파일 핸들을 붙잡고 있으면
Windows 에서 데몬의 prune 이 막힌다.

### 6.7 CI

현재 `.github/workflows/go.yml` 에 **`gui/` 스텝이 없다.** 디렉터리와 별도 `go.mod` 가 아직 없어
지금 넣으면 무조건 실패하기 때문이다. GUI 티켓에서 `working-directory: gui` 스텝과 `gui/go.sum`
캐시 경로를 함께 추가해야 한다. 검증 명령은 `(cd gui && go build ./...)` 다.

---

## 7. 운영

### 7.1 `telemetryctl daemon`

```text
telemetryctl daemon [--listen localhost:4318] [--data-dir <경로>] [--state <경로>]
                    [--no-receiver] [--no-forward] [--no-store-content]
                    [--interval 30s]
```

| 플래그 | 동작 |
|---|---|
| `--listen` | `localhost:4318` 또는 `4318`. loopback 호스트만 받는다. **명시하면 포트 폴백 없이 하드 실패** |
| `--data-dir` | SQLite·`runtime.json` 위치. 미지정 시 `state.Local.DataDir` → `~/.pulsemetry` |
| `--no-receiver` | 수신기를 띄우지 않는다. 이때는 `runtime.json` 도 쓰지 않는다(듣는 곳이 없다) |
| `--no-forward` | 상위 전달 없이 수신·로컬 집계만. `grpc` manifest 에서의 탈출구이기도 하다 |
| `--no-store-content` | 원문을 로컬에 저장하지 않는다. **끄는 방향으로만 작동한다** — 플래그로 켜 주지는 않는다 |
| `--interval` | 세션 마감·스냅샷 저장 주기 (기본 30초) |

데몬은 `enroll` 된 상태를 요구한다(`state.json` 이 없으면 기동 실패). 회사 manifest 가 `grpc` 면
**하드 실패**한다 — 조용히 수신만 하면 벤더 설정이 우리를 가리키는 순간 회사 Collector 로 가던
스트림이 아무도 모르게 끊긴다. 오류 메시지가 `--no-forward` 탈출구를 안내한다.

틱 주기: flush 2초(또는 512 이벤트) · 세션 30초 · prune 1시간(+기동 직후 1회) · 토큰 확인 15분.
크래시 시 잃는 양의 상한이 곧 flush 주기다.

세션 틱 안의 순서가 load-bearing 이다: **`Advance` → `Snapshot` → flush → 조립기 `Prune`.**
`Prune` 을 앞에 두면 데몬이 몇 시간 잠들었다 깰 때, 마감되는 순간 이미 TTL(2시간)을 넘긴 세션이
`store` 에 한 번도 쓰이지 못하고 사라진다.

종료는 **수신기 → 파이프라인 → 포워더 → DB → `runtime.json`** 순서로 15초 예산 안에서 끝난다.
파이프라인이 제한 시간을 넘겼으면 DB 를 닫지 않는다 — 진행 중 트랜잭션을 우리 손으로 깨뜨리느니
WAL 복구에 맡긴다.

#### 401 은 사유와 함께 로그로 남는다 (분당 1줄)

```text
인증 실패: 로컬 헤더 없음 (누계 12, 최근 11건 생략) — 벤더 설정이 로컬 수신기와 어긋났다면 `telemetryctl local enable` 로 재병합하세요
```

사유는 `토큰 불일치`·`로컬 헤더 없음` 두 가지이고 둘 다면 ` + ` 로 이어 붙는다. 전자는 낡은
토큰(→ `local disable && local enable`)을, 후자는 벤더 설정이 §7.2 의 두 번째 헤더를 안 적었음을
가리킨다. **HTTP 응답은 여전히 불투명한 `unauthorized` 다** — 청중이 다르다. 원격 호출자에게
사유를 주면 헤더를 하나씩 맞춰 보는 탐색을 돕지만, 이 기계의 주인에게는 그것이 진단의 전부다.

로그에는 제시된 토큰·헤더 값·요청 경로가 담기지 않는다. 틀린 토큰이라도 남으면 오타 하나 차이의
진짜 토큰을 유추할 수 있다(`auth_test.go` 의 `TestTokenNeverReachesLogs`).

첫 건은 무조건 찍고 이후는 분당 1회로 접으며, 접힌 수를 함께 보고한다. 벤더 exporter 는
metrics 60초·logs 5초 주기로 재시도하므로 매 건 찍으면 로그가 그것만으로 채워진다. 반대로
아예 안 찍으면 텔레메트리가 전량 사라지는 동안 아무 흔적도 남지 않는다 — 실제로 그런 사고가
있었고 이 로그가 그 대응이다. 총량은 `telemetryctl status` 의 `수신 카운터 · 인증실패` 에서
접히지 않은 값으로 볼 수 있다.

### 7.2 `telemetryctl local enable | disable`

**배선은 `enroll` 이 자동으로 한다 (기본 ON, opt-out)** — PROJ-45, ADR 0006. `local enable` 은 이미 배선된
설치를 **다시** 배선할 때 쓴다: 데몬이 포트 폴백을 했거나(§7.5), 예전에 `disable` 한 설치를 되돌릴 때다.
탈출구는 `local disable` 이다.

`installer.Apply` 와 `EnableLocal` 은 같은 `localProfile` 을 거쳐 같은 벤더 설정을 만든다. 두 경로가
어긋나지 않는 것은 `TestEnroll배선과enable배선이같은설정을만든다` 가 바이트 단위로 지킨다.

```text
telemetryctl local enable [--port 4318] [--data-dir <경로>] [--state <경로>]
telemetryctl local disable [--data-dir <경로>] [--state <경로>]
```

배선이 하는 일:

1. **고정 로컬 프로필**(`localProfile`)을 만든다. 회사 manifest 의 깊은 사본에서 출발하지만 벤더 설정에
   닿는 필드를 전부 덮는다 — 회사가 수집 범위를 좁혀도 로컬 기능이 죽지 않아야 하기 때문이다.

   | 필드 | 값 |
   |---|---|
   | `otlp.endpoint` | `http://localhost:<port>` |
   | `otlp.protocol` | `http/protobuf` |
   | `otlp.compression` | 없음 — 수신기는 `identity`·`gzip` 만 풀고, loopback 에서 압축이 벌어 주는 것이 없다 |
   | `signals` | 셋 다 `true` |
   | `privacy` | `collect_assistant_responses` 만 `false`, 나머지 `true` |

   회사 값이 살아남는 것은 벤더 설정에 나타나지 않는 필드뿐이다 — `collect_user_email`,
   `repository_allowlist`, `timeout_ms`, 그리고 Codex `environment` 가 파생되는 `resource_attributes`.
   회사가 끈 시그널이 상위로 새 나가지 않는 것은 이제 포워더의 시그널 게이팅이 보장한다 (§4.2 축 1).
2. **회사 telemetry token 을 키링(`credential.AccountTelemetry`)으로 대피시킨다.** enroll 은 enrollment
   응답에서 직접 넣고, `local enable` 은 벤더 파일에서 되읽어 넣는다(`stashTelemetryToken`). 벤더 설정에는
   이제 로컬 ingest 토큰만 남으므로 이것이 회사 토큰의 **유일한 사본**이다.
3. `MergeClaude`/`MergeCodex` 로 벤더 설정을 고정 프로필 + 로컬 ingest 토큰으로 다시 쓴다.
4. `state.Local.Enabled`·`ListenPort` 를 저장한다. 저장에 실패하면 설정을 되돌리고 실패로 끝낸다.

배선하지 못하면 **회사 Collector 직결로 강등하고 알린다.** 조용히 넘어가지 않는다.
ingest 토큰을 얻지 못한 경우(잠긴 키링·헤드리스 CI)와 회사 manifest 가 `grpc` 인 경우다.
후자는 포워더가 grpc 상위 전달을 못 하므로 배선하면 로컬에만 쌓이고 회사에는 아무것도 가지 않는다.

`disable` 은 같은 `(회사 manifest, 회사 token)` 으로 다시 병합한다. `MergeClaude`/`MergeCodex` 가
관리 키를 전부 지우고 다시 쓰는 **권위적 병합**이라 결과는 재배선 전과 같다. 여기서 "같다"는 결정론이지
버전 간 동일성이 아니다 — 관리 키가 늘면 되돌린 파일에 그 키가 새로 나타난다. 반대로 로컬 전용 키
(`CLAUDE_CODE_ENHANCED_TELEMETRY_BETA`, Codex `metrics_exporter`·`trace_exporter`)는 반드시 관리 키
목록에 있어야 한다. 없으면 `disable` 후에도 남아 회사 직결 상태에 로컬 흔적이 섞인다.

`reconnect` 는 배선된 설치의 벤더 설정을 **건드리지 않는다.** 거기 적힌 것은 로컬 ingest 토큰이라
회사 토큰을 쓰면 endpoint 까지 함께 회사 것으로 돌아가 재배선이 조용히 풀린다. 새 회사 토큰은
대피본에만 갱신하고, 그 값이 나중에 `disable` 이 쓸 값이다.

#### 재배선된 설정은 인증 헤더를 **두 개** 적는다

수신기의 인증은 3중이고 bearer 토큰과 `X-Pulsemetry-Local: 1` 을 **AND** 로 묶는다
(`internal/receiver/auth.go`, ADR 0001). 따라서 설정을 쓰는 쪽도 둘 다 적어야 한다.
`Authorization` 만 적으면 Claude Code·Codex 가 보내는 **모든 배치가 401 로 사라진다** —
벤더 exporter 는 조용히 재시도할 뿐이라 사용자에게는 "아무것도 수집되지 않음" 으로만 보인다.

```text
# Claude settings.json — 쉼표로 나눈 "K=V,K=V" 한 줄이다
env.OTEL_EXPORTER_OTLP_HEADERS = "Authorization=Bearer <로컬 ingest 토큰>,X-Pulsemetry-Local=1"

# Codex config.toml — 헤더가 원래 표라 두 항목이다
[otel.exporter.otlp-http.headers]
  Authorization = "Bearer <로컬 ingest 토큰>"
  X-Pulsemetry-Local = "1"
```

**붙일지 말지는 `otlp.endpoint` 에서 파생한다** (`internal/config/localheader.go` 의
`isLocalEndpoint`). `http://localhost:*` 이면 붙이고 아니면 안 붙인다. 별도 플래그를 두지 않는
이유는 진실원을 하나로 유지하기 위해서다 — "endpoint 는 로컬인데 헤더는 없는" 상태가 정확히
이 헤더를 빠뜨렸던 버그이고, endpoint 에서 파생시키면 그 상태를 만들 자리가 없어진다.
규칙이 성립하는 근거는 `contract.validOTLPEndpoint` 가 `http` 를 리터럴 `localhost` 에만
허용한다는 것이다(§7.3). 따라서 `http` + `localhost` ⟺ 로컬 수신기다.

`config` 는 `receiver` 를 import 하지 않으므로 헤더 상수를 복제한다(§3.1 의 의존 방향).
두 값이 어긋나면 `internal/config/localheader_test.go` 의
`TestLocalIngestHeaderMatchesReceiver` 가 잡는다.

토큰에 `,` 나 `=` 가 없어야 `K=V,K=V` 파싱이 모호해지지 않는데, `receiver.NewToken` 이
패딩 없는 base64url 을 쓰므로 그 조건이 성립한다. 되읽기는 `config.bearerFromHeaderList` 가
하고, 그 경로가 `disable` 이 회사 토큰을 되찾는 **유일한 통로**다.

#### 회사 telemetry token 의 키링 대피 (계획서에 없던 항목)

회사 manifest 는 `state.json` 에 있지만 **회사 telemetry token 은 거기 둘 수 없다**(§4.5). 그래서
`enable` 이 벤더 설정 파일에서 그 값을 읽어 `credential.AccountTelemetry` 로 옮기고, `disable` 이
꺼내 되돌린 뒤 대피본을 지운다.

목적은 하나다 — **탈출구가 네트워크에 묶이지 않게 하는 것.** 대피본이 없으면 `disable` 은
`telemetryctl reconnect` 로 서버에서 토큰을 재발급받아야 하고, 그러면 회사 서버가 죽어 있는 동안
사용자가 로컬 재배선에서 빠져나올 수 없다.

안전장치 두 겹:

- 대피는 **아직 꺼져 있을 때만**(`!state.Local.Enabled`) 일어난다. 이미 켜져 있으면 벤더 설정에
  적힌 것은 로컬 ingest 토큰이고, 그것을 대피본으로 덮으면 `disable` 이 로컬 토큰을 회사 설정에
  써 넣는다.
- 읽은 값이 ingest 토큰과 **다를 때만** 대피한다.

토큰은 원래 벤더 설정 파일에 평문으로 있던 값이므로, 키링으로 옮기면 노출 면적은 오히려 좁아진다.
`LocalReport.TelemetryTokenStashed` 가 `false` 면 CLI 가 경고한다.

로컬 ingest 토큰은 `disable` 후에도 **키링에 남긴다.** 데몬은 재배선 여부와 무관하게 그 토큰으로
인증하므로 지우면 돌고 있는 데몬이 자기 토큰을 잃는다. 완전 삭제는 `uninstall` 의 몫이다.

`enable` 은 데몬이 떠 있지 않으면 **경고한다.** 그 상태는 텔레메트리가 로컬에도 회사에도 남지 않는
상태다(9절 첫 줄). PROJ-55 부터 경고는 자동 실행 등록 여부에 따라 다른 조언을 준다 — 등록돼 있는데도
데몬이 없으면 볼 것은 로그이고, 등록되지 않았으면 `autostart enable` 이 답이다(7.7절).

### 7.3 로컬 endpoint 표기 — `localhost` 이지 `127.0.0.1` 이 아니다

`internal/contract/manifest.go` 의 `validOTLPEndpoint` 와 `contracts/enrollment-manifest.schema.json`
은 `http://` 를 **리터럴 호스트 `localhost`** 에만 허용한다. 따라서:

- **벤더 설정에 적히는 주소는 언제나 `http://localhost:<port>`** 다(`installer.LocalEndpoint`).
  `http://127.0.0.1:4318`·`http://[::1]:4318`·`http://localhost.evil.com` 은 전부 거부되고,
  그 거부를 못박는 회귀 테스트가 `internal/contract/manifest_test.go` 에 있다.
- **수신기가 실제로 듣는 주소는 `127.0.0.1` 과 `[::1]` 두 개**다. 두 리스너를 각각 `net.Listen` 으로
  잡아 하나의 `*http.Server` 에 문다. `net.Listen("tcp", "localhost:port")` 는 한쪽만 바인딩해
  절반의 사용자가 조용히 깨진다. 기동 시 전 리스너의 loopback 여부와 포트 일치를 단언한다.
- IPv6 한쪽 실패는 `[::1]:0` 시험 바인딩으로 원인을 가른다. IPv6 loopback 자체가 없는 머신이면
  IPv4 단독으로 충분하고, 그 포트의 `[::1]` 을 남이 점유한 것이면 진행할 때 `::1` 로 푸는
  클라이언트의 텔레메트리가 통째로 남의 프로세스로 간다. errno 로 추측하지 않는 이유는 errno
  상수가 Windows 에서 값이 다르기 때문이다.

> `docs/installation-architecture.md` §4.5 의 「제품화 이후」 절은 로컬 endpoint 예시를
> `http://127.0.0.1:4318` 로 적어 두었다. 그 주소는 위 검증을 통과하지 못한다. 해당 절에 각주를
> 달아 두었으니 그 예시를 그대로 복사하지 말 것.

`validOTLPEndpoint` 와 스키마를 넓히는 것은 서버 저장소와의 공유 계약이라 별도 티켓이다(10절).

### 7.4 `runtime.json` 의 역할

`<data-dir>/runtime.json`. `status` 명령과 GUI 가 "데몬이 지금 어디서 듣고 있는가" 를 알아내는
유일한 수단이다.

```json
{ "schema_version": 1, "pid": 0, "started_at": "", "endpoint": "http://localhost:4318",
  "listen_port": 4318, "listen_addrs": ["127.0.0.1:4318", "[::1]:4318"],
  "data_dir": "", "database_path": "", "version": "" }
```

- **비밀이 들어가지 않는다.** loopback ingest 토큰은 키링에만 있다. 필드 allowlist 테스트가 있어
  누가 `Token` 필드를 더하면 컴파일은 통과해도 테스트가 깨진다.
- `config.AtomicWriteFile`(임시 파일 → fsync → rename)로 쓴다. 독자는 항상 온전한 이전 버전이나
  온전한 새 버전 중 하나만 본다.
- 낡은 파일 판별은 pid 생존으로 한다. **pid 는 재사용되므로 `Stale=true` 만 확정이고
  `Stale=false` 는 "아마 살아 있음" 이다.** 확정이 필요하면 `endpoint` 에 인증 없는
  `GET /healthz` 를 한 번 더 던진다 — `status` 와 `local enable` 이 그렇게 한다.
- `state.Local` 과 역할이 다르다. `state.Local` 은 **설정된 의도**("사용자가 이렇게 돌기를 원했다"),
  `runtime.json` 은 **현실**("실제로 이렇게 돌고 있다")이다. 포트 폴백이 일어나면 둘이 갈리는데,
  재병합이 필요한지 판단하는 근거는 `state.Local` 이다(벤더 설정에 적힌 주소가 거기서 나왔다).
  그래서 데몬은 실제 바인딩 포트를 `state.Local.ListenPort` 에 덮어쓰지 않는다.

### 7.5 포트 폴백 시 재병합 절차

4318 이 사용 중이면(`--listen` 미지정 시) 데몬이 임의 포트로 폴백한다. 이때 벤더 설정은 여전히
옛 포트를 가리키므로 텔레메트리가 아무 데도 도달하지 않는다.

```text
1. 데몬이 경고를 남긴다:
   "요청 포트 4318 를 잡지 못해 <N> 로 폴백했다. ... telemetryctl local enable 을 다시 실행하라"
   실제 포트는 runtime.json 의 listen_port 에 있다.

2. telemetryctl local enable        # --port 없이

3. --port 를 주지 않으면 resolveEnablePort 가 runtime.json 을 읽어
   "데몬이 실제로 듣고 있는 포트" 를 채택하고, 무엇을 왜 골랐는지 출력한다.
   설정값을 강제하고 싶으면 --port 4318 을 명시한다.
```

데몬은 벤더 설정을 절대 직접 고치지 않는다. 재병합은 `local enable` 의 몫이다.

포트를 고정하고 싶으면 `--listen localhost:4318` 을 명시한다. 그러면 폴백 없이 하드 실패한다.

### 7.6 조회·정리 명령

```text
telemetryctl stats    [--since 7d] [--group vendor|model|tool|project|day] [--limit 20] [--json]
telemetryctl sessions [--since 7d] [--status running|completed|abandoned|handoff] [--limit 50] [--json]
telemetryctl purge    --content [--before 2026-07-01] [--yes]
telemetryctl status
```

- 셋 다 `internal/dashboard` 를 쓴다. CLI 와 GUI 가 같은 함수로 같은 숫자를 낸다.
- **DB 없음·데몬 꺼짐에서 세 명령 모두 종료 코드 0 이다.** `status` 는 진단 명령이라 어떤
  상태에서도 동작해야 한다. `--json` 은 `available:false` + 빈 배열(`null` 아님)을 준다.
- `--since` 는 `7d`·`24h`·`90m` 형식이고 상한이 400일(고정 보존 상한)이다.
- `stats` 의 합계 행은 `dim=total` 을 따로 질의해 만든다. 표시된 행의 합으로 계산하면 `--limit`
  으로 잘렸을 때 조용히 틀린 숫자가 된다.
- 사람용 출력과 `--json` 이 한 구조체에서 나오고, 표 셀은 그 구조체 필드에서만 만든다 — 표는 JSON
  의 부분집합이다. JSON 시각은 UTC unix 초로 통일하되 `timezone`·`utc_offset_seconds` 를 함께 실어
  기계가 로컬 시각을 복원할 수 있게 한다.
- `purge --content` 는 지우기 전에 대상 행 수와 되돌릴 수 없음을 알린다. 구간 제한 없는 전체 삭제만
  확인을 요구하고, 비대화 실행에서는 프롬프트로 멈추지 않고 `--yes`·`--before` 를 안내하며 거부한다.
  DB 파일이 없으면 `store.Open` 을 부르지 않는다 — 부르면 빈 DB 를 만들어 다음 `status` 가
  "설정 안 됨" 대신 "0건" 을 보고한다.
- `status` 의 로컬 블록은 `printCredentialStatus` 와 같은 규칙으로 **존재 여부만** 출력한다. ingest
  토큰 값은 절대 찍지 않는다.

### 7.7 `telemetryctl autostart enable | disable | status` (PROJ-55)

로그인 시 데몬을 자동으로 띄운다. **`enroll` 이 배선 직후 best-effort 로 이 등록을 수행하므로**
보통은 직접 칠 일이 없다 — 등록에 실패한 환경에서 되살리거나, 끄거나, 상태를 볼 때 쓴다.

```text
telemetryctl autostart enable  [--exec-path <절대 경로>] [--force] [--data-dir <경로>] [--state <경로>]
telemetryctl autostart disable [--data-dir <경로>] [--state <경로>]
telemetryctl autostart status  [--data-dir <경로>] [--state <경로>]
```

| 플랫폼 | 메커니즘 | 산출물 |
|---|---|---|
| macOS | LaunchAgent (`launchctl bootstrap gui/<uid>`) | `~/Library/LaunchAgents/com.your-org.pulsemetry.daemon.plist` |
| 리눅스 | systemd user unit (`systemctl --user enable --now`) | `$XDG_CONFIG_HOME/systemd/user/pulsemetry-daemon.service` (기본 `~/.config/…`) |
| Windows | 없음 — `ErrUnsupportedPlatform` | PROJ-56 |

**둘 다 사용자 수준이다.** LaunchDaemon·시스템 유닛은 root 로 **로그인 전에** 돌아 사용자 로그인
키체인을 읽지 못하고, 그러면 `receiver.EnsureToken()` 이 실패해 데몬 전체가 뜨지 못한다.
`loginctl enable-linger` 도 켜지 않는다 — 켜지 않으면 "로그인 시 시작·로그아웃 시 종료" 라는
launchd LaunchAgent 와 **정확히 같은 의미론**이 되어 두 플랫폼이 대칭이 된다. 자세한 근거는
`internal/autostart` 패키지 주석에 있다.

**서비스 명령은 `daemon`으로 시작한다.** `--state`·`--data-dir`은 명시한 값이 기본값과 다를 때만
뒤에 붙고, `--listen`은 서비스에 굽지 않는다. 기본 경로는 서비스 관리자 아래에서 HOME 을 기준으로
`DefaultStatePath` → `state.Local` → 기본값 순서로 풀린다. `--listen` 생략은 단지 허용 가능한 게
아니라 **바람직하다** — `fixed=false`
라 부팅 시 일시적 포트 충돌이 하드 실패(→ 재시작 루프)가 아니라 우아한 폴백이 된다(7.5절).
기본 경로까지 굽지 않는 이유는 `state.json` 위치가 두 곳이 되고 `installer.EnableLocal` 이
`state.Local.DataDir` 를 바꾸는 순간 조용히 어긋나는 일을 막기 위해서다.

**재시작 정책은 ADR 0007 이다** — launchd `KeepAlive={SuccessfulExit:false}`, systemd
`Restart=on-failure`. SIGTERM 은 `main.go` 의 `signal.NotifyContext` 가 잡아 종료 코드 0 이 되므로
`systemctl --user stop`·`launchctl bootout`·Ctrl-C 는 전부 **정지 상태를 유지한다.**
systemd `TimeoutStopSec=20` 은 `daemon.DefaultShutdownTimeout`(15초)보다 커야 하고, 그 불변식은
`internal/daemon/daemon_test.go` 가 지킨다(반대 방향 import 는 SQLite·protobuf 를 CLI 의 status
경로까지 끌고 들어온다).

**`go run` 으로는 등록할 수 없다.** `os.Executable()` 이 임시 디렉터리를 가리키면
`ErrExecPathVolatile` 로 **거부한다** — 사라질 경로를 재시작 정책과 함께 등록하면 영구 크래시
루프가 되고 그것은 거부보다 훨씬 나쁘다. 탈출구는 `--exec-path <설치된 절대 경로>` 이고 그것이
곧 패키저·CI 의 통로다.

**이미 도는 데몬이 있으면 `enable` 이 종료 코드 2 로 거부한다.** 단순 포트 충돌보다 나쁘기
때문이다: 두 번째 데몬은 임의 포트로 조용히 폴백하고, 두 데몬이 같은 `runtime.json` 을 쓰며,
`local enable` 이 그 임의 포트로 벤더 설정을 재배선하고, 한 SQLite 파일에 writer 가 둘이 된다.
`--force` 로 넘어갈 수 있지만 사용자의 foreground 프로세스를 자동으로 죽이지는 않는다.

**등록 후 최대 5초 동안 데몬 생존을 폴링한다.** 두 메커니즘 모두 등록과 동시에 데몬을 띄우지만
바인딩과 `runtime.json` 기록에 수백 ms 가 걸려서, 기다리지 않으면 **완전히 성공한 등록에서도**
곧바로 "데몬이 실행 중이 아닙니다" 를 찍게 된다. 반대로 키체인 프롬프트·Secret Service 실패 같은
진짜 실패는 이 폴링이 사용자가 조치할 수 있는 시점에 잡아 준다.

**등록 상태를 `state.json` 에 저장하지 않는다.** plist·unit 파일 자체가 산출물이고 OS 서비스
관리자가 권위 있는 소스다. 저장하면 사용자가 `systemctl --user disable` 하거나 macOS 로그인
항목에서 껐을 때 `state.json` 만 거짓말을 한다. 현재 `StateSchemaVersion` 은 5이며, schema 5는
자동 실행 등록과 무관하게 PROJ-71의 `local.retention_days` 제거 마이그레이션에 사용한다(ADR 0008).

로그는 플랫폼마다 다르다. systemd 는 journald 가 로테이션까지 맡으므로 할 일이 없다
(`journalctl --user -u pulsemetry-daemon.service -n 100`). launchd 는 로테이션하지 않으므로
`~/Library/Logs/pulsemetry/daemon.log`·`daemon.err.log` 를 데몬의 기존 prune 틱이 16 MiB 상한으로
회전시킨다(`autostart.RotateLogs`). **rename 이 아니라 copy-truncate 다** — launchd 가 fd 를 잡고
있어 rename 하면 데몬이 계속 옛 inode 에 쓰고 새 파일은 영원히 빈다. `daemon.err.log` 를 따로
두는 이유는 데몬 로거가 stdout 만 쓰기 때문이다 — 그쪽은 **순수한 크래시 진단 파일**이 된다.

---

## 8. 엔드투엔드 수동 검증

계획서 「검증」을 실제 명령으로 고친 것이다. 4.3절의 정정이 5번에 반영돼 있다.

```sh
# 0. 자동 검증
go build ./... && go vet ./... && go test -race -cover ./...

# 1. 상위 Collector 대역 — 받은 본문을 덤프하는 간이 서버를 띄운다.
#    (state.json 의 manifest.otlp.endpoint 가 그곳을 가리키게 하거나, --no-forward 로 이 단계를 건너뛴다)

# 2. 데몬 기동 (별도 터미널). enroll 이 선행돼야 한다.
go run ./cmd/telemetryctl daemon --listen localhost:4318 --data-dir /tmp/pm-test

# 3. loopback ingest 토큰을 키링에서 꺼낸다 (macOS. 키링 서비스명은 "pulsemetry")
#    go-keyring 은 값을 "go-keyring-base64:<base64>" 로 감싸 저장한다. 래퍼를 벗기지 않으면
#    토큰이 아니라 래퍼 문자열을 보내게 되어 401 이 나오고, 마치 재배선이 깨진 것처럼 보인다.
INGEST_TOKEN="$(security find-generic-password -s pulsemetry -a local-ingest -w \
  | sed 's/^go-keyring-base64://' | base64 -d)"

# 4. 픽스처를 수신기로 직접 전송
curl -sS -X POST http://localhost:4318/v1/logs \
  -H 'Content-Type: application/json' \
  -H 'X-Pulsemetry-Local: 1' \
  -H "Authorization: Bearer $INGEST_TOKEN" \
  --data @internal/otlpdecode/testdata/logs_session_walkthrough.json

curl -sS -X POST http://localhost:4318/v1/metrics \
  -H 'Content-Type: application/json' \
  -H 'X-Pulsemetry-Local: 1' \
  -H "Authorization: Bearer $INGEST_TOKEN" \
  --data @internal/otlpdecode/testdata/metrics_lines_of_code.json

# 인증 실패가 401 인지도 확인한다. 세 가지를 각각 본다 — 데몬 로그에 사유가 다르게 찍혀야 한다.
#   토큰 없이            → 401 "토큰 불일치 + 로컬 헤더 없음"
#   토큰만, 헤더 없이    → 401 "로컬 헤더 없음"
#   헤더만, 틀린 토큰    → 401 "토큰 불일치"
# 연속으로 보내면 로그는 분당 한 줄로 접히고 접힌 건수를 함께 보고한다 (§7.1).

# 5. 세션이 조립됐는지 — 화면 요소별로. flush 는 2초, 세션 스냅샷은 30초 주기다.
DB=/tmp/pm-test/pulsemetry.db
sqlite3 "$DB" "SELECT session_id, title, title_source, status, tool_calls, cost_usd FROM sessions;"
sqlite3 "$DB" "SELECT file_name, lines_added, lines_removed FROM session_files;"
sqlite3 "$DB" "SELECT ts, tool_name, target_name, success FROM tool_events ORDER BY ts;"
sqlite3 "$DB" "SELECT vendor, last_seen FROM vendors;"
sqlite3 "$DB" "SELECT hour, dim, key, cost_usd, prompts, tool_calls FROM rollup_hourly ORDER BY hour;"

# 6. 프라이버시 회귀 — 4.3절의 (1)~(4)를 그대로 실행한다.
#    events 는 0, event_content 의 tool_input 은 0 이 아니어야 한다.

# 7. 상위로 전달된 본문에 원문·tool details 가 없는지 1번 덤프에서 확인한다.
#    동시에 남아야 할 속성(model·session.id 등)이 살아 있는지도 함께 본다.

# 8. 조회
go run ./cmd/telemetryctl sessions --since 1d
go run ./cmd/telemetryctl stats --since 1d --group vendor
go run ./cmd/telemetryctl status
```

**실제 Claude Code 연동**: `telemetryctl local enable` 후 한 세션 작업하고, `sessions` 에 제목·파일
변경·툴 타임라인이 잡히는지, 회사 Collector 대역에는 원문 없이 도착하는지 확인한다. `local disable`
후 두 벤더 설정이 재배선 전과 바이트 단위로 같은지도 확인한다.

**GUI 연동**: `cd gui && wails3 generate bindings` 후 JS 에서 `DashboardService.Today("Asia/Seoul")`
과 `Session(id)` 가 정상 반환하는지 확인한다(6.5절의 모듈 경로 제약을 먼저 만족시켜야 한다).

### 8.1 자동 실행 등록 체크리스트 (PROJ-55)

**CI 에 넣지 않는다.** 두 러너 모두 구조적으로 적대적이다 — macOS 러너의 UID 에는 GUI 로그인
세션이 없어 `bootstrap gui/$UID` 가 `Bootstrap failed: 5` 로 실패하고, ubuntu 러너의 사용자에게는
systemd user manager 도 `XDG_RUNTIME_DIR` 도 없다. 그래서 아래는 사람이 실제 장비에서 한다.

```sh
go build -o dist/telemetryctl ./cmd/telemetryctl   # go run 으로는 등록할 수 없다 (7.7절)

# 0. 자동화된 부분 (환경 변수 게이트)
PULSEMETRY_E2E_AUTOSTART=1 PULSEMETRY_E2E_EXEC="$PWD/dist/telemetryctl" \
  go test -race -run TestE2E ./internal/autostart/
```

1. `./dist/telemetryctl autostart enable` → `등록됨` + `데몬: 실행 중 (헬스체크 응답 확인)`
2. `./dist/telemetryctl status` → 자동 실행 블록이 데몬 줄 바로 뒤에 나오는지
3. `launchctl print gui/$UID/com.your-org.pulsemetry.daemon`
   / `systemctl --user status pulsemetry-daemon.service`
4. **로그아웃 후 다시 로그인**(또는 재부팅) → 데몬이 스스로 돌아왔는지 (`telemetryctl status`)
5. `kill -9 $(pgrep -f 'telemetryctl daemon')` → **재시작되어야 한다** (비정상 종료, ADR 0007)
6. `launchctl bootout gui/$UID/com.your-org.pulsemetry.daemon`
   / `systemctl --user stop pulsemetry-daemon.service`
   → **정지 상태를 유지해야 한다.** 되살아나면 ADR 0007 의 회귀다
7. `./dist/telemetryctl autostart disable` → plist/unit 이 사라지고 `status` 가 `등록 안 됨`
8. macOS 만: 시스템 설정 → 일반 → 로그인 항목에 항목이 보이는지 (거기서 끄면 등록이 남아 있어도
   실행되지 않는다 — 우리가 읽을 수 없는 상태다, 9절 한계 표)
9. 등록물이 진짜 홈에 남지 않았는지 (단위 테스트가 새지 않았음을 확인하는 용도)

```sh
ls ~/Library/LaunchAgents | grep -i pulsemetry || echo "OK: 등록물 없음"
ls ~/.config/systemd/user 2>/dev/null | grep -i pulsemetry || echo "OK: 등록물 없음"
```

---

## 9. 알려진 한계

**데몬이 실행 중이 아닌데 배선돼 있으면 텔레메트리가 로컬에도 회사에도 남지 않는다.**
가장 큰 한계였고, PROJ-45 가 배선을 opt-out 으로 바꾸면서 이 상태를 지나가는 사람이 enroll 한
전원으로 늘었다.

**PROJ-55 가 이 한계를 좁혔다.** `enroll` 이 배선 직후 자동 실행을 best-effort 로 등록하고
(macOS LaunchAgent · 리눅스 systemd user unit, 7.7절), 등록 후 데몬 생존까지 확인한다. 남은 노출은
셋이다 — **등록할 수 없는 환경**(Windows·systemd 없는 리눅스·`go run`), **재시작으로 낫지 않는
영구 실패**(미enroll·잠긴 키링·바이너리 이동, ADR 0007 Negative), 그리고 **로그아웃 중**(사용자
수준 서비스라 로그아웃하면 함께 종료된다). 세 경우 모두 `enroll`·`local enable`·`status` 가
서로 다른 조언과 함께 알린다.

| 한계 | 내용·완화 |
|---|---|
| **파일별 라인 배분이 근사** | `claude_code.lines_of_code.count` 메트릭에는 파일명이 없다. `tool_result` 의 `tool_input` 에서 파일을 얻고 같은 시각의 증분을 귀속시키므로, 한 응답에서 여러 파일을 고치면 배분이 근사가 된다. **세션 합계(`sessions.lines_added`)는 메트릭에서 직접 받아 정확하고 파일별 배분만 근사다.** PROJ-35 는 `session_files` 의 수치에 툴팁으로 이 사실을 표기해야 한다. 코드 형태로 보장된 것은 `Σ배분 ≤ total` 하나다(`fileState` 에 라인 필드가 아예 없고 배분이 `unassigned → assigned` 이동이다) |
| **제목 품질** | `prompt_head`(첫 프롬프트 첫 문장 60자) → `files` → `fallback` 3단계 휴리스틱이다. 화면 예시(`인증 토큰 검증 및 Collector 전달 프록시 구현`) 수준은 나오지 않는다. `title_source` 컬럼이 출처를 남기므로 후속 교체가 스키마 변경 없이 가능하고, `SessionRow.TitleSource` 로 화면이 출처를 표시할 수 있다 |
| **`abandoned` 오판 가능** | "마지막 툴 이벤트가 실패이고 이후 성공 없음" 이라는 휴리스틱이다. **화면 필터로만 쓰고 지표로 쓰지 않는다.** 판정 근거는 세션 마감 로그(`s.Diag.StatusReason`)에 남는다 |
| **데몬 미실행 중 유실** | 위 첫 문단. PROJ-55 의 자동 실행 등록이 대부분을 막지만, 등록할 수 없는 환경과 영구 실패는 남는다 |
| **Windows 는 자동 실행 등록이 없다** | `autostart` 명령이 `ErrUnsupportedPlatform` 으로 알리고 `telemetryctl daemon` 직접 실행을 안내한다. 작업 스케줄러 등록은 PROJ-56 이다. **경고가 아니라 정보로 출력한다** — 실패한 것이 없기 때문이다 |
| **로그아웃하면 데몬도 종료된다** | 사용자 수준 서비스(LaunchAgent / `systemctl --user`)를 쓰고 `loginctl enable-linger` 를 켜지 않기 때문이다. 두 플랫폼이 같은 의미론을 갖게 하려는 의도적 선택이고, linger 는 "로그인한 사용자 없이 수집" 이라는 **프라이버시 의미론 변경**인 데다 세션이 없으면 Secret Service 도 없어 `EnsureToken` 이 실패한다. 필요한 사용자는 `loginctl enable-linger $USER` 를 직접 실행한다 |
| **macOS 로그인 항목 토글을 읽을 수 없다** | macOS 13+ 는 시스템 설정 → 일반 → 로그인 항목에서 사용자가 이 항목을 끌 수 있는데, 그 상태는 `SMAppService`(Objective-C → cgo → ADR 0002 위반) 없이는 조회할 수 없다. `autostart status` 는 `등록됨` 으로 보이지만 실제로는 실행되지 않는 상태가 가능하다. 완화는 `enable` 출력의 안내 한 줄과, 데몬 생존을 `runtime.json` + `/healthz` 로 따로 확인하는 것이다 |
| **재enroll 없는 업그레이드는 경로 드리프트가 남는다** | 유닛 파일에는 `os.Executable()` 결과를 **해석하지 않고** 적는다. macOS 에서는 Homebrew 심볼릭 링크가 보존돼 업그레이드를 견디지만, **리눅스는 `os.Executable()` 이 `/proc/self/exe` 라 이미 완전히 해석돼 있어** 심볼릭 링크를 보존할 수 없다. 바이너리만 갈고 재enroll 하지 않으면 등록된 경로가 낡은 채로 남는다. `autostart status` 의 `ExecPathDrift`·`ExecPathMissing` 이 보고하지만 **자동 복구하지 않는다** — 고치는 방법은 `autostart enable` 재실행이다 |
| **launchd 로그는 우리가 회전시킨다** | launchd 는 로테이션하지 않는다. 데몬의 prune 틱(1시간)이 16 MiB 상한으로 copy-truncate 한다(`.1` 하나만 보관). 복사와 truncate 사이에 쓰인 몇 줄은 잃을 수 있고, 크래시 진단 로그에 대해 그것은 받아들일 만하다 |
| **회사가 끈 시그널은 로컬에만 쌓인다** | 로컬은 시그널 셋을 전부 켜고 받지만 포워더가 상위 전달을 막는다(§4.2 축 1). 즉 회사 `signals.traces=false` 면 트레이스는 로컬 파이프라인을 통과하되 회사에는 가지 않는다 — 설계된 동작이고 `Stats.DroppedSignalDisabled` 로 보인다. 다만 `/v1/traces` 는 저장도 하지 않으므로(아래 행) 그 시그널은 실질적으로 버려진다 |
| **`grpc` 테넌트는 배선되지 않는다** | 포워더가 grpc 상위 전달을 못 하므로 `Apply` 가 회사 직결로 강등하고 알린다. `local enable` 도 `ErrGRPCUnsupported` 로 거부한다. 기존 회사 Collector 직결은 그대로 동작한다 |
| **기존 설치자는 자동 전환되지 않는다** | ADR 0006에서 로컬 재배선 마이그레이션을 넣지 않았다. state schema 5 마이그레이션은 보존 설정만 제거하므로, 바이너리만 교체한 사용자는 여전히 `local enable`을 명시적으로 실행해야 한다 |
| **크래시 손실 창** | flush 주기(2초, 또는 512 이벤트)만큼의 미저장 이벤트를 잃는다. 세션 스냅샷은 30초 주기지만 세션 수치는 마감 전에는 어차피 확정값이 아니다 |
| **조립기 TTL 이후 같은 `session.id` 재등장** | 마감된 세션은 2시간(`sessionMemoryTTL`) 뒤 조립기 메모리에서 지워진다. 그 뒤 같은 `session.id` 가 다시 등장하면 조립기가 **새 세션으로 시작**하고 `sessions` UPSERT 가 기존 행을 덮는다 — 앞 구간의 수치를 잃는다. TTL 이 유휴 임계값(10분)의 12배인 이유이자, 보존 기간(400일)이 아닌 몇 시간짜리 값을 쓰는 이유(`store.Prune` 이 지운 타임라인을 다음 스냅샷이 되살리지 못하게)다 |
| **Windows + WSL 이중 설정** | 두 환경이 각각 설정을 갖고 같은 이벤트를 두 번 보낼 수 있다. `dedup_key` UNIQUE 와 배선 창이 잡지만 두 환경의 `installation_id` 가 다르면 다른 이벤트로 취급된다 |
| **경로 정규화의 한계** | `NormalizePath` 는 구분자 통일 → `path.Clean` → 드라이브 문자 대문자화까지만 한다. `~/a.go` 와 `/Users/jy/a.go` 는 다른 해시가 되고, POSIX 파일명에 들어간 리터럴 백슬래시는 구분자로 취급된다. 홈 확장·심볼릭 링크 해석은 파일시스템을 읽어야 하므로 하지 않는다(순수 함수 유지) |
| **cumulative 콜드 스타트 과소 집계** | 계열의 첫 관측은 기준선으로만 기록하고 값을 더하지 않는다. "조용히 2배 집계되느니 폐기" 와 같은 방향이며 `rollup.Stats.Baselines` 로 보인다 |
| **`UNSPECIFIED` temporality 폐기** | `Sum.aggregation_temporality` 가 `UNSPECIFIED` 면 폐기하고 카운트만 올린다(`Stats.DroppedTemporality`, `status` 에 노출). `Sum` 이 아닌 메트릭 타입도 폐기한다 — `events.value` 한 칸에 Gauge 의 last-value 의미를 담을 자리가 없다 |
| **Codex 시그널 매핑 없음** | `rollup/mapping.go` 의 표에 Claude Code 시그널만 있다. Codex 이름을 추측으로 넣지 않았다(틀린 컬럼에 조용히 쌓인다). 표에 없는 이름은 `Unmapped` 로 세고 집계하지 않는다 |
| **트레이스는 저장하지 않는다** | `/v1/traces` 는 받아서 상위로 전달만 한다. `events` 스키마에 스팬을 담을 자리가 없다 |
| **시간대 근사** | 6.4절 — UTC+5:30 같은 오프셋에서 하루 경계에 최대 한 시간 버킷의 근사가 남는다 |
| **원문 평문 저장** | `event_content` 는 400일간 평문으로 남고 디스크 암호화에 의존한다. 완화는 16KB 캡 + `--no-store-content` + `purge --content` + `status` 사용량 표시다 |
| **로컬 ingest 토큰이 `settings.json` 에 평문** | loopback 전용이고 권한은 "이 PC 에 텔레메트리 쓰기" 뿐이다. 회사 토큰이 같은 자리에 있던 것보다 낫다 |
| **키링 불가 환경(WSL·헤드리스)** | `enroll` 이 이미 키링에 의존하므로 기존 제약이다. 데몬은 `Options.IngestToken` 으로 우회할 수 있으나 CLI 플래그는 없다 |
| **Windows 에서 GUI 가 파일을 연 채 prune** | prune 실패는 로깅 후 다음 틱 재시도다. 치명적으로 다루지 않는다 |
| **`manifest` 가 `grpc`** | `local enable` 과 데몬 기동이 모두 명확한 에러로 거부한다. 기존 회사 Collector 직결은 그대로 동작한다 |

---

## 10. 범위 밖 · 후속 티켓

1. **SQLite v3 런타임 전환** — `vendors`, `sessions`, `turns`, `events`, `llm_calls`, `tool_calls`,
   `file_changes`에 맞춰 디코드 결과의 ETL, 쓰기, 조회, 보존과 GUI 계약을 교체하고 전체 테스트를
   복구한다
2. **데몬 자동 실행 등록 — Windows** (PROJ-56, 작업 스케줄러). macOS·리눅스는 PROJ-55 에서 끝났다
   (7.7절, ADR 0007). Settings 「시작 프로그램」 토글은 `autostart.Manager` 를 감싸면 되고,
   등록 상태를 `state.json` 에 두지 않으므로 토글의 진실원은 OS 서비스 관리자 하나다
3. **Insights 경고 카드·제안** — 반복 실패 감지, 유사 프롬프트 탐지, 벤더 전환 분석
4. **제목·요약 품질 개선** (`title_source='llm'`) — 프롬프트를 외부로 보내는 문제라 **별도 프라이버시
   검토가 선행**되어야 한다. ADR 0003 은 원문이 로컬을 떠나지 않는다는 전제 위에 서 있고, 그 전제를
   깨는 결정은 새 ADR 을 요구한다
5. **Gemini CLI · Cursor 연동** — OTLP 지원 여부 조사부터. 스키마의 `vendor` 는 제약이 없어 받을 수는
   있으나 자동 설정과 시그널 매핑이 없다
6. **Settings 의 Cloud 탭** — 회사 서버 조회. 로컬 DB 가 아니라 회사 API 를 읽으므로 별도 경로다
7. **`contracts/enrollment-manifest.schema.json` 에 `127.0.0.1` 허용** — 서버 저장소와 협의 필요.
   지금은 `localhost` 표기로 우회한다(7.3절)
8. **gRPC 상위 전달** — 현재는 `forward.ErrGRPCUnsupported` 로 거부한다
9. **`resource_attributes` → `OTEL_RESOURCE_ATTRIBUTES` 배선** (회사 단위 태깅)
10. **`gui/` CI 스텝** — 6.7절
11. **`wails3 generate bindings` 생성물 최신성 CI 검증** — GUI 티켓에서 정한다
12. **툴 출력 본문 파싱** — 「테스트 실행 (2 실패)」의 실패 건수 같은 것. 지금은 성공/실패 여부만
    저장한다
13. **systemd `Type=notify`** (`sd_notify`) — `enable --now` 가 반환될 때 데몬이 이미 수신 가능함을
    systemd 가 알게 한다. 지금은 CLI 가 `/healthz` 폴링으로 대신한다 (ADR 0007 Follow-up)
