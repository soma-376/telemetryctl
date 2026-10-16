package dashboard

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// PROJ-92 — v3 를 실제로 읽어 분류한다.
//
// store 로 쓰고 dashboard 로 읽는 왕복이다 (helper_test.go 의 newFixture). 규칙을 흉내 낸
// 입력으로만 검증하면 "분류기가 v3 스키마를 잘못 읽는다" 는 부류의 버그가 통째로 빠져나간다.

// classifyBase 는 이 파일의 고정 fixture 기준 시각이다. 벽시계를 읽지 않는다.
var classifyBase = testNow.Add(-2 * time.Hour)

// classifyAt 은 fixture 안의 상대 시각이다.
func classifyAt(sec int) time.Time { return classifyBase.Add(time.Duration(sec) * time.Second) }

// classifyFixture 는 네 턴짜리 세션 하나를 쓴다.
//
//	턴0 탐색   Read
//	턴1 구현   Edit + file_changes (도구가 240초에 걸쳐 두 번)
//	턴2 검증   Bash `go test ./...` 성공
//	턴3 디버깅 Bash `go test ./...` 실패
//
// 마지막으로 턴에 붙지 않는 세션 수준 이벤트를 하나 더 쓴다 — 가상 턴이 분류에 섞이면
// 오류 이벤트 하나로 세션 전체가 디버깅이 된다.
func classifyFixture(t *testing.T) (*fixture, int64) {
	t.Helper()
	f := newFixture(t)

	const key = "s-cls"
	f.write(store.Batch{
		Sessions: []session.Session{newSession(key, classifyAt(0))},
		Events: []store.EventRecord{
			promptRecord(key, "turn-0", classifyAt(0), 1, "이 저장소의 구조를 알려줘"),
			toolRecord(key, "turn-0", "call-0", classifyAt(60), 2, toolSpec{
				ToolName: "Read", Target: workspaceA + "/AGENTS.md", Success: event.Some(true),
			}),

			promptRecord(key, "turn-1", classifyAt(300), 3, "분류기를 붙여줘"),
			toolRecord(key, "turn-1", "call-1", classifyAt(360), 4, toolSpec{
				ToolName: "Edit", Target: workspaceA + "/classify.go", Success: event.Some(true),
				File: fileChange(workspaceA+"/classify.go", 120, 4),
			}),
			toolRecord(key, "turn-1", "call-2", classifyAt(540), 5, toolSpec{
				ToolName: "Edit", Target: workspaceA + "/classify_phase.go", Success: event.Some(true),
				File: fileChange(workspaceA+"/classify_phase.go", 60, 0),
			}),

			promptRecord(key, "turn-2", classifyAt(600), 6, "테스트 돌려줘"),
			toolRecord(key, "turn-2", "call-3", classifyAt(660), 7, toolSpec{
				ToolName: "Bash", Target: "go test ./internal/dashboard/", Success: event.Some(true),
			}),

			promptRecord(key, "turn-3", classifyAt(900), 8, "다시 돌려줘"),
			toolRecord(key, "turn-3", "call-4", classifyAt(960), 9, toolSpec{
				ToolName: "Bash", Target: "go test ./...", Success: event.Some(false),
				ErrorType: "exit_status_1",
			}),

			// 턴에 붙지 않는 세션 수준 이벤트. 가상 턴(turn_index IS NULL)으로 들어간다.
			{Event: baseEvent(vendorClaude, key, "claude_code.api_error", classifyAt(1200), 10)},
		},
	})
	return f, f.sessionID(vendorClaude, key)
}

// classifyDump 는 실패 메시지에 싣는 분류 전문이다. 조회에서 어긋났을 때 어떤 턴이 어떤
// 근거로 그렇게 분류됐는지 여기서 바로 보인다.
func classifyDump(t *testing.T, got SessionClassification) string {
	t.Helper()
	b, err := json.MarshalIndent(got, "  ", "  ")
	if err != nil {
		return "(분류 직렬화 실패: " + err.Error() + ")"
	}
	return string(b)
}

