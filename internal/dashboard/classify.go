package dashboard

import (
	"strconv"
	"strings"
)

// 턴 단계·작업 유형의 간접 분류다 (PROJ-92). 이 파일은 규칙을, classify_phase.go 는
// 단계 묶기와 세션 유형을, classify_query.go 는 v3 에서 입력을 읽어 오는 일을 맡는다.
//
// # 왜 조회 시점인가
//
// ADR 0005 가 비워 둔 work_type·phase_json 자리는 ADR 0009 의 v3 전환에서 통째로
// 사라졌고, 이 티켓은 **v3 에 새 컬럼을 추가하지 않는다.** 그래서 분류는 저장 시점이
// 아니라 조회 시점에 계산한다. 대신 같은 입력이 항상 같은 답을 줘야 하므로 규칙은
// 난수·시계·맵 순회에 기대지 않는다 — 이 파일의 표가 전부 슬라이스인 이유다.
//
// # 무엇으로 판단하는가
//
// 관측 가능한 것만 쓴다.
//
//	tool_calls.tool_name         도구가 읽기인지 쓰기인지 셸인지
//	tool_calls.target            셸 도구의 명령어와 인자
//	tool_calls.success/error_type 테스트·빌드의 결과와 실패
//	tool_calls.decision          사용자가 거부한 호출
//	file_changes.*               실제로 파일이 바뀌었는지
//	events.event_name            오류 이벤트
//
// **turns.prompt_text 는 쓰지 않는다.** 원문은 프라이버시 게이트 대상이라(ADR 0003)
// 게이트가 닫힌 설치에서 분류가 통째로 달라진다. 화면이 설치마다 다른 답을 주면
// 지표로 쓸 수 없다.
//
// # 혼합 턴
//
// 한 턴이 여러 근거를 동시에 갖는 것이 정상이다 — 고치고, 테스트 돌리고, 실패한다.
// 그때는 **디버깅 → 구현 → 검증 → 탐색** 우선순위로 하나를 고른다. 고르지 못한 근거도
// 버리지 않고 Evidence 에 전부 남긴다. 분류가 틀렸을 때 "왜 그렇게 봤나" 를 실패 메시지
// 한 줄에서 읽을 수 있어야 하기 때문이다.

// WorkType 은 턴·단계·세션의 작업 유형이다. 문자열이 곧 TS 계약이다 (ADR 0004).
type WorkType string

const (
	// WorkTypeExploration 은 읽기·검색만 한 턴이다.
	WorkTypeExploration WorkType = "exploration"
	// WorkTypeImplementation 은 파일을 만들거나 고친 턴이다.
	WorkTypeImplementation WorkType = "implementation"
	// WorkTypeDebugging 은 실패·오류를 마주한 턴이다.
	WorkTypeDebugging WorkType = "debugging"
	// WorkTypeVerification 은 테스트·빌드·정적검사를 돌린 턴이다.
	WorkTypeVerification WorkType = "verification"
	// WorkTypeUnknown 은 근거가 없거나 모르는 도구·명령만 있는 턴이다. **안전한 fallback**
	// 이고, 모르는 것을 그럴듯한 값으로 채우지 않는다 — 추측한 유형은 틀려도 아무 데서도
	// 실패하지 않은 채 화면의 비율만 조용히 왜곡한다.
	WorkTypeUnknown WorkType = "unknown"
)

// workTypeOrder 는 혼합 턴의 우선순위이자 동률 처리 순서다: 디버깅 → 구현 → 검증 → 탐색.
// unknown 은 값이 아니라 "모른다" 라서 언제나 맨 뒤다.
//
// **슬라이스인 것이 핵심이다.** 맵으로 두면 순회 순서가 실행마다 달라져 동률에서 답이
// 흔들린다. 이 패키지의 결정론은 여기서 시작한다.
var workTypeOrder = []WorkType{
	WorkTypeDebugging,
	WorkTypeImplementation,
	WorkTypeVerification,
	WorkTypeExploration,
	WorkTypeUnknown,
}

