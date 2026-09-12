package store

import (
	"context"
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
)

func TestLifecycleWorkspacePreservation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := event.SecFromTime(baseTime)
	for _, step := range []struct {
		name string
		path string
		end  bool
		want any
	}{
		{"경로 없는 최초 시작", "", false, nil},
		{"시작 경로 저장", "/projects/한글 repo", false, "/projects/한글 repo"},
		{"경로 없는 재개는 보존", "", false, "/projects/한글 repo"},
		{"종료 경로는 무시", "/another/path", true, "/projects/한글 repo"},
		{"새 경로로 재개", "/projects/worktree", false, "/projects/worktree"},
	} {
		t.Run(step.name, func(t *testing.T) {
			if err := db.ApplyLifecycle(ctx, "claude_code", "workspace-hook", at, step.end, step.path); err != nil {
				t.Fatal(err)
			}
			if got := scanOne(t, db, `SELECT workspace_path FROM sessions WHERE session_key='workspace-hook'`); got != step.want {
				t.Fatalf("workspace=%v, want %v", got, step.want)
			}
		})
		at++
	}
	// 뒤늦게 도착한 경로 없는 OTel 스냅샷이 훅에서 확보한 경로를 지우지 않는다.
	s := newSession("workspace-hook", baseTime)
	mustWrite(t, db, Batch{Sessions: []session.Session{s}})
	if got := scanOne(t, db, `SELECT workspace_path FROM sessions WHERE session_key='workspace-hook'`); got != "/projects/worktree" {
		t.Fatalf("스냅샷 저장 후 workspace=%v", got)
	}
}

func TestEventSeedDoesNotWriteWorkspace(t *testing.T) {
	db := openTestDB(t)
	r := evrec("claude_code.user_prompt", baseTime, 1, sess("workspace-event"))
	r.Event.Attr.WorkspacePath = "/projects/old-path"
	mustWrite(t, db, Batch{Events: []EventRecord{r}})
	const query = `SELECT workspace_path FROM sessions WHERE session_key='workspace-event'`
	if got := scanOne(t, db, query); got != nil {
		t.Fatalf("이벤트 씨앗이 경로를 생성함: %v", got)
	}
	if err := db.ApplyLifecycle(context.Background(), "claude_code", "workspace-event",
		event.SecFromTime(baseTime), false, "/projects/hook-path"); err != nil {
		t.Fatal(err)
	}
	r.Event.Sequence++
	mustWrite(t, db, Batch{Events: []EventRecord{r}})
	if got := scanOne(t, db, query); got != "/projects/hook-path" {
		t.Fatalf("이벤트 씨앗이 훅 경로를 덮음: %v", got)
	}
}
