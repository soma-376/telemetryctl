# Activity 실데이터 배선

GUI는 SQLite를 직접 열지 않는다(ADR 0013). Wails `Dashboard.Activity`와
`Dashboard.ActivitySession`이 인증된 데몬 로컬 API를 호출한다.

- `GET /v1/activity?q=<ActivityQuery JSON>`: 시작일 범위·벤더·프로젝트 전체 경로·상태·검색어로
  세션을 조회한다. 시작 시각 포함, 종료 시각 배타이며 프런트는 로컬 날짜의 다음 날 자정을
  종료 경계로 변환한다. 페이지 크기는 50이고 다음 페이지는 서버의 커서를 사용한다.
- `GET /v1/activity/{id}`: 세션 상세·지표·분류를 묶는다. ID는 SQLite의 세션 ID다.
  도구 호출도 자체 ID와 턴 ID를 전달해 같은 시각·이름의 호출이 겹쳐도 구분한다.
- 프롬프트는 턴별 4,000자까지 표시하며 잘림 여부를 전달한다. 턴 200개·도구 1,000개 상한을
  넘으면 화면에 일부 표시임을 알린다. 상단 지표는 전체 세션 합계다.

TanStack Query가 캐시와 로딩·실패 상태를 소유한다(ADR 0015). 메인 창이 보이고 Activity가
열린 동안 30초마다 조회한다. 네이티브 `main:shown`·`main:hidden` 이벤트로 폴링을 제어한다.
상세 선택은 목록 인덱스 대신 세션 ID를 유지하며 다른 세션의 응답을 임시 표시하지 않는다.

토큰은 저장된 입력+출력 합계를 k/M으로 축약한다. 추론·캐시 토큰을 총량에 중복 가산하지 않는다.
목록 비용은 벤더 보고값, 상세 비용은 기존 pricing 계산 결과다. 서버 단가표 연동은 포함하지
않는다. 비용을 구할 수 없으면 계산 불가라고 표시한다.

미수집 재시도·턴 시간과 성공 여부는 0이나 실패로 추정하지 않는다. 작업 유형은 서버 분류를
사용한다. 턴 시간이 없으면 흐름 비율을 턴 수로 표시한다. 구현되지 않은 세션 재개·인계 버튼은
표시하지 않는다. Home·앱 공통 헤더 등 Activity 바깥의 목 데이터는 별도 작업이다.

검증:

```powershell
go test ./cmd/telemetryctl ./internal/...
node --test cmd/pulsemetry-gui/frontend/tests/activity-adapter.test.mjs
task check:frontend
task build
```

기동한 데몬과 GUI를 새 빌드로 재시작해야 새 API와 화면이 반영된다.

Activity 목록 SQL·검색·필터·커서는 `internal/dashboard/activity/list.go`,
응답 조립은 `internal/dashboard/activity/build.go`, 응답 타입은 `types.go`에 둔다.
`dashboard/tray`와 같은 파일 구성을 따른다. 상세 조회는 `Service.ReadSnapshot`이 넘긴 `SQLQuerier`로
세션·지표·분류에 같은 읽기 트랜잭션을 전달한다. 첫 SELECT 이후 새 배치가 커밋되어도
현재 응답은 같은 시점의 데이터를 유지하며, 다음 조회부터 새 배치를 반영한다.
트랜잭션은 JSON 전송 전에 종료하며 오류·취소 시에도 정리한다. 아직 수신하지 않은 이벤트의
완전성을 보장하는 기능은 아니다.

`TestReadSnapshotSurvivesConcurrentCommit`은 실제 WAL DB에서 조회 사이에 새 턴과 LLM 호출을
커밋해 현재 응답과 다음 응답을 비교한다. 콜백 실패·취소 후 연결 재사용과 DB 부재도 검증한다.