// WorkTypeRank 는 우선순위 순번이다. 작을수록 우선한다. 모르는 값은 맨 뒤로 보낸다 —
// 나중에 유형이 늘어도 정렬이 터지지 않는다.
func WorkTypeRank(w WorkType) int {
	for i, k := range workTypeOrder {
		if k == w {
			return i
		}
	}
	return len(workTypeOrder)
}

// WorkTypes 는 우선순위 순서의 작업 유형 전체다. 화면의 범례가 이 순서를 그대로 쓴다.
func WorkTypes() []WorkType {
	out := make([]WorkType, len(workTypeOrder))
	copy(out, workTypeOrder)
	return out
}

// 분류 규칙 식별자. **문자열이 계약이다** — 테스트 실패 메시지와 GUI 툴팁이 이 값을
// 그대로 싣는다. 늘릴 수는 있어도 이름을 바꾸면 둘 다 조용히 의미를 잃는다.
const (
	RuleToolFailed     = "tool_failed"
	RuleToolError      = "tool_error_type"
	RuleErrorEvent     = "error_event"
	RuleFileChanged    = "file_changed"
	RuleWriteTool      = "write_tool"
	RuleReadTool       = "read_tool"
	RulePlanTool       = "plan_tool"
	RuleMCPTool        = "mcp_tool"
	RuleCheckCommand   = "test_or_build_command"
	RuleReadCommand    = "read_command"
	RuleToolRejected   = "tool_rejected"
	RuleUnknownTool    = "unknown_tool"
	RuleUnknownCommand = "unknown_command"
	RuleNoSignal       = "no_signal"
)

// decisionReject 는 사용자가 도구 호출을 거부했다는 tool_calls.decision 값이다.
// sessions.go 의 ToolRejects 집계가 쓰는 값과 같아야 한다.
const decisionReject = "reject"

// maxDetailRunes 는 근거 한 줄에 실을 값의 길이 상한이다. 셸 명령은 길이 제한이 없어서
// 그대로 실으면 JSON 한 덩어리가 화면을 넘긴다.
const maxDetailRunes = 120

// ── 입력 ────────────────────────────────────────────────────────────────────

// FileSignal 은 file_changes 한 행이다.
type FileSignal struct {
	// Operation 은 create|modify|delete|rename 이다 (v3 CHECK 제약).
	Operation string `json:"operation"`
	FilePath  string `json:"file_path"`
	Additions int64  `json:"additions"`
	Deletions int64  `json:"deletions"`
}

// ToolSignal 은 tool_calls 한 행과 거기 매달린 파일 변경이다.
type ToolSignal struct {
	ToolName string `json:"tool_name"`
	// Target 은 도구가 건드린 대상이다. 파일 도구에서는 원경로이고, 셸 도구에서는
	// 명령어와 인자다.
	Target string `json:"target"`
	// Success 가 nil 이면 결과를 모른다는 뜻이고 실패와 다르다 — 결정만 있고 결과가
	// 없는 호출이 그 경우다.
	Success   *bool  `json:"success"`
	ErrorType string `json:"error_type"`
	Decision  string `json:"decision"`
	MCPServer string `json:"mcp_server"`
	// Files 는 이 호출이 만든 파일 변경이다. 거부된 호출의 변경은 분류에 쓰지 않는다.
	Files []FileSignal `json:"files"`
}

