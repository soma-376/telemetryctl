package session

import (
	"math"
	"sort"

	"github.com/your-org/pulsemetry/internal/event"
)

// state 는 조립 중인 세션 하나의 내부 상태다. 조회 시점에 이벤트를 스캔하지 않도록
// 수치를 이벤트 도착마다 증분 갱신한다 (ADR 0005).
type state struct {
	id          string
	vendor      string
	started     event.UnixSec
	last        event.UnixSec
	ended       event.Opt[event.UnixSec]
	status      Status
	projectHash string
	projectName string

	// 로컬 저장 전용 식별 정보 (ADR 0010). 세션 안에서 한 번만 정한다 —
	// 도중에 바뀌면 세션의 정체성이 흔들리고 화면 필터에서 세션이 사라진다.
	workspacePath string
	userEmail     string
	userAccountID string
	terminalType  string

	// 첫 프롬프트에서 뽑은 파생 문자열. 본문은 여기 남기지 않는다.
	promptTitle   string
	promptSummary string

	activeSeconds       float64
	costUSD             float64
	inputTokens         int64
	outputTokens        int64
	cacheReadTokens     int64
	cacheCreationTokens int64
	toolCalls           int64
	toolErrors          int64
	toolRejects         int64
	apiRequests         int64
	apiErrors           int64
	retries             int64
	prompts             int64
	responses           int64

	lines lineLedger
	files map[string]*fileState
	tools []ToolEvent
	mcp   map[string]*MCPUsage

	// 마지막 툴 이벤트의 성공 여부. abandoned 판정의 유일한 근거다.
	// 성공 여부가 설정된 이벤트에서만 갱신한다 — 미상은 실패가 아니다.
	lastOutcomeSeen bool
	lastOutcomeOK   bool
	lastOutcomeTool string
	lastOutcomeTS   event.UnixSec

	// accept 결정은 뒤따르는 tool_result 에 붙인다. reject 는 결과가 없어 즉시 행이 된다.
	pendingAccept map[string]string

	cumulative map[seriesKey]event.CumulativeState
	// watchFrom 은 조립기가 이벤트를 보기 시작한 시각이다 (Assembler.watchFrom).
	// 세션이 아니라 조립기의 기준점이라는 점이 중요하다 — rollup 의 집계기와 같은 값이어야
	// 같은 스트림에서 sessions 와 rollup_hourly 의 누적 집계가 갈리지 않는다.
	watchFrom event.UnixNano

	statusReason string
	reopens      int
	discarded    int64
}

// fileState 에는 라인 필드가 없다. 라인 수는 lineLedger 만 소유해서 배분 합계가 세션
// 합계를 넘는 코드를 애초에 쓸 수 없게 한다 (lines.go 의 불변식).
type fileState struct {
	hash   string
	name   string
	ext    string
	edits  int64
	lastTS event.UnixSec
}

func newState(e event.Event, watchFrom event.UnixNano) *state {
	ts := e.TS.Sec()
	return &state{
		id:            e.SessionID,
		vendor:        e.Vendor,
		started:       ts,
		last:          ts,
		status:        StatusRunning,
		files:         make(map[string]*fileState),
		mcp:           make(map[string]*MCPUsage),
		pendingAccept: make(map[string]string),
		cumulative:    make(map[seriesKey]event.CumulativeState),
		watchFrom:     watchFrom,
	}
}

// observe 는 어떤 이벤트든 공통으로 갱신하는 것을 처리한다.
func (s *state) observe(e event.Event) {
	ts := e.TS.Sec()
	// 이벤트가 순서대로 온다고 가정하지 않는다. exporter 배치가 섞이면 첫 도착이
	// 가장 이른 이벤트가 아니다.
	if ts < s.started {
		s.started = ts
	}
	if ts > s.last {
		s.last = ts
	}
	if s.ended.Valid() {
		// 같은 session.id 재등장 — 마감을 되돌리고 진행 중으로 되돌린다.
		// 늦게 도착한 낙오 이벤트여서 여전히 유휴라면 다음 Advance 가 즉시 다시 마감한다.
		// ended_at 은 last_event_at 이라 낙오 이벤트가 마감 시각을 흔들지도 않는다.
		s.ended = event.Opt[event.UnixSec]{}
		s.status = StatusRunning
		s.statusReason = ""
		s.reopens++
	}
	if s.vendor == "" {
		s.vendor = e.Vendor
	}
	// 프로젝트는 한 번만 정한다. 세션 도중에 작업 디렉터리가 바뀌어도 세션의 정체성은
	// 처음 본 프로젝트다 — 바뀌면 화면의 프로젝트 필터에서 세션이 사라진다.
	if s.projectHash == "" && e.Attr.ProjectHash != "" {
		s.projectHash = e.Attr.ProjectHash
		s.projectName = e.Attr.ProjectName
	}
	// 식별 정보도 같은 규칙이다 — 먼저 관측한 값이 이긴다.
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&s.workspacePath, e.Attr.WorkspacePath},
		{&s.userEmail, e.Attr.UserEmail},
		{&s.userAccountID, e.Attr.UserAccountID},
		{&s.terminalType, e.Attr.TerminalType},
	} {
		if *f.dst == "" && f.src != "" {
			*f.dst = f.src
		}
	}
}

