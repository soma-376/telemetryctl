package session

import (
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
)

// 턴 경계 규칙 전체를 한 표로 고정한다. 여기서 틀리면 turns · events.turn_id ·
// tool_calls.turn_id 가 동시에, 같은 방향으로 틀린다.
func TestTurnTrackerAssignsTurnKeys(t *testing.T) {
	const start = 1_700_000_000

	tests := []struct {
		name string
		// inputs 는 도착 순서다. want 는 각 입력의 turn_key 이고 "" 는 가상 턴이다.
		inputs []Input
		want   []string
	}{
		{
			name: "첫 프롬프트 앞의 이벤트는 가상 턴으로 간다",
			inputs: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Read")),
				logEv("s1", "claude_code.user_prompt", start+1, turnKey("p1")),
				logEv("s1", "claude_code.api_request", start+2),
			},
			want: []string{"", "p1", "p1"},
		},
		{
			name: "프롬프트마다 턴이 새로 열린다",
			inputs: []Input{
				logEv("s1", "claude_code.user_prompt", start, turnKey("p1")),
				logEv("s1", "claude_code.tool_result", start+1, tool("Edit")),
				logEv("s1", "claude_code.user_prompt", start+2, turnKey("p2")),
				logEv("s1", "claude_code.tool_result", start+3, tool("Edit")),
			},
			want: []string{"p1", "p1", "p2", "p2"},
		},
		{
			name: "세션 수준 시그널은 열린 턴이 있어도 가상 턴으로 간다",
			inputs: []Input{
				logEv("s1", "claude_code.user_prompt", start, turnKey("p1")),
				metricEv("s1", "claude_code.session.count", start+1, 1),
				metricEv("s1", "claude_code.active_time.total", start+2, 30),
				logEv("s1", "claude_code.mcp.connection", start+3, mcp("github")),
				logEv("s1", "claude_code.api_request", start+4),
			},
			want: []string{"p1", "", "", "", "p1"},
		},
		{
			name: "누적 데이터포인트는 턴에 귀속시키지 않는다",
			inputs: []Input{
				logEv("s1", "claude_code.user_prompt", start, turnKey("p1")),
				metricEv("s1", "claude_code.token.usage", start+1, 100,
					temporality(event.TemporalityCumulative)),
				metricEv("s1", "claude_code.token.usage", start+2, 100,
					temporality(event.TemporalityDelta)),
			},
			want: []string{"p1", "", "p1"},
		},
		{
			name: "세션이 다르면 턴도 갈린다",
			inputs: []Input{
				logEv("s1", "claude_code.user_prompt", start, turnKey("p1")),
				logEv("s2", "claude_code.user_prompt", start+1, turnKey("p2")),
				logEv("s1", "claude_code.api_request", start+2),
				logEv("s2", "claude_code.api_request", start+3),
			},
			want: []string{"p1", "p2", "p1", "p2"},
		},
		{
			name: "벤더가 턴을 직접 알려 주면 추론보다 우선한다",
			inputs: []Input{
				logEv("s1", "claude_code.user_prompt", start, turnKey("p1")),
				logEv("s1", "claude_code.tool_result", start+1, tool("Edit"), turnKey("p9")),
				logEv("s1", "claude_code.api_request", start+2),
			},
			want: []string{"p1", "p9", "p9"},
		},
		{
			name: "session.id 가 없으면 귀속할 세션이 없다",
			inputs: []Input{
				logEv("", "claude_code.api_request", start),
			},
			want: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := NewTurnTracker()
			for i, in := range tt.inputs {
				if got := tr.Assign(in).Key; got != tt.want[i] {
					t.Errorf("[%d] %s: turn_key = %q, want %q", i, in.Event.Name, got, tt.want[i])
				}
			}
		})
	}
}

// 벤더가 prompt.id 를 안 주면 프롬프트 이벤트의 DedupKey 가 턴 키다.
// 세션 안에서 유일하고 같은 프롬프트가 재전송돼도 같은 값이라 턴이 갈라지지 않는다.
func TestTurnKeyFallsBackToPromptDedupKey(t *testing.T) {
	const start = 1_700_000_000
	in := logEv("s1", "claude_code.user_prompt", start)

	tr := NewTurnTracker()
	got := tr.Assign(in).Key
	if got != in.Event.DedupKey() {
		t.Fatalf("turn_key = %q, want DedupKey %q", got, in.Event.DedupKey())
	}
	if len(got) != 64 {
		t.Fatalf("turn_key 길이 = %d — sha256 hex 가 아니다", len(got))
	}

	// 재전송된 같은 프롬프트는 같은 턴이다.
	if again := NewTurnTracker().Assign(in).Key; again != got {
		t.Fatalf("같은 프롬프트가 다른 턴 키를 얻었다: %q vs %q", again, got)
	}
}