// TurnSignals 는 턴 하나의 분류 입력이다. 순수 값이라 DB 없이 규칙을 검증할 수 있다.
type TurnSignals struct {
	TurnID    int64 `json:"turn_id"`
	TurnIndex int64 `json:"turn_index"`
	// StartedAt 은 turns.started_at(UTC 초)이다. 0 이면 모른다.
	StartedAt int64 `json:"started_at"`
	// EndedAt 은 turns.ended_at 이다. **현재 쓰기 경로가 이 컬럼을 채우지 않으므로**
	// 대개 0 이다 (store/resolve.go 의 upsertTurnSQL 에 ended_at 이 없다).
	// 길이 계산은 그 사실을 전제로 한다 — classify_phase.go 의 turnDurationSec 참조.
	EndedAt int64 `json:"ended_at"`
	// LastSeenAt 은 턴 안에서 관측된 마지막 활동 시각이다. ended_at 이 비어 있을 때의
	// 대체 끝점이다. 0 이면 모른다.
	LastSeenAt int64 `json:"last_seen_at"`

	Tools []ToolSignal `json:"tools"`
	// EventNames 는 이 턴에 붙은 events.event_name 의 중복 없는 목록이다.
	EventNames []string `json:"event_names"`
}

// ── 출력 ────────────────────────────────────────────────────────────────────

// Evidence 는 분류 근거 한 줄이다. 같은 규칙이 여러 번 걸리면 행이 늘지 않고 Count 만 는다 —
// 근거 목록의 길이가 도구 호출 수에 비례하면 JSON 이 감당하지 못하고, 잘라내는 순간
// "무엇이 판정했는가" 가 사라진다.
type Evidence struct {
	WorkType WorkType `json:"work_type"`
	// Rule 은 규칙 식별자다 (Rule* 상수).
	Rule string `json:"rule"`
	// Detail 은 이 규칙을 **처음** 발동시킨 값이다. 도구 이름·명령 토큰·파일 경로 따위다.
	Detail string `json:"detail"`
	Count  int    `json:"count"`
}

// String 은 실패 메시지에 그대로 싣는 한 조각이다.
func (e Evidence) String() string {
	s := string(e.WorkType) + ":" + e.Rule
	if e.Detail != "" {
		s += "(" + e.Detail + ")"
	}
	if e.Count > 1 {
		s += " x" + strconv.Itoa(e.Count)
	}
	return s
}

// TurnClass 는 턴 하나의 분류 결과다.
type TurnClass struct {
	TurnID    int64    `json:"turn_id"`
	TurnIndex int64    `json:"turn_index"`
	WorkType  WorkType `json:"work_type"`
	// StartedAt 은 turns.started_at(UTC 초)이다. 0 이면 모른다.
	StartedAt int64 `json:"started_at"`
	// DurationSec 는 턴 길이다. ClassifyTurns 가 채운다 — 이웃 턴을 봐야 정해지므로
	// 턴 하나만으로는 알 수 없다.
	DurationSec int64 `json:"duration_sec"`
	// Evidence 는 관측 순서 그대로의 근거다. 결정은 이 목록 전체에서 나온다.
	Evidence []Evidence `json:"evidence"`
	// Reason 은 Evidence 를 사람이 읽는 한 줄로 만든 것이다. 테스트 실패 메시지가
	// 이 문자열을 싣는다 — 분류가 어긋났을 때 근거를 다시 캐낼 필요가 없어야 한다.
	Reason string `json:"reason"`
}

// ── 도구 표 ─────────────────────────────────────────────────────────────────
//
// 표는 전부 슬라이스이고 비교는 대소문자를 무시한다. 벤더마다 표기가 다르다 —
// Claude Code 는 Bash, Codex 는 shell 이다. internal/session/kind.go 의 actionOf 와
// 같은 목록에서 출발했고, 거기 없는 벤더별 이름을 더했다.

// readTools 는 읽기·검색 도구다 → 탐색.
var readTools = []string{
	"Read", "ReadFile", "NotebookRead", "read_file", "view",
	"Glob", "Grep", "LS", "Search", "list_dir", "file_search", "grep_search",
	"codebase_search", "WebSearch", "WebFetch",
}

// writeTools 는 파일을 만들거나 고치는 도구다 → 구현.
var writeTools = []string{
	"Edit", "MultiEdit", "NotebookEdit", "edit_file", "search_replace",
	"str_replace_editor", "Write", "WriteFile", "Create", "CreateFile",
	"ApplyPatch", "apply_patch",
}