// apply 는 이벤트 종류별 수치를 반영한다.
//
// 출처를 종류별로 하나씩만 고른다. Claude Code 는 같은 사실을 로그와 메트릭 양쪽으로
// 내보내므로(api_request 로그에 cost·token 이 있고 cost.usage·token.usage 메트릭도 있다)
// 둘 다 세면 비용이 두 배가 된다.
//
//	총량(토큰·비용·라인·활동 시간) → 메트릭에서만
//	건수(프롬프트·툴·API 요청·재시도) → 로그 이벤트에서만
func (s *state) apply(in Input) {
	e := in.Event

	if in.Content.Kind == event.ContentPrompt && s.promptTitle == "" {
		if t, sum := deriveFromPrompt(in.Content.Body); t != "" {
			s.promptTitle, s.promptSummary = t, sum
		}
	}
	// 응답 이벤트는 벤더마다 이름이 달라 이름으로 잡지 않는다. response_length 가
	// 설정됐다는 것이 곧 응답이 있었다는 뜻이다. 안 보내는 벤더는 responses=0 이 된다.
	if e.Signal == event.SignalLog {
		if _, ok := e.Measure.ResponseLength.Get(); ok {
			s.responses++
		}
	}

	switch classify(e.Name) {
	case kindTokenUsage:
		s.applyTokens(e)
	case kindCostUsage:
		if v, ok := metricScalar(e); ok {
			s.addFloat(e, "cost", v, &s.costUSD)
		}
	case kindLinesOfCode:
		s.applyLines(e)
	case kindActiveTime:
		if v, ok := metricScalar(e); ok {
			s.addFloat(e, "active", v, &s.activeSeconds)
		}
	case kindUserPrompt:
		s.prompts++
	case kindToolResult:
		s.applyToolResult(in)
	case kindToolDecision:
		s.applyToolDecision(in)
	case kindAPIRequest:
		s.apiRequests++
		s.countRetry(e)
	case kindAPIError:
		s.apiErrors++
		s.countRetry(e)
	}

	s.applyMCP(e)
}

// countRetry 는 attempt 가 2 이상인 요청을 재시도로 센다. 첫 시도는 재시도가 아니다.
func (s *state) countRetry(e event.Event) {
	if n, ok := e.Measure.Attempt.Get(); ok && n > 1 {
		s.retries++
	}
}

func (s *state) applyTokens(e event.Event) {
	// 디코더가 종류별 컬럼을 채워 줬으면 그걸 쓴다. 안 채웠으면 type 속성으로 값을 가른다.
	// 둘 중 하나만 타므로 이중 집계가 없다.
	typed := false
	for _, f := range []struct {
		name string
		src  event.Opt[int64]
		dst  *int64
	}{
		{"input", e.Measure.InputTokens, &s.inputTokens},
		{"output", e.Measure.OutputTokens, &s.outputTokens},
		{"cache_read", e.Measure.CacheReadTokens, &s.cacheReadTokens},
		{"cache_creation", e.Measure.CacheCreationTokens, &s.cacheCreationTokens},
	} {
		if v, ok := f.src.Get(); ok {
			s.addInt(e, f.name, float64(v), f.dst)
			typed = true
		}
	}
	if typed {
		return
	}
	v, ok := metricScalar(e)
	if !ok {
		return
	}
	switch normalizeType(e.Attr.Type) {
	case "input":
		s.addInt(e, "input", v, &s.inputTokens)
	case "output":
		s.addInt(e, "output", v, &s.outputTokens)
	case "cacheread":
		s.addInt(e, "cache_read", v, &s.cacheReadTokens)
	case "cachecreation":
		s.addInt(e, "cache_creation", v, &s.cacheCreationTokens)
	}
}

