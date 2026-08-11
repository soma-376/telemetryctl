# 0004. GUI 연동을 Go 패키지 공유로 하고 HTTP 조회 API 를 두지 않는다.

## Status
Accepted

## Context
PROJ-36 은 로컬 SQLite(ADR 0002)에 세션·롤업·원문을 쌓는다. 이 데이터를 그리는 쪽은 Wails v3 데스크탑 앱이다.
GUI 는 같은 저장소 안 `gui/` 디렉터리에 **별도 `go.mod`** 로 둔다 — Wails 의존성이 CLI 바이너리로 새어들지 않게 하기 위해서다.

데몬과 GUI 는 별도 프로세스다. 데몬이 DB 를 소유하고 쓰며, GUI 는 읽기만 한다.
따라서 "GUI 가 데이터를 어떻게 얻는가"를 정해야 하고, 선택지는 프로세스 간 프로토콜을 세우는 쪽과
같은 언어의 함수 호출로 끝내는 쪽으로 갈린다.

제약이 두 가지 더 있다.

- ADR 0001 이 이미 로컬에 인증 붙은 HTTP 표면을 하나 만들었다. 두 번째 표면을 늘리는 결정은 그만큼의 근거가 필요하다.
- Wails v3 서비스의 `ServiceStartup` 이 error 를 반환하면 **앱 기동 자체가 중단된다.**
  아직 `enroll` 하지 않았거나 데몬을 한 번도 켜지 않은 사용자에게는 DB 파일이 없다.

## Decision
- `internal/dashboard` 를 **화면별 조회 API 를 제공하는 순수 Go 패키지**로 두고, 여기서 Wails 를 import 하지 않는다.
  DB 연결·SQL·시간대 처리·어제 대비 계산이 전부 이 패키지 안에서 끝난다.

  ```go
  func Open(dbPath string) (*Reader, error)   // mode=ro&_pragma=busy_timeout(5000)
  func (r *Reader) Today(ctx context.Context, tz string) (TodaySummary, error)
  func (r *Reader) Sessions(ctx context.Context, q SessionQuery) ([]SessionRow, error)
  func (r *Reader) Session(ctx context.Context, id string) (SessionDetail, error)
  func (r *Reader) Search(ctx context.Context, q SearchQuery) ([]Hit, error)
  ```

- **Wails v3 서비스가 이 패키지를 직접 import 해 감싼다.** Wails 는 `gui/` 쪽에만 존재한다.
  `application.NewService(&DashboardService{})` 로 등록하고 `wails3 generate bindings` 로 TS 바인딩을 생성한다.
  Go 의 `error` 는 Promise reject 로 전파되고, 구조체에 `json` 태그를 붙여 TS 필드명을 고정한다.
- **로컬 HTTP 조회 API 를 만들지 않는다.** 로컬 네트워크 표면은 ADR 0001 의 수신기 하나로 유지한다.
- **GUI 는 SQLite 를 직접 열지 않는다.** 스키마 지식은 `internal/dashboard` 밖으로 나가지 않는다.
  연결은 항상 read-only 로 열어 화면이 데이터를 바꿀 수 없게 한다.
- **미설치 상태는 error 가 아니라 빈 결과다.** DB 파일이 없거나 테이블이 비어 있으면
  `ServiceStartup` 은 성공하고 각 조회는 빈 값과 "아직 데이터 없음" 상태를 돌려준다.
  `Status()` 가 데몬 실행 여부·마지막 이벤트 시각을 알려 화면이 안내 문구를 띄운다.
- CLI 의 `stats` · `sessions` 명령도 같은 `internal/dashboard` 를 쓴다. 조회 로직을 두 번 쓰지 않는다.