func TestClassifierSession(t *testing.T) {
	f, id := classifyFixture(t)
	got, err := NewClassifier(f.reader).Session(context.Background(), id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	if got.SessionID != id {
		t.Errorf("SessionID = %d, want %d", got.SessionID, id)
	}
	// 가상 턴은 세지 않는다. 네 개여야 한다.
	if got.TurnCount != 4 {
		t.Fatalf("턴 = %d, want 4 — 가상 턴이 섞였을 수 있다\n%s",
			got.TurnCount, classifyDump(t, got))
	}

	wantTurns := []WorkType{
		WorkTypeExploration, WorkTypeImplementation,
		WorkTypeVerification, WorkTypeDebugging,
	}
	for i, want := range wantTurns {
		turn := got.Turns[i]
		if turn.WorkType != want {
			t.Errorf("턴 %d 분류 = %s, want %s\n  근거: %s", i, turn.WorkType, want, turn.Reason)
		}
		if turn.Reason == "" {
			t.Errorf("턴 %d 의 Reason 이 비었다", i)
		}
	}

	// 각 턴이 연속으로 다른 유형이라 단계도 넷이다.
	if len(got.Phases) != 4 {
		t.Fatalf("단계 = %d, want 4\n%s", len(got.Phases), classifyDump(t, got))
	}
	var sum int64
	for _, p := range got.Phases {
		sum += p.SharePermille
	}
	if sum != 1000 {
		t.Errorf("단계 비율 합 = %d, want 1000\n%s", sum, classifyDump(t, got))
	}

	// 구현 턴이 가장 길다 (도구가 240초에 걸쳐 있다). 세션 유형은 누적 시간이 가장 긴 분류다.
	if got.WorkType != WorkTypeImplementation {
		t.Errorf("세션 유형 = %s, want implementation\n  근거: %s\n%s",
			got.WorkType, got.WorkTypeReason, classifyDump(t, got))
	}
	if !got.DurationKnown || got.TotalDurationSec <= 0 {
		t.Errorf("세션 길이를 못 읽었다 (%d초, known=%v)\n%s",
			got.TotalDurationSec, got.DurationKnown, classifyDump(t, got))
	}

	// 파일 변경이 근거로 남아야 한다 — 구현 판정의 출처다.
	if !classifyHasRule(got.Turns[1], RuleFileChanged) {
		t.Errorf("구현 턴의 근거에 %s 가 없다: %s", RuleFileChanged, got.Turns[1].Reason)
	}
	// 셸 명령이 근거로 남아야 한다 — target 에 실려 온 명령어를 읽었다는 증거다.
	if !classifyHasRule(got.Turns[2], RuleCheckCommand) {
		t.Errorf("검증 턴의 근거에 %s 가 없다: %s", RuleCheckCommand, got.Turns[2].Reason)
	}
	// 실패한 도구가 검증을 디버깅으로 뒤집었다는 사실도 근거에 있어야 한다.
	if !classifyHasRule(got.Turns[3], RuleToolFailed) {
		t.Errorf("디버깅 턴의 근거에 %s 가 없다: %s", RuleToolFailed, got.Turns[3].Reason)
	}
}

// 인수조건: 고정 fixture 에서 분류·단계 비율·세션 유형이 항상 같다.
// 같은 DB 를 반복해서 조회해도 결과가 바이트 단위로 같아야 한다.
func TestClassifierSessionIsDeterministic(t *testing.T) {
	f, id := classifyFixture(t)
	c := NewClassifier(f.reader)
	ctx := context.Background()

	want, err := c.Session(ctx, id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	for i := range 20 {
		got, gerr := c.Session(ctx, id)
		if gerr != nil {
			t.Fatalf("%d회차 Session: %v", i, gerr)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%d회차 결과가 다르다\n  got:  %s\n  want: %s",
				i, classifyDump(t, got), classifyDump(t, want))
		}
		gotJSON, merr := json.Marshal(got)
		if merr != nil {
			t.Fatalf("Marshal: %v", merr)
		}
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("%d회차 JSON 이 다르다\n  got:  %s\n  want: %s", i, gotJSON, wantJSON)
		}
	}
}