// applyLines 는 세션 합계를 메트릭에서 직접 받는다 — 이 값이 정확한 쪽이다.
// 파일별 배분은 lineLedger 가 근사로 나눈다.
func (s *state) applyLines(e event.Event) {
	v, ok := metricScalar(e)
	if !ok {
		return
	}
	d, ok := s.metricDelta(e, "lines", v)
	if !ok {
		return
	}
	n := int64(math.Round(d))
	switch normalizeType(e.Attr.Type) {
	case "added":
		s.lines.addAdded(n)
	case "removed":
		s.lines.addRemoved(n)
	}
}

func (s *state) applyToolResult(in Input) {
	e := in.Event
	act := actionOf(e.Attr.ToolName)
	ts := e.TS.Sec()

	te := ToolEvent{
		TS:         ts,
		ToolName:   e.Attr.ToolName,
		Action:     act,
		TargetName: in.Target.Name,
		TargetHash: in.Target.Hash,
		Success:    e.Measure.Success,
		DurationMS: e.Measure.DurationMS,
		ErrorType:  e.Measure.ErrorType,
		Decision:   e.Attr.Decision,
		MCPServer:  e.Attr.MCPServer,
	}
	if te.Decision == "" {
		if d, ok := s.pendingAccept[e.Attr.ToolName]; ok {
			te.Decision = d
			delete(s.pendingAccept, e.Attr.ToolName)
		}
	}
	s.tools = append(s.tools, te)
	s.toolCalls++

	// success 가 미설정이면 실패로 세지 않는다. 미상을 실패로 세면 tool_errors 가
	// 과대 집계되고, 그 수치는 화면에 그대로 나간다.
	failed := false
	if ok, set := e.Measure.Success.Get(); set {
		failed = !ok
		if failed {
			s.toolErrors++
		}
		if !s.lastOutcomeSeen || ts >= s.lastOutcomeTS {
			s.lastOutcomeSeen = true
			s.lastOutcomeOK = ok
			s.lastOutcomeTool = e.Attr.ToolName
			s.lastOutcomeTS = ts
		}
	}

	// 실패한 편집은 파일을 바꾸지 않았다. 성공 여부 미상은 반영한다 — 게이트가 꺼진
	// 설치에서 success 가 안 오는데 그 세션의 파일 목록을 통째로 비울 이유는 없다.
	if touchesFile(act) && !failed && in.Target.Hash != "" {
		s.noteEdit(in.Target, ts)
	}
}

func (s *state) applyToolDecision(in Input) {
	e := in.Event
	switch e.Attr.Decision {
	case "reject":
		s.toolRejects++
		// 거부된 호출은 결과 이벤트가 없다. 타임라인에서 사라지지 않게 여기서 행을 만든다.
		s.tools = append(s.tools, ToolEvent{
			TS:         e.TS.Sec(),
			ToolName:   e.Attr.ToolName,
			Action:     actionOf(e.Attr.ToolName),
			TargetName: in.Target.Name,
			TargetHash: in.Target.Hash,
			ErrorType:  e.Measure.ErrorType,
			Decision:   "reject",
			MCPServer:  e.Attr.MCPServer,
		})
	case "accept":
		// 뒤따를 tool_result 에 붙인다. 결과가 끝내 안 오면 조용히 버려진다.
		s.pendingAccept[e.Attr.ToolName] = "accept"
	}
}

func (s *state) noteEdit(p event.Path, ts event.UnixSec) {
	f := s.files[p.Hash]
	if f == nil {
		f = &fileState{hash: p.Hash, name: p.Name, ext: p.Ext}
		s.files[p.Hash] = f
	}
	f.edits++
	if ts > f.lastTS {
		f.lastTS = ts
	}
	s.lines.noteEdit(p.Hash)
}