// shellTools 는 명령을 실행하는 도구다. 유형은 도구 이름이 아니라 **명령어**가 정한다 —
// Bash 하나로 테스트도 돌리고 로그도 본다.
var shellTools = []string{
	"Bash", "BashOutput", "KillShell", "Shell", "shell", "Execute",
	"run_terminal_cmd", "exec_command", "local_shell",
}

// planTools 는 계획·정리 도구다 → 탐색. 코드를 바꾸지 않으므로 구현이 아니고,
// 우선순위가 가장 낮아 다른 근거가 있으면 절대 이기지 않는다.
var planTools = []string{"TodoWrite", "ExitPlanMode", "AskUserQuestion", "update_plan"}

// hasToolName 은 대소문자를 무시한 목록 검사다. 표가 짧아 선형 탐색으로 충분하고,
// 맵을 쓰지 않으므로 순회 순서 문제가 아예 생기지 않는다.
func hasToolName(list []string, name string) bool {
	for _, s := range list {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}

// ── 명령어 표 ───────────────────────────────────────────────────────────────

// checkCommandTokens 는 테스트·빌드·정적검사 명령을 알아보는 토큰이다.
//
// **순서가 곧 우선순위다.** 먼저 걸린 토큰이 근거(Detail)로 남는다. 토큰은 정확히
// 일치할 때만 센다 — 부분 일치로 두면 `cat foo_test.go` 가 테스트 실행으로 둔갑한다.
var checkCommandTokens = []string{
	"test", "tests", "pytest", "jest", "vitest", "rspec", "phpunit", "ctest", "gotestsum",
	"build", "compile", "make", "cmake", "gradle", "gradlew", "mvn", "tsc", "webpack",
	"lint", "eslint", "golangci-lint", "ruff", "flake8", "pylint", "clippy", "vet",
	"typecheck", "mypy", "check", "gofmt", "prettier", "fmt", "format",
}

// readCommandTokens 는 상태를 바꾸지 않는 조회 명령이다 → 탐색.
// checkCommandTokens 다음에 본다 — `make lint` 는 검증이지 탐색이 아니다.
var readCommandTokens = []string{
	"ls", "cat", "head", "tail", "less", "wc", "tree", "stat", "file", "du", "df",
	"grep", "rg", "ag", "find", "fd", "which", "whereis", "man",
	"status", "log", "diff", "show", "blame", "ps", "top", "pwd", "env", "echo",
}

// ── 규칙 ────────────────────────────────────────────────────────────────────

// ClassifyTurn 은 턴 하나를 분류한다. DB 도 시계도 난수도 건드리지 않는다.
//
// DurationSec 는 채우지 않는다 — 턴 길이는 이웃 턴을 봐야 정해지므로 ClassifyTurns 의 몫이다.
func ClassifyTurn(t TurnSignals) TurnClass {
	var log evidenceLog
	for _, tool := range t.Tools {
		classifyToolSignal(tool, &log)
	}
	for _, name := range t.EventNames {
		if isErrorEventName(name) {
			log.add(WorkTypeDebugging, RuleErrorEvent, name)
		}
	}
	// 근거가 하나도 없는 턴도 이유를 남긴다. 빈 Evidence 를 돌려주면 "규칙이 없었다" 와
	// "규칙을 돌리지 않았다" 를 구별할 방법이 없다.
	if len(log.items) == 0 {
		log.add(WorkTypeUnknown, RuleNoSignal, "")
	}

	return TurnClass{
		TurnID:    t.TurnID,
		TurnIndex: t.TurnIndex,
		WorkType:  decideWorkType(log.items),
		Evidence:  log.items,
		Reason:    FormatEvidence(log.items),
	}
}

// classifyToolSignal 은 도구 호출 하나의 근거를 모은다.
func classifyToolSignal(tool ToolSignal, log *evidenceLog) {
	name := strings.TrimSpace(tool.ToolName)

	// 실패와 오류가 먼저다. 무엇을 하려던 호출이었든 실패했다는 사실이 그 턴을 디버깅으로
	// 만든다 — 테스트가 깨진 턴이 「검증」으로 분류되면 화면에서 그 순간을 찾을 수 없다.
	if tool.Success != nil && !*tool.Success {
		log.add(WorkTypeDebugging, RuleToolFailed, name)
	}
	if tool.ErrorType != "" {
		log.add(WorkTypeDebugging, RuleToolError, tool.ErrorType)
	}

	// 사용자가 거부한 호출은 실제로 아무 일도 하지 않았다. 근거는 남기되 유형은 주지
	// 않는다 — 거부된 편집을 「구현」으로 세면 하지 않은 일이 비율에 들어간다.
	if strings.EqualFold(strings.TrimSpace(tool.Decision), decisionReject) {
		log.add(WorkTypeUnknown, RuleToolRejected, name)
		return
	}

	for _, f := range tool.Files {
		log.add(WorkTypeImplementation, RuleFileChanged, fileChangeDetail(f))
	}

	switch {
	case hasToolName(shellTools, name):
		w, rule, detail := classifyCommand(tool.Target)
		log.add(w, rule, detail)
	case hasToolName(writeTools, name):
		log.add(WorkTypeImplementation, RuleWriteTool, name)
	case hasToolName(readTools, name):
		log.add(WorkTypeExploration, RuleReadTool, name)
	case hasToolName(planTools, name):
		log.add(WorkTypeExploration, RulePlanTool, name)
	case tool.MCPServer != "":
		// MCP 도구는 이름을 미리 알 수 없다. 대부분 조회이고, 탐색은 우선순위가 가장 낮아
		// 파일이 바뀌었거나 실패했다면 그쪽이 이긴다 — 틀려도 다른 근거를 덮지 않는다.
		log.add(WorkTypeExploration, RuleMCPTool, tool.MCPServer+"/"+name)
	default:
		// 모르는 도구다. 그럴듯한 값을 지어내지 않고 모른다고 적는다.
		log.add(WorkTypeUnknown, RuleUnknownTool, name)
	}
}

// classifyCommand 는 셸 명령어와 인자로 유형을 본다.
//
// 입력은 tool_calls.target 이다. **현재 디코더는 tool_input 에서 파일 경로만 뽑으므로
// (otlpdecode/target.go) 셸 도구의 target 은 대개 비어 있다.** 그때는 unknown_command 로
// 떨어지고, 같은 턴의 다른 근거(파일 변경·실패·오류 이벤트)가 그 턴을 대신 설명한다.
// 벤더나 디코더가 명령 문자열을 실어 주기 시작하면 그 순간부터 이 규칙이 동작한다.
func classifyCommand(command string) (WorkType, string, string) {
	tokens := commandTokens(command)
	if kw, ok := matchCommandToken(tokens, checkCommandTokens); ok {
		return WorkTypeVerification, RuleCheckCommand, kw
	}
	if kw, ok := matchCommandToken(tokens, readCommandTokens); ok {
		return WorkTypeExploration, RuleReadCommand, kw
	}
	if len(tokens) == 0 {
		return WorkTypeUnknown, RuleUnknownCommand, ""
	}
	return WorkTypeUnknown, RuleUnknownCommand, tokens[0]
}

// commandTokens 는 명령 문자열을 비교 가능한 토큰으로 자른다. 소문자로 낮추고 경로를
// 떼어 `/usr/bin/go` 와 `go` 가 같은 토큰이 되게 한다.
func commandTokens(command string) []string {
	fields := strings.Fields(strings.ToLower(command))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if i := strings.LastIndexAny(f, `/\`); i >= 0 {
			f = f[i+1:]
		}
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// matchCommandToken 은 표를 바깥 루프로 돈다. 명령 안의 토큰 순서가 아니라 **표의 순서**가
// 우선순위를 정하므로, 같은 명령이 언제나 같은 근거를 남긴다.
func matchCommandToken(tokens, table []string) (string, bool) {
	for _, want := range table {
		for _, tok := range tokens {
			if tok == want {
				return want, true
			}
		}
	}
	return "", false
}

// isErrorEventName 은 오류 이벤트인지 본다. 이름에는 벤더 접두가 붙어 오므로
// (claude_code.api_error) 마지막 조각만 본다 — internal/session/kind.go 와 같은 방식이다.
func isErrorEventName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndexByte(n, '.'); i >= 0 {
		n = n[i+1:]
	}
	return n == "error" || strings.HasSuffix(n, "_error")
}

// fileChangeDetail 은 파일 변경 한 건을 근거 한 줄로 줄인다. 전체 경로 대신 basename 을
// 쓰는 이유는 표 한 줄을 넘기지 않기 위해서다 (sessions.go 의 FileRow 와 같은 이유).
func fileChangeDetail(f FileSignal) string {
	op := strings.TrimSpace(f.Operation)
	if op == "" {
		op = "unknown"
	}
	name := baseName(f.FilePath)
	if name == "" {
		name = "?"
	}
	return op + " " + name + " +" + strconv.FormatInt(f.Additions, 10) +
		"/-" + strconv.FormatInt(f.Deletions, 10)
}

// decideWorkType 은 근거 목록에서 유형 하나를 고른다. 혼합 턴 우선순위가 여기 있다.
//
// unknown 근거는 유형을 주지 않는다 — 모르는 도구가 몇 개 있든 아는 근거 하나를 이기지
// 못한다. 아는 근거가 하나도 없으면 결과도 unknown 이다.
func decideWorkType(items []Evidence) WorkType {
	best := WorkTypeUnknown
	bestRank := WorkTypeRank(WorkTypeUnknown)
	for _, e := range items {
		if e.WorkType == WorkTypeUnknown {
			continue
		}
		if r := WorkTypeRank(e.WorkType); r < bestRank {
			best, bestRank = e.WorkType, r
		}
	}
	return best
}

// FormatEvidence 는 근거 목록을 한 줄로 만든다. 테스트 실패 메시지와 GUI 툴팁이 쓴다.
func FormatEvidence(items []Evidence) string {
	if len(items) == 0 {
		return string(WorkTypeUnknown) + ":" + RuleNoSignal
	}
	parts := make([]string, 0, len(items))
	for _, e := range items {
		parts = append(parts, e.String())
	}
	return strings.Join(parts, ", ")
}

// hasRuleName 은 규칙 이름 목록 검사다. 표가 짧아 선형 탐색으로 충분하다.
func hasRuleName(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

// evidenceLog 는 근거를 규칙별로 한 줄씩 모은다.
//
// 관측 순서를 그대로 유지하는 것이 의도다. 입력(SQL 결과)의 순서가 고정돼 있으므로
// 여기서 다시 정렬할 필요가 없고, 정렬을 얹지 않으면 "왜 이 순서인가" 를 설명할 규칙도
// 하나 줄어든다.
type evidenceLog struct{ items []Evidence }

func (l *evidenceLog) add(w WorkType, rule, detail string) {
	detail = trimEvidenceDetail(detail)
	for i := range l.items {
		if l.items[i].WorkType != w || l.items[i].Rule != rule {
			continue
		}
		l.items[i].Count++
		if l.items[i].Detail == "" {
			l.items[i].Detail = detail
		}
		return
	}
	l.items = append(l.items, Evidence{WorkType: w, Rule: rule, Detail: detail, Count: 1})
}

func trimEvidenceDetail(s string) string {
	s = strings.TrimSpace(s)
	// 줄바꿈은 근거 한 줄을 여러 줄로 만든다. 실패 메시지가 깨지지 않게 눕힌다.
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	r := []rune(s)
	if len(r) <= maxDetailRunes {
		return s
	}
	return string(r[:maxDetailRunes]) + "..."
}