func TestTurnPromptTextComesFromPromptContent(t *testing.T) {
	const start = 1_700_000_000
	tr := NewTurnTracker()

	got := tr.Assign(logEv("s1", "claude_code.user_prompt", start,
		turnKey("p1"), prompt("턴 경계를 붙여줘")))
	if got.PromptText != "턴 경계를 붙여줘" {
		t.Errorf("prompt_text = %q", got.PromptText)
	}
	// 프롬프트가 아닌 이벤트에는 붙지 않는다.
	next := tr.Assign(logEv("s1", "claude_code.tool_result", start+1, tool("Edit")))
	if next.PromptText != "" {
		t.Errorf("도구 이벤트에 prompt_text 가 붙었다: %q", next.PromptText)
	}
}

// call_key 는 전역 UNIQUE 다. 벤더 값이 있으면 벤더 접두를 붙여 그대로 쓴다.
func TestCallKeyUsesVendorIDWithPrefix(t *testing.T) {
	const start = 1_700_000_000
	tr := NewTurnTracker()
	tr.Assign(logEv("s1", "claude_code.user_prompt", start, turnKey("p1")))

	got := tr.Assign(logEv("s1", "claude_code.tool_result", start+1,
		tool("Edit"), callKey("toolu_01AB")))
	if got.CallKey != "claude_code:toolu_01AB" {
		t.Fatalf("call_key = %q, want claude_code:toolu_01AB", got.CallKey)
	}

	// 벤더가 다르면 같은 벤더 ID 라도 다른 호출이다.
	other := tr.Assign(logEv("s2", "codex.tool_result", start+2,
		tool("Edit"), callKey("toolu_01AB"), vendor("codex")))
	if other.CallKey == got.CallKey {
		t.Fatal("서로 다른 벤더의 같은 호출 ID 가 한 키로 접혔다 — call_key 는 전역 UNIQUE 다")
	}
}

// 결정 이벤트와 결과 이벤트가 같은 호출로 합쳐져야 tool_calls 가 한 행이 된다.
func TestSynthesizedCallKeyPairsDecisionWithResult(t *testing.T) {
	const start = 1_700_000_000
	tr := NewTurnTracker()
	tr.Assign(logEv("s1", "claude_code.user_prompt", start, turnKey("p1")))

	decision := tr.Assign(logEv("s1", "claude_code.tool_decision", start+1,
		tool("Edit"), decide("accept")))
	result := tr.Assign(logEv("s1", "claude_code.tool_result", start+2,
		tool("Edit"), success(true)))

	if decision.CallKey == "" || decision.CallKey != result.CallKey {
		t.Fatalf("결정·결과가 다른 호출이 됐다: %q vs %q", decision.CallKey, result.CallKey)
	}
	if !strings.HasPrefix(decision.CallKey, "claude_code:") {
		t.Errorf("합성 키에 벤더 접두가 없다: %q", decision.CallKey)
	}
}

// 같은 턴에서 같은 툴을 연달아 부르면 서로 다른 호출이어야 한다.
// 여기서 접히면 tool_calls 가 호출 하나를 통째로 잃는다.
func TestSynthesizedCallKeysDifferPerCall(t *testing.T) {
	const start = 1_700_000_000
	tr := NewTurnTracker()
	tr.Assign(logEv("s1", "claude_code.user_prompt", start, turnKey("p1")))

	// 결정 두 건이 먼저 오고 결과 두 건이 뒤따르는 순서다.
	d1 := tr.Assign(logEv("s1", "claude_code.tool_decision", start+1, tool("Edit"), decide("accept")))
	d2 := tr.Assign(logEv("s1", "claude_code.tool_decision", start+1, tool("Edit"), decide("accept")))
	r1 := tr.Assign(logEv("s1", "claude_code.tool_result", start+2, tool("Edit"), success(true)))
	r2 := tr.Assign(logEv("s1", "claude_code.tool_result", start+2, tool("Edit"), success(true)))

	if d1.CallKey == d2.CallKey {
		t.Fatal("같은 초의 두 결정이 한 호출로 접혔다")
	}
	if d1.CallKey != r1.CallKey || d2.CallKey != r2.CallKey {
		t.Fatalf("결정·결과 짝이 어긋났다: %q/%q vs %q/%q", d1.CallKey, r1.CallKey, d2.CallKey, r2.CallKey)
	}
}

// 자동 승인된 호출은 결정 이벤트가 없다. 결과만 와도 호출 키가 나와야 한다.
func TestResultWithoutDecisionOpensNewCall(t *testing.T) {
	const start = 1_700_000_000
	tr := NewTurnTracker()
	tr.Assign(logEv("s1", "claude_code.user_prompt", start, turnKey("p1")))

	r1 := tr.Assign(logEv("s1", "claude_code.tool_result", start+1, tool("Bash"), success(true)))
	r2 := tr.Assign(logEv("s1", "claude_code.tool_result", start+1, tool("Bash"), success(true)))
	if r1.CallKey == "" || r1.CallKey == r2.CallKey {
		t.Fatalf("자동 승인 호출이 갈리지 않았다: %q vs %q", r1.CallKey, r2.CallKey)
	}
}

