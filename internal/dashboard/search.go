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
	// maxFTSTokens·maxFTSTokenRunes 는 사용자 입력이 만들 수 있는 FTS 질의 크기를 묶는다.
	// 붙여넣기 한 번으로 수천 토큰짜리 MATCH 가 생기면 조회가 몇 초씩 걸린다.
	maxFTSTokens     = 12
	maxFTSTokenRunes = 64
)

// SearchQuery 는 통합 검색 조건이다 (계획서 「검색(제목·파일·원문)」).
type SearchQuery struct {
	// Text 는 사용자가 입력한 그대로다. FTS5 문법으로 해석되지 않도록 정제해서 쓴다.
	Text string `json:"text"`
	// Since·Until 은 세션 started_at 범위(UTC unix 초)다. 0 이면 무제한.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	Limit int   `json:"limit"`
}

// Hit 는 검색 결과 한 건이다. 세션 단위로 합쳐서 준다 — 같은 세션이 제목·파일·원문 모두에
// 걸렸다고 목록에 세 번 나오면 결과가 실제보다 많아 보이고 클릭할 대상도 같다.
type Hit struct {
	SessionID   string `json:"session_id"`
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
	// MatchedFiles 는 파일명으로 걸린 파일들이다 (basename 만 — 전체 경로는 저장하지 않는다).
	MatchedFiles []string `json:"matched_files"`
}

// Search 는 제목·파일명·원문 세 출처를 한 번에 뒤진다.
//
// 빈 질의는 에러가 아니라 빈 결과다. 검색창을 지우는 동작이 매번 에러 토스트를 띄울 이유가 없다.
func (r *Reader) Search(ctx context.Context, q SearchQuery) ([]Hit, error) {
	out := []Hit{}
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return out, nil
	}
	db, ok := r.db()
	if !ok {
		return out, nil
	}

	limit := clampLimit(q.Limit, defaultSearchLimit, maxSearchLimit)
	scan := limit * perSourceFactor

	acc := newHitAccumulator()
	if err := searchTitles(ctx, db, text, scan, acc); err != nil {
		return nil, err
	}
	if err := searchFiles(ctx, db, text, scan, acc); err != nil {
		return nil, err
	}
	if err := searchContent(ctx, db, text, scan, acc); err != nil {
		return nil, err
	}
	if len(acc.order) == 0 {
		return out, nil
	}
	return acc.resolve(ctx, db, q, limit)
}

// ── 출처 1: sessions.title ──────────────────────────────────────────────────

const searchTitleSQL = `SELECT session_id FROM sessions
WHERE title LIKE ? ESCAPE '\'
ORDER BY started_at DESC LIMIT ?`

func searchTitles(ctx context.Context, db sqlQuerier, text string, limit int, acc *hitAccumulator) (err error) {
	const op = "제목 검색"
	rows, err := db.QueryContext(ctx, searchTitleSQL, likePattern(text), limit)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var id string
		if serr := rows.Scan(&id); serr != nil {
			return queryErr(op, serr)
		}
		acc.mark(id, SourceTitle)
	}
	return nil
}

// ── 출처 2: session_files.file_name ─────────────────────────────────────────

const searchFileSQL = `SELECT session_id, file_name FROM session_files
WHERE file_name LIKE ? ESCAPE '\'
ORDER BY last_ts DESC LIMIT ?`

func searchFiles(ctx context.Context, db sqlQuerier, text string, limit int, acc *hitAccumulator) (err error) {
	const op = "파일명 검색"
	rows, err := db.QueryContext(ctx, searchFileSQL, likePattern(text), limit)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var id, name string
		if serr := rows.Scan(&id, &name); serr != nil {
			return queryErr(op, serr)
		}
		acc.mark(id, SourceFile)
		acc.addFile(id, name)
	}
	return nil
}

// ── 출처 3: content_fts ─────────────────────────────────────────────────────

