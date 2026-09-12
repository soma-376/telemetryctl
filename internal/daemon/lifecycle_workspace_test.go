package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/localapi"
)

func TestLifecycleHookStoresWorkspace(t *testing.T) {
	for _, vendor := range []string{"claude_code", "codex"} {
		t.Run(vendor, func(t *testing.T) {
			db := openTestStore(t)
			t.Cleanup(func() { _ = db.Close() })
			p := newTestPipeline(t, db, &syncBuffer{}, func() time.Time { return time.Unix(fixtureUnix, 0) })
			srv := httptest.NewServer(localapi.NewServer(nil, nil, p))
			defer srv.Close()
			workspace := `C:\projects\한글 폴더\workspace`
			body, err := json.Marshal(map[string]string{
				"session_id": "workspace-hook", "hook_event_name": "SessionStart",
				"source": "startup", "cwd": workspace,
			})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.Post(srv.URL+localapi.SessionStartPath+"?vendor="+vendor,
				"application/json", strings.NewReader(string(body)))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status=%d", resp.StatusCode)
			}
			var got string
			if err := db.SQL().QueryRowContext(context.Background(),
				`SELECT workspace_path FROM sessions WHERE vendor_id=? AND session_key=?`,
				vendor, "workspace-hook").Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != workspace {
				t.Fatalf("workspace=%q, want %q", got, workspace)
			}
		})
	}
}

func TestOTLPDoesNotWriteSessionWorkspace(t *testing.T) {
	for _, hooked := range []bool{false, true} {
		name := "훅 없는 세션은 경로 없음"
		if hooked {
			name = "훅 경로는 OTel 저장 후에도 유지"
		}
		t.Run(name, func(t *testing.T) {
			db := openTestStore(t)
			t.Cleanup(func() { _ = db.Close() })
			p := newTestPipeline(t, db, &syncBuffer{}, func() time.Time { return time.Unix(fixtureUnix, 0) })
			batch := walkthroughBatch(t)
			const hookPath = "/projects/resumed-worktree"
			if hooked {
				if err := p.SubmitLifecycle(context.Background(), localapi.LifecycleEvent{
					Vendor: "claude_code", SessionID: fixtureSession, WorkspacePath: hookPath,
				}); err != nil {
					t.Fatal(err)
				}
			}
			// 픽스처에는 hookPath와 다른 cwd가 있다. 이벤트 쓰기와 최종 스냅샷을 모두 기다린다.
			if !strings.Contains(string(batch.Body), fixturePath) {
				t.Fatal("경로 픽스처가 없음")
			}
			if err := p.Consume(context.Background(), batch); err != nil {
				t.Fatal(err)
			}
			if !p.close(time.Now().Add(5 * time.Second)) {
				t.Fatal("파이프라인 종료 실패")
			}
			var got sql.NullString
			if err := db.SQL().QueryRow(`SELECT workspace_path FROM sessions WHERE vendor_id=? AND session_key=?`,
				"claude_code", fixtureSession).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if hooked {
				if !got.Valid || got.String != hookPath {
					t.Fatalf("workspace=%v", got)
				}
			} else if got.Valid {
				t.Fatalf("OTel이 경로를 저장함: %q", got.String)
			}
		})
	}
}
