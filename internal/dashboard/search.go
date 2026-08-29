package dashboard

import (
	"context"
	"sort"
	"strings"
	"unicode"
)

// 검색 출처. 하나의 세션이 여러 출처에서 걸릴 수 있어 Hit.Sources 는 슬라이스다.
const (
	SourceTitle   = "title"
	SourceFile    = "file"
	SourceContent = "content"
)

const (
	defaultSearchLimit = 50
	maxSearchLimit     = 200
	// perSourceFactor 는 출처별로 얼마나 많이 긁어올지다. 세 출처가 같은 세션을 가리키는 일이
	// 흔해서 최종 개수보다 넉넉히 받아야 limit 를 채운다.
	perSourceFactor = 4
	// maxSearchRunes 는 받아들이는 검색어 길이다. 붙여넣기 한 번으로 수천 자짜리 LIKE
	// 패턴이 생기면 전체 스캔이 몇 초씩 걸린다.
	maxSearchRunes = 128
	// snippetContextRunes 는 발췌에서 일치 지점 앞뒤로 보여 줄 룬 수다.
	snippetContextRunes = 40
)

// SearchQuery 는 통합 검색 조건이다 (계획서 「검색(제목·파일·원문)」).
type SearchQuery struct {
	// Text 는 사용자가 입력한 그대로다. LIKE 와일드카드는 escape 해서 쓴다.
	Text string `json:"text"`
	// Since·Until 은 세션 started_at 범위(UTC unix 초)다. 0 이면 무제한.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	Limit int   `json:"limit"`
}

// Hit 는 검색 결과 한 건이다. 세션 단위로 합쳐서 준다 — 같은 세션이 제목·파일·원문 모두에
// 걸렸다고 목록에 세 번 나오면 결과가 실제보다 많아 보이고 클릭할 대상도 같다.
type Hit struct {
	// ID 는 sessions.id 다. Session() 에 그대로 넘길 수 있는 값이어야 한다.
	ID          int64  `json:"id"`
	SessionKey  string `json:"session_key"`
	Vendor      string `json:"vendor"`
	Title       string `json:"title"`
	StartedAt   int64  `json:"started_at"`
	Status      string `json:"status"`
	ProjectName string `json:"project_name"`

	// Sources 는 title|file|content 중 걸린 출처다. 화면이 "파일명에서 발견" 같은 배지를
	// 붙일 수 있어야 한다.
	Sources []string `json:"sources"`
	// Snippet 은 본문에서 뽑은 발췌다. 원문 출처로 걸리지 않았으면 빈 문자열이다.
	Snippet string `json:"snippet"`
	// MatchedFiles 는 경로로 걸린 파일들의 basename 이다.
	MatchedFiles []string `json:"matched_files"`
}

// Search 는 제목·파일 경로·원문 세 출처를 한 번에 뒤진다.
//
// # 왜 LIKE 인가
//
// v3 에는 FTS5 가상 테이블이 없다. ADR 0009 가 원문 검색을 `LIKE` 로 하기로 정하면서
// ADR 0002 의 "LIKE 폴백을 두지 않는다" 를 철회했다. 대가는 전체 스캔이고, 단일 개발자
// 로컬 규모(400일 보존)에서는 수용 가능하다는 것이 그 결정의 근거다.
//
// 검색 대상은 sessions.title · file_changes.file_path · turns.prompt_text 세 컬럼이다.
//
// 빈 질의는 에러가 아니라 빈 결과다. 검색창을 지우는 동작이 매번 에러 토스트를 띄울 이유가 없다.
func (r *Reader) Search(ctx context.Context, q SearchQuery) ([]Hit, error) {
	out := []Hit{}
	text := capRunes(strings.TrimSpace(q.Text), maxSearchRunes)
	if text == "" {
		return out, nil
	}
	db, ok := r.db()
	if !ok {
		return out, nil
	}

	limit := clampLimit(q.Limit, defaultSearchLimit, maxSearchLimit)
	scan := limit * perSourceFactor
	pattern := likePattern(text)

	acc := newHitAccumulator()
	if err := searchTitles(ctx, db, pattern, scan, acc); err != nil {
		return nil, err
	}
	if err := searchFiles(ctx, db, pattern, scan, acc); err != nil {
		return nil, err
	}
	if err := searchContent(ctx, db, pattern, text, scan, acc); err != nil {
		return nil, err
	}
	if len(acc.order) == 0 {
		return out, nil
	}
	return acc.resolve(ctx, db, q, limit)
}

// ── 출처 1: sessions.title ──────────────────────────────────────────────────

const searchTitleSQL = `SELECT s.id FROM sessions s
WHERE s.title LIKE ? ESCAPE '\'
ORDER BY s.started_at DESC LIMIT ?`