// external content FTS5 라 본문으로 되돌아가려면 event_content·events 를 거쳐야 한다.
// snippet() 의 첫 인자는 FTS 테이블 이름이라 별칭을 붙이지 않는다. 마지막 인자(발췌 토큰 수)는
// 자리표시자가 아니라 리터럴이다 — 보조 함수의 인자를 바인딩으로 넘기면 드라이버·버전에 따라
// 해석이 갈린다. 사용자 입력이 아니라 우리 상수라 리터럴이어도 안전하다.
const searchContentSQL = `SELECT e.session_id, snippet(content_fts, 0, '', '', '…', 12)
FROM content_fts
JOIN event_content c ON c.id = content_fts.rowid
JOIN events e ON e.id = c.event_id
WHERE content_fts MATCH ? AND e.session_id IS NOT NULL
ORDER BY e.ts DESC LIMIT ?`

func searchContent(ctx context.Context, db sqlQuerier, text string, limit int, acc *hitAccumulator) (err error) {
	const op = "원문 검색"
	match := ftsQuery(text)
	if match == "" {
		// 문장부호만 입력한 경우다. FTS 에 빈 질의를 넣으면 문법 오류가 나므로 이 출처만 건너뛴다.
		return nil
	}
	rows, err := db.QueryContext(ctx, searchContentSQL, match, limit)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var id, snippet string
		if serr := rows.Scan(&id, &snippet); serr != nil {
			return queryErr(op, serr)
		}
		acc.mark(id, SourceContent)
		acc.addSnippet(id, snippet)
	}
	return nil
}

// ── 결과 병합 ───────────────────────────────────────────────────────────────

type hitAccumulator struct {
	order []string
	byID  map[string]*Hit
}

func newHitAccumulator() *hitAccumulator {
	return &hitAccumulator{byID: make(map[string]*Hit)}
}

func (a *hitAccumulator) get(id string) *Hit {
	h, ok := a.byID[id]
	if !ok {
		h = &Hit{SessionID: id, Sources: []string{}, MatchedFiles: []string{}}
		a.byID[id] = h
		a.order = append(a.order, id)
	}
	return h
}

func (a *hitAccumulator) mark(id, source string) {
	h := a.get(id)
	for _, s := range h.Sources {
		if s == source {
			return
		}
	}
	h.Sources = append(h.Sources, source)
}

func (a *hitAccumulator) addFile(id, name string) {
	h := a.get(id)
	for _, f := range h.MatchedFiles {
		if f == name {
			return
		}
	}
	h.MatchedFiles = append(h.MatchedFiles, name)
}

func (a *hitAccumulator) addSnippet(id, snippet string) {
	h := a.get(id)
	if h.Snippet == "" {
		h.Snippet = snippet
	}
}

// resolve 는 모은 session_id 에 세션 메타데이터를 붙이고 정렬·필터·자르기를 한다.
//
// 세션 행이 없는 히트도 버리지 않는다. 원문(events)이 세션보다 먼저 지워지는 일은 보존
// 정책상 없지만, 세션 쓰기가 실패한 배치가 있으면 생길 수 있다 — 결과에서 조용히 사라지는
// 것보다 제목 없는 한 줄로 보이는 편이 진단 가능하다.
func (a *hitAccumulator) resolve(ctx context.Context, db sqlQuerier, q SearchQuery, limit int) (hits []Hit, err error) {
	const op = "검색 결과 조회"

	ids := make([]any, len(a.order))
	for i, id := range a.order {
		ids[i] = id
	}
	query := `SELECT session_id, vendor, COALESCE(title,''), started_at, status, COALESCE(project_name,'')
FROM sessions WHERE session_id IN (` + placeholders(len(ids)) + `)`
	rows, err := db.QueryContext(ctx, query, ids...)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			id, vendor, title, status, project string
			started                            int64
		)
		if serr := rows.Scan(&id, &vendor, &title, &started, &status, &project); serr != nil {
			return nil, queryErr(op, serr)
		}
		h := a.byID[id]
		if h == nil {
			continue
		}
		h.Vendor, h.Title, h.StartedAt, h.Status, h.ProjectName = vendor, title, started, status, project
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
	// 최근 세션 우선. session_id 2순위는 동시각 세션의 순서를 고정하기 위한 것이다.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].StartedAt != hits[j].StartedAt {
			return hits[i].StartedAt > hits[j].StartedAt
		}
		return hits[i].SessionID < hits[j].SessionID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// ── 입력 정제 ───────────────────────────────────────────────────────────────

