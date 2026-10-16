package dashboard_test

import (
	"context"
	"reflect"
	"testing"

	. "github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/dashboard/activity"
)

// 목록 자격은 세션의 실제 확정 턴으로 판정하고, 검색·정렬·커서보다 먼저 적용한다.
func TestActivityCodexSessionVisibilityAndContentSearch(t *testing.T) {
	f := TestNewFixture(t)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.TestDB().SQL().ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO vendors(vendor,first_seen,last_seen,status) VALUES
 ('codex',1,2,'enabled'),('claude_code',1,2,'enabled')`)
	exec(`INSERT INTO sessions(id,vendor_id,session_key,started_at,ended_at,title,workspace_path) VALUES
 (1,'codex','internal-only',10,NULL,'hidden','/a'),
 (2,'codex','unknown-only',20,NULL,'hidden','/a'),
 (3,'codex','pending-only',30,NULL,'hidden','/a'),
 (4,'codex','virtual-only',40,NULL,'hidden','/a'),
 (5,'codex','no-turn',50,NULL,'hidden','/a'),
 (6,'codex','client-mixed',60,NULL,'needle title','/a'),
 (7,'codex','client-three',70,90,'three','/b'),
 (8,'codex','ambiguous-purged',80,90,'purged','/b'),
 (9,'claude_code','claude-legacy',90,100,'claude','/c'),
 (10,'codex','no-provenance',45,NULL,'hidden','/a'),
 (11,'codex','client-pending',55,NULL,'hidden','/a')`)
	exec(`INSERT INTO turns(id,session_id,turn_key,turn_index,started_at,prompt_text,content_purged) VALUES
 (1,1,'internal',1,10,'needle internal',0),
 (2,2,'unknown',1,20,'needle unknown',0),
 (3,3,'pending',1,30,'needle pending',0),
 (4,4,'virtual',NULL,40,'needle virtual',0),
 (5,6,'client',1,60,'chosen words',0),
 (6,6,'latest-internal',2,61,'needle private',0),
 (7,7,'client-1',1,70,'first',0),
 (8,7,'client-2',2,71,'second',0),
 (9,7,'client-3',3,72,'third',0),
 (10,8,'client-purged',1,80,NULL,1),
 (11,9,'claude-virtual',NULL,90,'legacy needle',0),
 (12,10,'no-provenance',1,45,'client_submitted text',0),
 (13,11,'client-pending',1,55,'client pending text',0)`)
	exec(`INSERT INTO codex_turn_provenance(turn_id,label,processing_state,link_state) VALUES
 (1,'internal_task','finalized','unique'),
 (2,'unknown','finalized','unmatched'),
 (3,'unknown','pending','not_attempted'),
 (4,'client_submitted','finalized','unique'),
 (5,'client_submitted','finalized','unique'),
 (6,'internal_task','finalized','unique'),
 (7,'client_submitted','finalized','unique'),
 (8,'client_submitted','finalized','unique'),
 (9,'client_submitted','finalized','unique'),
 (10,'client_submitted','finalized','ambiguous'),
 (13,'client_submitted','pending','not_attempted')`)

	q := activity.Query{Vendors: []string{TestVendorCodex}, Limit: 2}
	first, err := readActivity(f.TestPath(), ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if got := activityKeys(first.Rows); !reflect.DeepEqual(got, []string{"client-mixed", "ambiguous-purged"}) || !first.HasMore {
		t.Fatalf("첫 페이지 = %v, HasMore=%v", got, first.HasMore)
	}
	q.Cursor = first.NextCursor
	second, err := readActivity(f.TestPath(), ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if got := activityKeys(second.Rows); !reflect.DeepEqual(got, []string{"client-three"}) || second.HasMore || second.Rows[0].Prompts != 3 {
		t.Fatalf("두 번째 페이지 = %+v", second)
	}
	if first.Rows[0].Prompts != 1 || first.Rows[1].Prompts != 1 {
		t.Fatalf("혼합·purge의 프롬프트 수 = %+v", first.Rows)
	}
	// 완료 분류가 없는 세션에 자격 턴처럼 보이는 본문이 있어도 목록에 나타나지 않는다.
	for _, term := range []string{"unknown", "pending", "virtual", "client_submitted"} {
		page, err := readActivity(f.TestPath(), ctx, activity.Query{Vendors: []string{TestVendorCodex}, Text: term})
		if err != nil || len(page.Rows) != 0 {
			t.Fatalf("%q 검색 = %+v, %v", term, page, err)
		}
	}
	page, err := readActivity(f.TestPath(), ctx, activity.Query{Vendors: []string{TestVendorCodex}, Text: "needle"})
	if err != nil || !reflect.DeepEqual(activityKeys(page.Rows), []string{"client-mixed"}) {
		t.Fatalf("제목/내부 본문 검색 = %+v, %v", page, err)
	}
	if !TestContainsString(page.Rows[0].MatchedSources, SourceTitle) || TestContainsString(page.Rows[0].MatchedSources, SourceContent) {
		t.Fatalf("내부 본문이 출처 배지에 섞임: %v", page.Rows[0].MatchedSources)
	}
	page, err = readActivity(f.TestPath(), ctx, activity.Query{Vendors: []string{TestVendorCodex}, Text: "chosen"})
	if err != nil || !reflect.DeepEqual(activityKeys(page.Rows), []string{"client-mixed"}) || !TestContainsString(page.Rows[0].MatchedSources, SourceContent) {
		t.Fatalf("확정 사용자 원문 검색 = %+v, %v", page, err)
	}
	page, err = readActivity(f.TestPath(), ctx, activity.Query{Vendors: []string{TestVendorClaude}, Text: "legacy"})
	if err != nil || !reflect.DeepEqual(activityKeys(page.Rows), []string{"claude-legacy"}) || !TestContainsString(page.Rows[0].MatchedSources, SourceContent) {
		t.Fatalf("다른 벤더의 기존 가상 원문 검색 = %+v, %v", page, err)
	}
	page, err = readActivity(f.TestPath(), ctx, activity.Query{Vendors: []string{TestVendorCodex}, Projects: []string{"/b"}, Status: []string{StatusCompleted}, Since: 70, Until: 81})
	if err != nil || !reflect.DeepEqual(activityKeys(page.Rows), []string{"ambiguous-purged", "client-three"}) {
		t.Fatalf("기존 필터 조합 = %+v, %v", page, err)
	}

	// 보강 commit 뒤 첫 페이지부터 갱신하면 기존 커서 앞에 새로 자격을 얻은 행도 보인다.
	exec(`UPDATE codex_turn_provenance SET label='client_submitted',processing_state='finalized' WHERE turn_id=3`)
	page, err = readActivity(f.TestPath(), ctx, activity.Query{Vendors: []string{TestVendorCodex}, Limit: 2})
	if err != nil || !reflect.DeepEqual(activityKeys(page.Rows), []string{"pending-only", "client-mixed"}) {
		t.Fatalf("보강 완료 뒤 첫 페이지 = %+v, %v", page, err)
	}
	exec(`UPDATE codex_turn_provenance SET processing_state='retrying' WHERE turn_id=3`)
	page, err = readActivity(f.TestPath(), ctx, activity.Query{Vendors: []string{TestVendorCodex}, Limit: 2})
	if err != nil || !reflect.DeepEqual(activityKeys(page.Rows), []string{"client-mixed", "ambiguous-purged"}) {
		t.Fatalf("마지막 자격 상실 뒤 첫 페이지 = %+v, %v", page, err)
	}
}
