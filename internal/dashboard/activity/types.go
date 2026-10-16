package activity

import "github.com/your-org/pulsemetry/internal/dashboard"

// Detail은 같은 스냅샷에서 읽은 세션 상세·지표·분류다.
type Detail struct {
	Detail         dashboard.SessionDetail         `json:"detail"`
	Metrics        dashboard.SessionMetrics        `json:"metrics"`
	Classification dashboard.SessionClassification `json:"classification"`
}

// Cursor는 마지막으로 받은 줄의 진행 여부·정렬 시각·ID다 (ADR 0027).
// 진행 중의 정렬 시각은 최근 활동, 종료의 정렬 시각은 시작이다.
// 페이지 사이에 상태나 활동이 바뀌면 첫 페이지부터 새로고침해야 최신 순서가 반영된다.
//
// ID 가 0 이면 "첫 페이지" 다. sessions.id 는 1부터라 유효한 커서와 겹치지 않는다.
type Cursor struct {
	Running bool  `json:"running"`
	SortAt  int64 `json:"sort_at"`
	ID      int64 `json:"id"`
}

// Query 는 Activity 목록 한 페이지의 조회 조건이다 (PROJ-90).
//
// 필터는 서로 AND 이고, 같은 필터 안의 여러 값은 OR 이다 — 화면의 다중 선택이 그 모양이다.
// 빈 목록·빈 문자열은 "거르지 않음" 이고 에러가 아니다.
type Query struct {
	// Since·Until 은 started_at 범위(UTC unix 초)다. Since 포함, Until 배타. 0 이면 무제한.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	// Vendors 는 vendor_id 다중 선택이다.
	Vendors []string `json:"vendors"`
	// Projects 는 workspace_path **원경로** 다중 선택이다 (ADR 0010). basename 이 아니다 —
	// 서로 다른 폴더의 같은 이름 프로젝트가 한 필터로 뭉개지면 안 된다.
	Projects []string `json:"projects"`
	// Status 는 running|completed|abandoned|handoff 다중 선택이다. 뒤의 둘은 v3 에서
	// 산출되지 않아 항상 빈 결과를 준다 (ADR 0009).
	Status []string `json:"status"`
	// Text 는 통합 검색어다. 사용자가 입력한 그대로 넣는다 — 와일드카드 escape 는 여기가 한다.
	Text string `json:"text"`

	Limit int `json:"limit"`
	// Cursor 는 이전 페이지의 NextCursor 다. 첫 페이지는 비워 둔다.
	Cursor Cursor `json:"cursor"`
}

// Row 는 Activity 목록 한 줄이다.
//
// 시작·경로·소요·토큰·비용·상태는 SessionRow 가 이미 갖고 있어 그대로 묻어 온다. JSON 에서
// 임베드는 평평하게 펼쳐지므로 화면은 한 겹 구조로 읽는다.
type Row struct {
	dashboard.SessionRow

	// WorkType 은 목록의 "작업" 열이다 — 이 세션이 무엇을 한 세션인지(구현·디버깅·리뷰 …).
	//
	// TODO(PROJ-92): 턴 분류가 이 값의 **유일한** 출처다. 그 작업이 붙기 전까지 항상 빈
	// 문자열이고, 여기서 휴리스틱으로 추측해 채우지 않는다 — 근거 없는 값이 목록에 뜨면
	// 사용자는 그것을 분류 결과로 읽는다. 화면은 빈 문자열을 "미분류" 로 그리면 된다.
	// v3 turns 에는 work_type 컬럼이 없으므로(v2 에 있었고 v3 가 지웠다) PROJ-92 는
	// 저장할 자리부터 만들어야 한다. 이 필드가 그 결과를 받을 자리다.
	WorkType string `json:"work_type"`

	// MatchedSources 는 검색어가 걸린 출처다 (title|workspace|file|content). 검색어가
	// 없으면 빈 슬라이스다. 화면이 "파일명에서 발견" 같은 배지를 붙일 수 있어야 한다.
	MatchedSources []string `json:"matched_sources"`
}

// Page 는 목록 한 페이지와 다음 페이지 정보다.
type Page struct {
	Rows []Row `json:"rows"`

	// HasMore 가 "더 불러오기" 버튼의 유일한 근거다. Rows 가 Limit 만큼 찼다는 사실로는
	// 마지막 페이지를 구분할 수 없다 — 딱 맞아떨어진 경우와 더 있는 경우가 같아 보인다.
	// 그래서 Limit+1 개를 받아 한 개가 남는지로 판정한다 (별도 COUNT 질의를 돌지 않는다).
	HasMore bool `json:"has_more"`

	// NextCursor 는 **항상** 마지막 줄의 위치다. HasMore 가 false 여도 0 으로 비우지 않는다 —
	// 비워 두면 HasMore 를 안 보는 호출자가 그것을 "첫 페이지" 로 읽어 처음부터 무한히 다시
	// 받는다. 마지막 줄을 그대로 두면 한 번 더 불러도 빈 페이지가 와서 그 자리에서 멈춘다.
	// Rows 가 비어 있으면 커서도 비어 있다.
	NextCursor Cursor `json:"next_cursor"`
}