## Alternatives
### A. 데몬이 로컬 HTTP 조회 API 를 열고 GUI 가 호출한다
- 장점: GUI 와 데몬이 언어에 묶이지 않는다. 나중에 웹 UI 나 Electron 으로 갈아타도 그대로 쓸 수 있고, 브라우저에서 `curl` 로 디버깅할 수 있으며, GUI 가 DB 파일 경로를 알 필요가 없다.
- 단점: 인증 붙은 두 번째 로컬 네트워크 표면이 생긴다. 토큰 발급·CORS·바인딩 단언을 한 벌 더 만들고 한 벌 더 테스트해야 한다. 조회할 때마다 데몬이 살아 있어야 해서, 데몬이 꺼져 있으면 과거 데이터조차 못 본다. 타입도 두 벌(Go 구조체와 JSON 계약)이 되어 화면 필드가 늘 때마다 양쪽을 고쳐야 한다.
- 탈락 이유: 다른 언어의 클라이언트가 없다. 유일한 소비자가 같은 저장소의 Go 코드인데 프로세스 간 프로토콜과 그 보안 표면을 유지하는 비용을 지불할 이유가 없다. "데몬이 꺼져도 이력은 보인다"는 성질도 잃는다.

### B. GUI 가 SQLite 파일을 직접 열고 SQL 을 쓴다
- 장점: 중간 계층이 아예 없어 가장 단순하다. 화면이 필요한 질의를 스스로 만들 수 있어 새 화면을 붙일 때 CLI 저장소를 건드리지 않아도 된다.
- 단점: 스키마 지식이 두 모듈로 퍼진다. 컬럼 하나를 바꾸면 어디가 깨지는지 컴파일러가 알려주지 못하고, 조회 로직이 CLI 의 `stats`·`sessions` 와 갈라져 같은 지표가 화면과 CLI 에서 다른 값을 낼 수 있다. read-only 강제와 `busy_timeout` 설정을 GUI 개발자가 매번 기억해야 한다.
- 탈락 이유: 스키마가 넓고(ADR 0002) 앞으로 `phase_json`·`work_type` 이 채워지며 계속 움직인다. 움직이는 스키마를 두 곳에서 아는 구조는 조용히 어긋난다.

## Consequences/Tradeoffs
### Positive
- 화면 계약이 Go 타입이다. 스키마가 바뀌면 GUI 빌드가 깨져서 알려준다. 문서 동기화에 의존하지 않는다.
- 로컬 네트워크 표면이 수신기 하나로 유지된다. 보안 검토 대상이 늘지 않는다.
- 데몬이 꺼져 있어도 GUI 가 과거 데이터를 그대로 보여준다. 조회 경로가 데몬 프로세스에 의존하지 않는다.
- CLI 와 GUI 가 같은 함수로 같은 숫자를 낸다.
- `internal/dashboard` 에 Wails 의존이 없으므로 표준 `go test` 로 화면 쿼리를 테스트할 수 있다.

### Negative
- GUI 가 Go 로 고정된다. 다른 스택으로 옮기려면 조회 계층을 새로 만들어야 한다.
  - 지금 그럴 계획이 없고, 필요해지면 `internal/dashboard` 를 감싸는 얇은 HTTP 어댑터를 추가하면 된다. 되돌리기 어려운 결정이 아니다.
- `gui/` 가 별도 모듈이므로 `internal/` import 규칙을 만족시켜야 한다.
  gui 모듈 경로를 상위 모듈 경로 아래(`.../gui`)에 두고 로컬 `replace` 로 상위 모듈을 가리켜야 한다. 이 배치를 벗어나면 `internal/dashboard` import 가 거부된다.
- 모듈이 둘이라 빌드·CI 단계가 둘이다. `go build ./...` 와 `(cd gui && go build ./...)` 를 모두 돌려야 한다.
- 화면에 새 지표가 필요하면 `internal/dashboard` 에 메서드를 추가하고 바인딩을 재생성해야 한다. GUI 쪽만 고쳐서 끝낼 수 없다.
- 데몬 쓰기와 GUI 읽기가 동시에 일어나므로 read-only 연결 조회를 `-race` 로 검증해야 한다.

## Follow-up
- **데몬 자동 실행** — GUI 를 켰는데 데몬이 꺼져 있는 상태를 근본적으로 줄인다. Settings 「시작 프로그램」 토글의 구현이기도 하다.
- **Settings 의 Cloud 탭**(회사 서버 조회)은 이 ADR 의 범위 밖이다. 로컬 DB 가 아니라 회사 API 를 읽으므로 별도 경로가 필요하다.
- `wails3 generate bindings` 를 CI 에 넣어 생성물이 최신인지 검증할지는 GUI 티켓에서 정한다.