func searchTitles(ctx context.Context, db sqlQuerier, pattern string, limit int, acc *hitAccumulator) (err error) {
	const op = "제목 검색"
	rows, err := db.QueryContext(ctx, searchTitleSQL, pattern, limit)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var id int64
		if serr := rows.Scan(&id); serr != nil {
			return queryErr(op, serr)
		}
		acc.mark(id, SourceTitle)
	}
	return nil
}

// ── 출처 2: file_changes.file_path ──────────────────────────────────────────

// 파일 변경은 세션에 직접 매달리지 않는다. tool_calls → turns 를 거쳐야 세션에 닿는다.
const searchFileSQL = `SELECT t.session_id, f.file_path
FROM file_changes f
JOIN tool_calls c ON c.id = f.tool_call_id
JOIN turns t ON t.id = c.turn_id
WHERE f.file_path LIKE ? ESCAPE '\'
ORDER BY c.called_at DESC LIMIT ?`

func searchFiles(ctx context.Context, db sqlQuerier, pattern string, limit int, acc *hitAccumulator) (err error) {
	const op = "파일 경로 검색"
	rows, err := db.QueryContext(ctx, searchFileSQL, pattern, limit)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			id   int64
			path string
		)
		if serr := rows.Scan(&id, &path); serr != nil {
			return queryErr(op, serr)
		}
		acc.mark(id, SourceFile)
		acc.addFile(id, baseName(path))
	}
	return nil
}

// ── 출처 3: turns.prompt_text ───────────────────────────────────────────────

// v3 에는 원문 테이블이 없다. 남는 원문은 사용자 프롬프트 하나뿐이고 그것은 턴에 붙어 있다
// (store/resolve.go 의 promptText). 발췌는 FTS5 의 snippet() 이 하던 일인데 그 함수도
// 함께 사라져서 Go 가 만든다 (snippetOf).
const searchContentSQL = `SELECT t.session_id, t.prompt_text
FROM turns t
WHERE t.prompt_text LIKE ? ESCAPE '\'
ORDER BY t.started_at DESC LIMIT ?`

func searchContent(ctx context.Context, db sqlQuerier, pattern, text string, limit int, acc *hitAccumulator) (err error) {
	const op = "원문 검색"
	rows, err := db.QueryContext(ctx, searchContentSQL, pattern, limit)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			id   int64
			body string
		)
		if serr := rows.Scan(&id, &body); serr != nil {
			return queryErr(op, serr)
		}
		acc.mark(id, SourceContent)
		acc.addSnippet(id, snippetOf(body, text))
	}
	return nil
}

// ── 결과 병합 ───────────────────────────────────────────────────────────────

type hitAccumulator struct {
	order []int64
	byID  map[int64]*Hit
}

func newHitAccumulator() *hitAccumulator {
	return &hitAccumulator{byID: make(map[int64]*Hit)}
}

func (a *hitAccumulator) get(id int64) *Hit {
	h, ok := a.byID[id]
	if !ok {
		h = &Hit{ID: id, Sources: []string{}, MatchedFiles: []string{}}
		a.byID[id] = h
		a.order = append(a.order, id)
	}
	return h
}

func (a *hitAccumulator) mark(id int64, source string) {
	h := a.get(id)
	for _, s := range h.Sources {
		if s == source {
			return
		}
	}
	h.Sources = append(h.Sources, source)
}

func (a *hitAccumulator) addFile(id int64, name string) {
	if name == "" {
		return
	}
	h := a.get(id)
	for _, f := range h.MatchedFiles {
		if f == name {
			return
		}
	}
	h.MatchedFiles = append(h.MatchedFiles, name)
}

func (a *hitAccumulator) addSnippet(id int64, snippet string) {
	h := a.get(id)
	if h.Snippet == "" {
		h.Snippet = snippet
	}
}

