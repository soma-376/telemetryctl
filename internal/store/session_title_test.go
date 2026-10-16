package store

import (
	"context"
	"testing"

	"github.com/your-org/pulsemetry/internal/session"
)

func seedSession(t *testing.T, db *DB, key, vendor string) {
	t.Helper()
	mustWrite(t, db, Batch{Sessions: []session.Session{{
		SessionID: key, Vendor: vendor, StartedAt: 1, LastEventAt: 1,
	}}})
}

func titleOf(t *testing.T, db *DB, key string) string {
	t.Helper()
	var title string
	if err := db.SQL().QueryRow(
		`SELECT COALESCE(title,'') FROM sessions WHERE session_key = ?`, key).Scan(&title); err != nil {
		t.Fatal(err)
	}
	return title
}

// 조립기는 제목을 만들지 않는다. 벤더 제목이 오기 전까지 title 은 NULL 이다.
func TestSessionTitleStartsNull(t *testing.T) {
	db := openTestDB(t)
	seedSession(t, db, "c-1", "claude_code")

	var title any
	if err := db.SQL().QueryRow(`SELECT title FROM sessions WHERE session_key = 'c-1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != nil {
		t.Fatalf("title = %v, want NULL", title)
	}
}

// 벤더 제목은 이후 변경도 그대로 반영한다 — 비교할 다른 출처가 없다.
func TestSetClaudeTitleReflectsLatest(t *testing.T) {
	db := openTestDB(t)
	seedSession(t, db, "c-1", "claude_code")

	ctx := context.Background()
	for _, want := range []string{"첫 제목", "바뀐 제목"} {
		if err := db.SetClaudeTitle(ctx, "c-1", want); err != nil {
			t.Fatal(err)
		}
		if got := titleOf(t, db, "c-1"); got != want {
			t.Fatalf("제목 = %q, want %q", got, want)
		}
	}
}

// 트랜스크립트는 Claude Code 의 것이다. claude_code_desktop 은 같은 저장소를 쓰므로
// 포함하고, 다른 벤더는 건드리지 않는다.
func TestSetClaudeTitleScopedToClaudeVendors(t *testing.T) {
	db := openTestDB(t)
	seedSession(t, db, "shared", "codex")
	seedSession(t, db, "desktop", "claude_code_desktop")

	ctx := context.Background()
	if err := db.SetClaudeTitle(ctx, "shared", "덮으면 안 된다"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetClaudeTitle(ctx, "desktop", "데스크탑도 대상이다"); err != nil {
		t.Fatal(err)
	}

	if got := titleOf(t, db, "shared"); got != "" {
		t.Fatalf("codex 세션 제목이 바뀌었다: %q", got)
	}
	if got := titleOf(t, db, "desktop"); got != "데스크탑도 대상이다" {
		t.Fatalf("desktop 제목 = %q", got)
	}
}

func TestSetClaudeTitleIgnoresEmptyInput(t *testing.T) {
	db := openTestDB(t)
	seedSession(t, db, "c-1", "claude_code")

	ctx := context.Background()
	if err := db.SetClaudeTitle(ctx, "c-1", "제목"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetClaudeTitle(ctx, "c-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SetClaudeTitle(ctx, "", "다른 제목"); err != nil {
		t.Fatal(err)
	}
	if got := titleOf(t, db, "c-1"); got != "제목" {
		t.Fatalf("빈 입력이 제목을 바꿨다: %q", got)
	}
}

// Codex 제목은 App Server 스레드 이름이다 (ADR 0017). 이름이 바뀌면 그것이 최신 제목이다.
func TestSetCodexTitleReflectsLatest(t *testing.T) {
	db := openTestDB(t)
	seedSession(t, db, "thread-1", "codex")

	ctx := context.Background()
	for _, want := range []string{"첫 스레드 이름", "바뀐 스레드 이름"} {
		if err := db.SetCodexTitle(ctx, "thread-1", want); err != nil {
			t.Fatal(err)
		}
		if got := titleOf(t, db, "thread-1"); got != want {
			t.Fatalf("제목 = %q, want %q", got, want)
		}
	}
}

func TestSetCodexTitleScopedToCodex(t *testing.T) {
	db := openTestDB(t)
	seedSession(t, db, "shared", "claude_code")

	if err := db.SetCodexTitle(context.Background(), "shared", "덮으면 안 된다"); err != nil {
		t.Fatal(err)
	}
	if got := titleOf(t, db, "shared"); got != "" {
		t.Fatalf("claude 세션 제목이 바뀌었다: %q", got)
	}
}

func TestSetCodexTitleIgnoresEmptyInput(t *testing.T) {
	db := openTestDB(t)
	seedSession(t, db, "thread-1", "codex")

	ctx := context.Background()
	if err := db.SetCodexTitle(ctx, "thread-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SetCodexTitle(ctx, "", "제목"); err != nil {
		t.Fatal(err)
	}
	if got := titleOf(t, db, "thread-1"); got != "" {
		t.Fatalf("빈 입력이 제목을 만들었다: %q", got)
	}
}
