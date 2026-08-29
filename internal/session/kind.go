package session

import "strings"

// kind 는 조립기가 알아보는 이벤트 종류다. 이벤트 이름의 벤더 접두를 떼고 판별한다 —
// claude_code.token.usage 와 codex.token.usage 가 같은 kind 로 떨어져야 벤더가 늘어도
// 조립기를 고치지 않는다. 표에 없는 이름은 kindOther 로, 세션의 시각만 갱신한다.
type kind uint8

const (
	kindOther kind = iota
	kindTokenUsage
	kindCostUsage
	kindLinesOfCode
	kindActiveTime
	kindUserPrompt
	kindToolResult
	kindToolDecision
	kindAPIRequest
	kindAPIError
	kindMCPConnection
	kindSessionCount
)

var kindBySuffix = map[string]kind{
	"token.usage":         kindTokenUsage,
	"cost.usage":          kindCostUsage,
	"lines_of_code.count": kindLinesOfCode,
	"active_time.total":   kindActiveTime,
	"user_prompt":         kindUserPrompt,
	"tool_result":         kindToolResult,
	"tool_decision":       kindToolDecision,
	"api_request":         kindAPIRequest,
	"api_error":           kindAPIError,
	"mcp.connection":      kindMCPConnection,
	"mcp_connection":      kindMCPConnection,
	"session.count":       kindSessionCount,
}

func classify(name string) kind {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		if k, ok := kindBySuffix[name[i+1:]]; ok {
			return k
		}
	}
	if k, ok := kindBySuffix[name]; ok {
		return k
	}
	return kindOther
}

// actionOf 는 tool_events.action 을 툴 이름에서 파생한다. 모르는 툴은 빈 값이다 —
// 추측해서 채우면 화면의 아이콘이 조용히 틀린다.
func actionOf(tool string) Action {
	switch tool {
	case "Read", "NotebookRead", "ReadFile":
		return ActionRead
	case "Edit", "MultiEdit", "NotebookEdit", "ApplyPatch", "apply_patch":
		return ActionEdit
	case "Write", "Create", "CreateFile", "WriteFile":
		return ActionWrite
	case "Bash", "BashOutput", "KillShell", "Shell", "shell", "Execute":
		return ActionRun
	case "Grep", "Glob", "LS", "Search", "WebSearch", "WebFetch":
		return ActionSearch
	}
	return ""
}

// touchesFile 은 session_files 에 집계할 툴인지 본다. 읽기는 "파일 변경" 화면의 대상이
// 아니므로 편집·쓰기만 해당한다.
func touchesFile(a Action) bool { return a == ActionEdit || a == ActionWrite }