// applyMCP 는 mcp_session_usage 를 갱신한다. mcp_server 속성이 붙은 이벤트만 대상이다.
func (s *state) applyMCP(e event.Event) {
	if e.Attr.MCPServer == "" {
		return
	}
	row := s.mcp[e.Attr.MCPServer]
	if row == nil {
		row = &MCPUsage{ServerName: e.Attr.MCPServer}
		s.mcp[e.Attr.MCPServer] = row
	}

	switch classify(e.Name) {
	case kindMCPConnection:
		if ok, set := e.Measure.Success.Get(); set && !ok {
			row.ConnectFailures++
		} else {
			row.Connected = true
		}
	case kindToolResult:
		row.ToolCalls++
		// 툴 호출이 성립했다는 것은 연결됐다는 뜻이다. 연결 이벤트를 안 내보내는 벤더에서도
		// "연결됐지만 한 번도 안 쓴 서버" 카드가 오탐하지 않게 한다.
		row.Connected = true
	}

	// 서버별 토큰은 mcp_server 속성이 붙은 이벤트에서만 모은다. 세션 총 토큰과 달리
	// 메트릭에 mcp_server 가 붙어 오는 경우가 드물어 최선 노력 값이다.
	for _, o := range []event.Opt[int64]{e.Measure.InputTokens, e.Measure.OutputTokens} {
		if v, ok := o.Get(); ok && v > 0 {
			row.Tokens += v
		}
	}
}

// session 은 현재 상태를 출력 타입으로 옮긴다. 제목은 여기서 파생한다 — 파일 목록이
// 늘어날 때마다 다시 만들 필요 없이 조회 시점의 상태로 한 번만 계산된다.
func (s *state) session() Session {
	files := s.fileRows()
	title, source, summary := s.title(files)

	end := s.last
	if v, ok := s.ended.Get(); ok {
		end = v
	}

	return Session{
		SessionID:   s.id,
		Vendor:      s.vendor,
		StartedAt:   s.started,
		LastEventAt: s.last,
		EndedAt:     s.ended,
		Status:      s.status,

		Title:       title,
		TitleSource: source,
		Summary:     summary,
		ProjectHash: s.projectHash,
		ProjectName: s.projectName,

		WorkspacePath: s.workspacePath,
		UserEmail:     s.userEmail,
		UserAccountID: s.userAccountID,
		TerminalType:  s.terminalType,

		DurationMS:    int64(end-s.started) * 1000,
		ActiveSeconds: s.activeSeconds,

		InputTokens:         s.inputTokens,
		OutputTokens:        s.outputTokens,
		CacheReadTokens:     s.cacheReadTokens,
		CacheCreationTokens: s.cacheCreationTokens,
		CostUSD:             s.costUSD,

		ToolCalls:   s.toolCalls,
		ToolErrors:  s.toolErrors,
		ToolRejects: s.toolRejects,

		APIRequests: s.apiRequests,
		APIErrors:   s.apiErrors,
		Retries:     s.retries,

		Prompts:   s.prompts,
		Responses: s.responses,

		LinesAdded:   s.lines.added.total,
		LinesRemoved: s.lines.removed.total,

		Files: files,
		Tools: append([]ToolEvent(nil), s.tools...),
		MCP:   s.mcpRows(),

		Diag: Diagnostics{
			StatusReason:             s.statusReason,
			Reopens:                  s.reopens,
			UnattributedLinesAdded:   s.lines.added.unassigned,
			UnattributedLinesRemoved: s.lines.removed.unassigned,
			DiscardedPoints:          s.discarded,
		},
	}
}

// fileRows 는 화면 쿼리와 같은 순서로 파일을 내보낸다 —
// ORDER BY lines_added+lines_removed DESC. 동률은 이름·해시로 갈라 결정론을 유지한다.
func (s *state) fileRows() []File {
	out := make([]File, 0, len(s.files))
	for _, f := range s.files {
		c := s.lines.assignedTo(f.hash)
		out = append(out, File{
			PathHash:     f.hash,
			Name:         f.name,
			Ext:          f.ext,
			LinesAdded:   c.added,
			LinesRemoved: c.removed,
			Edits:        f.edits,
			LastTS:       f.lastTS,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if x, y := a.LinesAdded+a.LinesRemoved, b.LinesAdded+b.LinesRemoved; x != y {
			return x > y
		}
		if a.Edits != b.Edits {
			return a.Edits > b.Edits
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.PathHash < b.PathHash
	})
	return out
}

func (s *state) mcpRows() []MCPUsage {
	out := make([]MCPUsage, 0, len(s.mcp))
	for _, m := range s.mcp {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServerName < out[j].ServerName })
	return out
}

// title 은 계획서의 3단계 폴백을 그대로 따른다.
func (s *state) title(files []File) (string, TitleSource, string) {
	if s.promptTitle != "" {
		return s.promptTitle, TitleFromPrompt, s.promptSummary
	}
	if t := filesTitle(files); t != "" {
		return t, TitleFromFiles, ""
	}
	return fallbackTitle(s.vendor), TitleFromFallback, ""
}