// 여러 세션을 한 번에 분류하면 결과는 인자 순서 그대로다. 맵으로 돌려주면 호출자가
// 순회할 때 순서가 실행마다 달라져 화면의 목록이 흔들린다.
func TestClassifierSessionsKeepsArgumentOrder(t *testing.T) {
	f := newFixture(t)
	keys := []string{"s-a", "s-b", "s-c"}
	for i, key := range keys {
		at := classifyAt(i * 600)
		f.write(store.Batch{
			Sessions: []session.Session{newSession(key, at)},
			Events: []store.EventRecord{
				promptRecord(key, key+"-turn", at, 100+i, "프롬프트"),
				toolRecord(key, key+"-turn", key+"-call", at.Add(30*time.Second), 200+i, toolSpec{
					ToolName: "Read", Target: workspaceA + "/a.go", Success: event.Some(true),
				}),
			},
		})
	}

	ids := []int64{
		f.sessionID(vendorClaude, "s-c"),
		f.sessionID(vendorClaude, "s-a"),
		f.sessionID(vendorClaude, "s-b"),
		// 없는 세션도 자리를 차지한다 — 걸러 내면 호출자의 인덱스가 어긋난다.
		999999,
		// 중복 id 는 각각 한 행이다.
		f.sessionID(vendorClaude, "s-a"),
	}
	got, err := NewClassifier(f.reader).Sessions(context.Background(), ids)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("결과 = %d행, want %d", len(got), len(ids))
	}
	for i, id := range ids {
		if got[i].SessionID != id {
			t.Errorf("%d행 SessionID = %d, want %d", i, got[i].SessionID, id)
		}
	}
	if got[3].WorkType != WorkTypeUnknown || got[3].TurnCount != 0 {
		t.Errorf("없는 세션이 빈 분류가 아니다: %+v", got[3])
	}
	if !reflect.DeepEqual(got[1], got[4]) {
		t.Error("같은 id 가 다른 분류를 줬다")
	}
}

// DB 가 없어도 에러가 아니다 (ADR 0004). 분류는 빈 결과다.
func TestClassifierWithoutDatabase(t *testing.T) {
	r, err := Open(store.PathIn(t.TempDir()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { r.Close() }) //nolint:errcheck // 테스트 정리
	if r.Available() {
		t.Fatal("파일이 없는데 Available = true")
	}

	ctx := context.Background()
	for name, c := range map[string]*Classifier{
		"DB 없는 Reader": NewClassifier(r),
		"nil Reader":   NewClassifier(nil),
	} {
		t.Run(name, func(t *testing.T) {
			one, oerr := c.Session(ctx, 1)
			if oerr != nil {
				t.Fatalf("Session: %v", oerr)
			}
			if one.WorkType != WorkTypeUnknown || one.TurnCount != 0 {
				t.Errorf("빈 분류가 아니다: %+v", one)
			}
			if one.Turns == nil || one.Phases == nil || one.Shares == nil {
				t.Errorf("슬라이스가 nil 이다 — JSON 에서 null 이 된다: %+v", one)
			}

			many, merr := c.Sessions(ctx, []int64{1, 2})
			if merr != nil {
				t.Fatalf("Sessions: %v", merr)
			}
			if len(many) != 2 {
				t.Errorf("결과 = %d행, want 2", len(many))
			}
		})
	}
}

// 공개 응답 타입의 json 태그는 전부 snake_case 다 (ADR 0004). 태그가 곧 TS 필드명이다.
func TestClassifyTypesUseSnakeCaseTags(t *testing.T) {
	for _, v := range []any{
		SessionClassification{}, Phase{}, WorkTypeShare{},
		TurnClass{}, Evidence{}, TurnSignals{}, ToolSignal{}, FileSignal{},
	} {
		assertSnakeCaseTags(t, v)
	}
}

// 데몬이 쓰는 동안 분류를 읽어도 안전해야 한다 (-race).
func TestClassifierConcurrentWithWrites(t *testing.T) {
	f, id := classifyFixture(t)
	c := NewClassifier(f.reader)
	ctx := context.Background()

	var wg sync.WaitGroup
	writeDone := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(writeDone)
		for i := range 20 {
			at := classifyAt(2000 + i*60)
			key := "s-cls"
			if _, err := f.db.Write(ctx, store.Batch{
				Sessions: []session.Session{newSession(key, classifyAt(0))},
				Events: []store.EventRecord{
					toolRecord(key, "turn-3", "late-call-"+string(rune('a'+i)), at, 3000+i, toolSpec{
						ToolName: "Read", Target: workspaceA + "/a.go", Success: event.Some(true),
					}),
				},
			}); err != nil {
				t.Errorf("Write %d: %v", i, err)
				return
			}
		}
	}()

	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-writeDone:
					return
				default:
				}
				if _, err := c.Session(ctx, id); err != nil {
					t.Errorf("Session: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