// ftsQuery 는 사용자 입력을 FTS5 MATCH 에 넣어도 안전한 질의로 바꾼다.
//
// # 왜 필요한가
//
// MATCH 의 오른쪽은 문자열이 아니라 **질의 언어** 다. 사용자가 친 것을 그대로 넣으면
// 두 가지가 일어난다.
//
//   - 문법 오류: `"`, `(`, `NEAR(`, 끝에 붙은 `AND` 는 SQL 에러가 되고, 그 에러 메시지가
//     Promise reject 로 사용자 화면에 뜬다.
//   - 의도치 않은 해석: `AND`·`OR`·`NOT`·`NEAR`·`*`·`^` 는 연산자다. "AND 게이트" 를 찾으면
//     연산자로 읽혀 엉뚱한 결과가 나온다.
//
// # 어떻게 바꾸는가
//
// 유니코드 글자·숫자만 토큰으로 남기고 나머지는 전부 구분자로 본다. FTS5 기본 토크나이저
// (unicode61)가 색인할 때 쓰는 규칙과 같아서, 여기서 버리는 문자는 애초에 색인에도 없다.
// 한글은 글자로 분류되므로 "인증 토큰" 은 두 토큰으로 남는다.
//
// 각 토큰은 큰따옴표로 감싼다. 따옴표 안에서는 AND·OR·NEAR 가 연산자가 아니라 낱말이다.
// 토큰을 나열하면 FTS5 의 암묵적 AND 라 "전부 포함" 이 된다.
//
// 마지막 토큰에는 `*` 를 붙여 접두 검색으로 만든다. 검색창은 타이핑 중에도 결과를 보여야
// 하는데 완전 일치만 하면 "프록" 이 "프록시" 를 못 찾는다.
//
// 남는 토큰이 없으면 빈 문자열을 돌려주고 호출자가 이 출처를 건너뛴다.
func ftsQuery(s string) string {
	tokens := ftsTokens(s)
	if len(tokens) == 0 {
		return ""
	}
	var b strings.Builder
	for i, t := range tokens {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('"')
		// 토큰에는 글자·숫자만 남아 따옴표가 들어올 수 없지만, 규칙이 바뀌어도 질의가 깨지지
		// 않도록 FTS5 규약(따옴표 두 번)대로 이스케이프해 둔다.
		b.WriteString(strings.ReplaceAll(t, `"`, `""`))
		b.WriteByte('"')
		if i == len(tokens)-1 {
			b.WriteByte('*')
		}
	}
	return b.String()
}

func ftsTokens(s string) []string {
	var (
		tokens []string
		cur    []rune
	)
	flush := func() {
		if len(cur) == 0 {
			return
		}
		if len(cur) > maxFTSTokenRunes {
			cur = cur[:maxFTSTokenRunes]
		}
		tokens = append(tokens, string(cur))
		cur = nil
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
			continue
		}
		flush()
		if len(tokens) >= maxFTSTokens {
			return tokens
		}
	}
	flush()
	if len(tokens) > maxFTSTokens {
		tokens = tokens[:maxFTSTokens]
	}
	return tokens
}

// likePattern 은 LIKE 부분 일치 패턴을 만든다.
//
// `%` 와 `_` 는 LIKE 의 와일드카드다. 사용자가 친 `_test` 를 그대로 넣으면 `_` 가 "아무 글자
// 하나" 로 읽혀 `atest`·`btest` 까지 걸린다. ESCAPE '\' 와 짝을 이룬다 — 질의문에서 ESCAPE 를
// 빼면 여기서 붙인 역슬래시가 리터럴이 되어 아무것도 못 찾는다.
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}