// 턴이 다르면 같은 툴의 같은 순번도 다른 호출이다.
func TestCallKeyIncludesTurn(t *testing.T) {
	const start = 1_700_000_000
	tr := NewTurnTracker()

	tr.Assign(logEv("s1", "claude_code.user_prompt", start, turnKey("p1")))
	first := tr.Assign(logEv("s1", "claude_code.tool_result", start+1, tool("Edit"), success(true)))
	tr.Assign(logEv("s1", "claude_code.user_prompt", start+2, turnKey("p2")))
	second := tr.Assign(logEv("s1", "claude_code.tool_result", start+3, tool("Edit"), success(true)))

	if first.CallKey == second.CallKey {
		t.Fatal("턴이 달라도 같은 call_key 가 나왔다")
	}
}

// 도구 이벤트가 아니면 call_key 를 만들지 않는다 — tool_calls 승격 대상이 아니다.
func TestNonToolEventsHaveNoCallKey(t *testing.T) {
	const start = 1_700_000_000
	tr := NewTurnTracker()
	tr.Assign(logEv("s1", "claude_code.user_prompt", start, turnKey("p1")))

	for _, name := range []string{"claude_code.api_request", "claude_code.api_error", "claude_code.user_prompt"} {
		if got := tr.Assign(logEv("s1", name, start+1)).CallKey; got != "" {
			t.Errorf("%s 에 call_key 가 붙었다: %q", name, got)
		}
	}
}

func TestFileChangeOf(t *testing.T) {
	const start = 1_700_000_000

	tests := []struct {
		name    string
		in      Input
		wantOK  bool
		wantOp  string
		wantPth string
	}{
		{
			name:    "편집은 modify 다",
			in:      logEv("s1", "claude_code.tool_result", start, tool("Edit"), target(fixtureRepoFile), success(true)),
			wantOK:  true,
			wantOp:  OperationModify,
			wantPth: fixtureRepoFile,
		},
		{
			name:    "쓰기는 create 다",
			in:      logEv("s1", "claude_code.tool_result", start, tool("Write"), target(fixtureRepoFile), success(true)),
			wantOK:  true,
			wantOp:  OperationCreate,
			wantPth: fixtureRepoFile,
		},
		{
			name: "읽기는 파일을 바꾸지 않는다",
			in:   logEv("s1", "claude_code.tool_result", start, tool("Read"), target(fixtureRepoFile), success(true)),
		},
		{
			name: "실패한 편집은 파일을 바꾸지 않았다",
			in:   logEv("s1", "claude_code.tool_result", start, tool("Edit"), target(fixtureRepoFile), success(false)),
		},
		{
			name: "대상 원경로가 없으면 행을 만들 수 없다 (file_path 는 NOT NULL)",
			in:   logEv("s1", "claude_code.tool_result", start, tool("Edit"), success(true)),
		},
		{
			// 게이트가 꺼진 설치에서는 success 가 오지 않는다. 미상은 실패가 아니다.
			name:    "성공 여부 미상은 반영한다",
			in:      logEv("s1", "claude_code.tool_result", start, tool("Edit"), target(fixtureRepoFile)),
			wantOK:  true,
			wantOp:  OperationModify,
			wantPth: fixtureRepoFile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FileChangeOf(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (%+v)", ok, tt.wantOK, got)
			}
			if !ok {
				return
			}
			if got.Operation != tt.wantOp || got.Path != tt.wantPth {
				t.Fatalf("FileChange = %+v, want op=%q path=%q", got, tt.wantOp, tt.wantPth)
			}
			// 줄 수는 관측하지 않는다 — 0 이 아니라 NULL 이어야 한다.
			if got.Additions.Valid() || got.Deletions.Valid() {
				t.Errorf("관측하지 않은 줄 수가 설정됐다: %+v", got)
			}
		})
	}
}

// Prune 이 세션을 지우면 턴 상태도 같이 사라져야 한다. 안 그러면 데몬 수명 내내 맵이 자란다.
func TestTurnTrackerForgetsPrunedSessions(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	in := logEv("s1", "claude_code.user_prompt", start, turnKey("p1"))
	a.Add(in)
	a.TurnOf(in)
	a.Advance(start + 3600)

	if n := a.Prune(start + 7200); n != 1 {
		t.Fatalf("Prune = %d, want 1", n)
	}
	if _, ok := a.turns.sessions["s1"]; ok {
		t.Fatal("세션이 정리됐는데 턴 상태가 남았다")
	}
}
