package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

// PROJ-92 — 턴 분류 규칙.
//
// # 실패 메시지 규약
//
// 이 파일의 모든 단언은 실패할 때 TurnClass.Reason 을 함께 싣는다. 인수조건이
// "분류 근거를 테스트 실패 메시지에 확인 가능한 형태로 남긴다" 이므로, 분류가 어긋났을 때
// 무엇을 보고 그렇게 판정했는지 다시 캐낼 필요가 없어야 한다. classifyFail 이 그 규약을
// 한 곳에 모아 둔 것이고, 새 단언을 쓸 때도 이것을 쓴다.

// classifyOK 는 Success 필드에 넣을 포인터다. nil(모름)과 false(실패)를 구별해야 하므로
// 값 타입으로는 표현할 수 없다.
func classifyOK(b bool) *bool { return &b }

// classifyFail 은 분류 단언의 표준 실패 메시지다. 근거를 반드시 싣는다.
func classifyFail(t *testing.T, got TurnClass, want WorkType) {
	t.Helper()
	t.Fatalf("분류 = %s, want %s\n  근거: %s\n  전체: %s",
		got.WorkType, want, got.Reason, classifyEvidenceJSON(t, got))
}

func classifyEvidenceJSON(t *testing.T, got TurnClass) string {
	t.Helper()
	b, err := json.Marshal(got.Evidence)
	if err != nil {
		return "(근거 직렬화 실패: " + err.Error() + ")"
	}
	return string(b)
}