// resolve 는 모은 세션 id 에 메타데이터를 붙이고 정렬·필터·자르기를 한다.
//
// 세션 행이 없는 히트도 버리지 않는다. 원문·파일 변경이 세션보다 먼저 지워지는 일은 보존
// 정책상 없지만(삭제는 자식에서 부모 순서다), 그래도 결과에서 조용히 사라지는 것보다
// 제목 없는 한 줄로 보이는 편이 진단 가능하다.
func (a *hitAccumulator) resolve(ctx context.Context, db sqlQuerier, q SearchQuery, limit int) (hits []Hit, err error) {
	const op = "검색 결과 조회"

	ids := make([]any, len(a.order))
	for i, id := range a.order {
		ids[i] = id
	}
	query := `SELECT s.id, s.session_key, s.vendor_id, COALESCE(s.title,''),
	  COALESCE(s.started_at,0), ` + statusExpr + `, COALESCE(s.workspace_path,'')
FROM sessions s WHERE s.id IN (` + placeholders(len(ids)) + `)`
	rows, err := db.QueryContext(ctx, query, ids...)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			id                                    int64
			key, vendor, title, status, workspace string
			started                               int64
		)
		if serr := rows.Scan(&id, &key, &vendor, &title, &started, &status, &workspace); serr != nil {
			return nil, queryErr(op, serr)
		}
		h := a.byID[id]
		if h == nil {
			continue
		}
		h.SessionKey, h.Vendor, h.Title, h.StartedAt, h.Status = key, vendor, title, started, status
		h.ProjectName = baseName(workspace)
	}

	hits = make([]Hit, 0, len(a.order))
	for _, id := range a.order {
		h := a.byID[id]
		if q.Since > 0 && h.StartedAt < q.Since {
			continue
		}
		if q.Until > 0 && h.StartedAt >= q.Until {
			continue
		}
		hits = append(hits, *h)
	}
	// 최근 세션 우선. id 2순위는 동시각 세션의 순서를 고정하기 위한 것이다.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].StartedAt != hits[j].StartedAt {
			return hits[i].StartedAt > hits[j].StartedAt
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// ── 입력 정제와 발췌 ────────────────────────────────────────────────────────

// likePattern 은 LIKE 부분 일치 패턴을 만든다.
//
// `%` 와 `_` 는 LIKE 의 와일드카드다. 사용자가 친 `_test` 를 그대로 넣으면 `_` 가 "아무 글자
// 하나" 로 읽혀 `atest`·`btest` 까지 걸린다. ESCAPE '\' 와 짝을 이룬다 — 질의문에서 ESCAPE 를
// 빼면 여기서 붙인 역슬래시가 리터럴이 되어 아무것도 못 찾는다.
//
// FTS5 시절과 달리 사용자 입력이 **질의 언어로 해석되지 않는다.** LIKE 의 오른쪽은 그냥
// 문자열이라 연산자도 괄호도 없고, 값은 전부 바인딩되므로 정제할 것은 와일드카드뿐이다.
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// capRunes 는 룬 기준으로 자른다. 바이트로 자르면 한글 3바이트 중간에서 끊겨 뒤따르는
// LIKE 패턴에 깨진 룬이 들어간다 (session/title.go 의 같은 이름 함수와 같은 이유).
func capRunes(s string, limit int) string {
	r := []rune(s)
	if limit <= 0 || len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

// snippetOf 는 본문에서 일치 지점 주변을 잘라 낸다 — FTS5 의 snippet() 을 대신한다.
//
// **룬 단위로 자른다.** 바이트로 자르면 한글 한 글자의 중간에서 끊겨 화면에 U+FFFD 가 뜬다.
// 대소문자 구분 없이 찾는 이유는 LIKE 가 ASCII 에 대해 그렇게 매칭하기 때문이다 — 여기서
// 못 찾으면 히트인데 발췌만 비는 모순이 생긴다.
//
// 잘린 쪽에는 말줄임표를 붙인다. 없으면 발췌가 완결된 문장으로 읽혀 원문을 오해하게 된다.
func snippetOf(body, needle string) string {
	if body == "" {
		return ""
	}
	text := []rune(body)
	at := indexFold(text, []rune(needle))
	if at < 0 {
		// LIKE 는 걸렸는데 여기서 못 찾는 경우다 (와일드카드 escape 의 경계 등).
		// 본문 앞머리를 준다 — 발췌가 통째로 비는 것보다 낫다.
		at = 0
	}

	start := max(0, at-snippetContextRunes)
	end := min(len(text), at+len([]rune(needle))+snippetContextRunes)

	var b strings.Builder
	if start > 0 {
		b.WriteRune('…')
	}
	b.WriteString(strings.TrimSpace(string(text[start:end])))
	if end < len(text) {
		b.WriteRune('…')
	}
	return b.String()
}

// indexFold 는 대소문자를 무시한 룬 부분열 검색이다. -1 은 못 찾았다는 뜻이다.
//
// strings.Index 를 쓰지 않는 이유는 결과가 **룬 인덱스** 여야 하기 때문이다. 바이트
// 인덱스를 받아 룬으로 환산하면 ToLower 가 길이를 바꾸는 문자에서 어긋난다.
func indexFold(hay, needle []rune) int {
	if len(needle) == 0 {
		return 0
	}
	if len(needle) > len(hay) {
		return -1
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j, r := range needle {
			if unicode.ToLower(hay[i+j]) != unicode.ToLower(r) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