// classifyHasRule 은 근거 목록에 규칙이 들어 있는지 본다.
func classifyHasRule(got TurnClass, rule string) bool {
	for _, e := range got.Evidence {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

// classifyEditTool 은 파일을 하나 고친 편집 호출이다.
func classifyEditTool(path string) ToolSignal {
	return ToolSignal{
		ToolName: "Edit", Target: path, Success: classifyOK(true),
		Files: []FileSignal{{Operation: "modify", FilePath: path, Additions: 12, Deletions: 3}},
	}
}

// classifyShellTool 은 셸 명령 한 건이다.
func classifyShellTool(command string, success *bool) ToolSignal {
	return ToolSignal{ToolName: "Bash", Target: command, Success: success}
}

func TestClassifyTurnRules(t *testing.T) {
	tests := []struct {
		name string
		turn TurnSignals
		want WorkType
		// wantRule 은 근거 목록에 반드시 있어야 하는 규칙이다.
		wantRule string
	}{
		{
			name: "읽기 도구만 쓰면 탐색이다",
			turn: TurnSignals{Tools: []ToolSignal{
				{ToolName: "Read", Target: workspaceA + "/a.go", Success: classifyOK(true)},
				{ToolName: "Grep", Target: "", Success: classifyOK(true)},
			}},
			want: WorkTypeExploration, wantRule: RuleReadTool,
		},
		{
			name: "계획 도구도 탐색이다 — 코드를 바꾸지 않는다",
			turn: TurnSignals{Tools: []ToolSignal{
				{ToolName: "TodoWrite", Success: classifyOK(true)},
			}},
			want: WorkTypeExploration, wantRule: RulePlanTool,
		},
		{
			name: "MCP 도구는 이름을 몰라도 탐색으로 본다",
			turn: TurnSignals{Tools: []ToolSignal{
				{ToolName: "query-docs", MCPServer: "context7", Success: classifyOK(true)},
			}},
			want: WorkTypeExploration, wantRule: RuleMCPTool,
		},
		{
			name: "파일이 바뀌면 구현이다",
			turn: TurnSignals{Tools: []ToolSignal{classifyEditTool(workspaceA + "/apply.go")}},
			want: WorkTypeImplementation, wantRule: RuleFileChanged,
		},
		{
			name: "파일 변경 행이 없어도 편집 도구면 구현이다",
			turn: TurnSignals{Tools: []ToolSignal{
				{ToolName: "Write", Target: workspaceA + "/new.go", Success: classifyOK(true)},
			}},
			want: WorkTypeImplementation, wantRule: RuleWriteTool,
		},
		{
			name: "테스트 명령은 검증이다",
			turn: TurnSignals{Tools: []ToolSignal{
				classifyShellTool("go test ./internal/dashboard/", classifyOK(true)),
			}},
			want: WorkTypeVerification, wantRule: RuleCheckCommand,
		},
		{
			name: "빌드 명령도 검증이다",
			turn: TurnSignals{Tools: []ToolSignal{
				classifyShellTool("npm run build -- --mode production", classifyOK(true)),
			}},
			want: WorkTypeVerification, wantRule: RuleCheckCommand,
		},
		{
			name: "결과를 모르는 테스트 명령도 검증이다 — nil 은 실패가 아니다",
			turn: TurnSignals{Tools: []ToolSignal{classifyShellTool("pytest -q", nil)}},
			want: WorkTypeVerification, wantRule: RuleCheckCommand,
		},
		{
			name: "조회 명령은 탐색이다",
			turn: TurnSignals{Tools: []ToolSignal{
				classifyShellTool("git status --short", classifyOK(true)),
			}},
			want: WorkTypeExploration, wantRule: RuleReadCommand,
		},
		{
			name: "테스트가 실패하면 검증이 아니라 디버깅이다",
			turn: TurnSignals{Tools: []ToolSignal{
				classifyShellTool("go test ./...", classifyOK(false)),
			}},
			want: WorkTypeDebugging, wantRule: RuleToolFailed,
		},
		{
			name: "error_type 만 있어도 디버깅이다",
			turn: TurnSignals{Tools: []ToolSignal{
				{ToolName: "Read", Target: "/none", ErrorType: "file_not_found"},
			}},
			want: WorkTypeDebugging, wantRule: RuleToolError,
		},
		{
			name: "오류 이벤트는 도구가 성공해도 디버깅이다",
			turn: TurnSignals{
				Tools:      []ToolSignal{{ToolName: "Read", Success: classifyOK(true)}},
				EventNames: []string{"claude_code.api_error", "claude_code.user_prompt"},
			},
			want: WorkTypeDebugging, wantRule: RuleErrorEvent,
		},
		{
			name: "혼합 턴은 구현이 검증을 이긴다",
			turn: TurnSignals{Tools: []ToolSignal{
				classifyEditTool(workspaceA + "/apply.go"),
				classifyShellTool("go test ./...", classifyOK(true)),
			}},
			want: WorkTypeImplementation, wantRule: RuleCheckCommand,
		},
		{
			name: "혼합 턴은 디버깅이 구현을 이긴다",
			turn: TurnSignals{Tools: []ToolSignal{
				classifyEditTool(workspaceA + "/apply.go"),
				classifyShellTool("go test ./...", classifyOK(false)),
			}},
			want: WorkTypeDebugging, wantRule: RuleFileChanged,
		},
		{
			name: "검증은 탐색을 이긴다",
			turn: TurnSignals{Tools: []ToolSignal{
				{ToolName: "Read", Success: classifyOK(true)},
				classifyShellTool("make lint", classifyOK(true)),
			}},
			want: WorkTypeVerification, wantRule: RuleReadTool,
		},
		{
			name: "거부된 편집은 구현이 아니다 — 하지 않은 일이다",
			turn: TurnSignals{Tools: []ToolSignal{func() ToolSignal {
				tool := classifyEditTool(workspaceA + "/apply.go")
				tool.Decision = "reject"
				tool.Success = nil
				return tool
			}()}},
			want: WorkTypeUnknown, wantRule: RuleToolRejected,
		},
		{
			name: "모르는 도구는 추측하지 않고 unknown 이다",
			turn: TurnSignals{Tools: []ToolSignal{
				{ToolName: "QuantumFrobnicator", Success: classifyOK(true)},
			}},
			want: WorkTypeUnknown, wantRule: RuleUnknownTool,
		},
		{
			name: "모르는 명령도 unknown 이다",
			turn: TurnSignals{Tools: []ToolSignal{
				classifyShellTool("frobnicate --all", classifyOK(true)),
			}},
			want: WorkTypeUnknown, wantRule: RuleUnknownCommand,
		},
		{
			name: "명령이 비어 있는 셸 호출도 unknown 이다 — 디코더가 명령을 안 실어 주는 현재 상태",
			turn: TurnSignals{Tools: []ToolSignal{classifyShellTool("", classifyOK(true))}},
			want: WorkTypeUnknown, wantRule: RuleUnknownCommand,
		},
		{
			name: "모르는 도구는 아는 근거를 이기지 못한다",
			turn: TurnSignals{Tools: []ToolSignal{
				{ToolName: "QuantumFrobnicator", Success: classifyOK(true)},
				{ToolName: "Read", Success: classifyOK(true)},
			}},
			want: WorkTypeExploration, wantRule: RuleUnknownTool,
		},
		{
			name: "근거가 하나도 없으면 unknown 이고 그 사실을 남긴다",
			turn: TurnSignals{},
			want: WorkTypeUnknown, wantRule: RuleNoSignal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyTurn(tt.turn)
			if got.WorkType != tt.want {
				classifyFail(t, got, tt.want)
			}
			if !classifyHasRule(got, tt.wantRule) {
				t.Errorf("근거에 %q 가 없다\n  근거: %s", tt.wantRule, got.Reason)
			}
			if got.Reason == "" {
				t.Error("Reason 이 비었다 — 실패 메시지가 근거를 실을 수 없다")
			}
			if !strings.Contains(got.Reason, tt.wantRule) {
				t.Errorf("Reason 에 판정 규칙 %q 가 보이지 않는다: %s", tt.wantRule, got.Reason)
			}
		})
	}
}

// 같은 규칙이 여러 번 걸려도 근거 행은 늘지 않고 Count 만 는다.
// 행이 도구 호출 수만큼 늘면 JSON 이 감당하지 못하고, 잘라내면 판정 근거가 사라진다.
func TestClassifyTurnEvidenceCounts(t *testing.T) {
	got := ClassifyTurn(TurnSignals{Tools: []ToolSignal{
		{ToolName: "Read", Target: "/a", Success: classifyOK(true)},
		{ToolName: "Read", Target: "/b", Success: classifyOK(true)},
		{ToolName: "Grep", Target: "", Success: classifyOK(true)},
	}})
	if len(got.Evidence) != 1 {
		t.Fatalf("근거 행 = %d, want 1 — 규칙별로 한 줄이어야 한다\n  근거: %s",
			len(got.Evidence), got.Reason)
	}
	if got.Evidence[0].Count != 3 {
		t.Errorf("Count = %d, want 3\n  근거: %s", got.Evidence[0].Count, got.Reason)
	}
	// 처음 발동시킨 값이 Detail 에 남는다.
	if got.Evidence[0].Detail != "Read" {
		t.Errorf("Detail = %q, want Read\n  근거: %s", got.Evidence[0].Detail, got.Reason)
	}
	if !strings.Contains(got.Reason, "x3") {
		t.Errorf("Reason 이 횟수를 보여 주지 않는다: %s", got.Reason)
	}
}

// 오류 이벤트 판정은 벤더 접두를 무시한다. 벤더가 늘 때마다 규칙을 고치면 안 된다.
func TestIsErrorEventName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"claude_code.api_error", true},
		{"codex.api_error", true},
		{"error", true},
		{"vendor.tool_error", true},
		{"claude_code.tool_result", false},
		{"claude_code.user_prompt", false},
		{"", false},
		{"errorless", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isErrorEventName(tt.name); got != tt.want {
				t.Errorf("isErrorEventName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// 명령 토큰은 경로를 떼고 소문자로 비교한다. 부분 일치는 쓰지 않는다 —
// `cat foo_test.go` 가 테스트 실행으로 둔갑하면 안 된다.
func TestClassifyCommand(t *testing.T) {
	tests := []struct {
		command    string
		wantType   WorkType
		wantRule   string
		wantDetail string
	}{
		{"go test ./...", WorkTypeVerification, RuleCheckCommand, "test"},
		{"/usr/local/bin/go build ./...", WorkTypeVerification, RuleCheckCommand, "build"},
		{"CGO_ENABLED=0 go vet ./...", WorkTypeVerification, RuleCheckCommand, "vet"},
		{"cargo clippy -- -D warnings", WorkTypeVerification, RuleCheckCommand, "clippy"},
		{"./gradlew test", WorkTypeVerification, RuleCheckCommand, "test"},
		{"cat internal/dashboard/classify_test.go", WorkTypeExploration, RuleReadCommand, "cat"},
		{"git log --oneline -5", WorkTypeExploration, RuleReadCommand, "log"},
		{"rm -rf node_modules", WorkTypeUnknown, RuleUnknownCommand, "rm"},
		{"", WorkTypeUnknown, RuleUnknownCommand, ""},
		{"   ", WorkTypeUnknown, RuleUnknownCommand, ""},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			gotType, gotRule, gotDetail := classifyCommand(tt.command)
			if gotType != tt.wantType || gotRule != tt.wantRule || gotDetail != tt.wantDetail {
				t.Errorf("classifyCommand(%q) = (%s, %s, %q), want (%s, %s, %q)",
					tt.command, gotType, gotRule, gotDetail,
					tt.wantType, tt.wantRule, tt.wantDetail)
			}
		})
	}
}

// 우선순위는 이 티켓이 못박은 순서다. 순서가 바뀌면 혼합 턴의 답이 전부 달라진다.
func TestWorkTypePriorityOrder(t *testing.T) {
	want := []WorkType{
		WorkTypeDebugging, WorkTypeImplementation,
		WorkTypeVerification, WorkTypeExploration, WorkTypeUnknown,
	}
	got := WorkTypes()
	if len(got) != len(want) {
		t.Fatalf("유형 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("우선순위 %d = %s, want %s (전체 %v)", i, got[i], want[i], got)
		}
		if WorkTypeRank(want[i]) != i {
			t.Errorf("WorkTypeRank(%s) = %d, want %d", want[i], WorkTypeRank(want[i]), i)
		}
	}
	// 모르는 값은 맨 뒤다. 유형이 늘어도 정렬이 터지지 않는다.
	if WorkTypeRank("planning") <= WorkTypeRank(WorkTypeUnknown) {
		t.Error("표에 없는 유형이 unknown 보다 앞선다")
	}
	// WorkTypes 는 복사본이어야 한다 — 호출자가 고쳐도 우선순위가 흔들리면 안 된다.
	got[0] = "tampered"
	if WorkTypes()[0] != WorkTypeDebugging {
		t.Error("WorkTypes 가 내부 슬라이스를 그대로 내줬다")
	}
}

// 긴 명령이 근거 한 줄을 통째로 삼키지 않는다. 줄바꿈도 눕힌다.
func TestEvidenceDetailIsBounded(t *testing.T) {
	long := strings.Repeat("가", maxDetailRunes*2)
	got := ClassifyTurn(TurnSignals{Tools: []ToolSignal{
		{ToolName: "Read", ErrorType: long + "\n두 번째 줄"},
	}})
	detail := got.Evidence[0].Detail
	if n := len([]rune(detail)); n > maxDetailRunes+3 {
		t.Errorf("Detail 길이 = %d 룬, 상한 %d(+생략 기호)", n, maxDetailRunes)
	}
	if strings.ContainsAny(got.Reason, "\n\r") {
		t.Errorf("Reason 에 줄바꿈이 남았다: %q", got.Reason)
	}
}
