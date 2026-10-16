package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
)

func codexPrompt(name, body, message string, when time.Time, sequence int) EventRecord {
	r := evrec(name, when, sequence, withVendor("codex"), inTurn("p1"), promptBody(body))
	r.Event.MessageID = message
	return r
}

func TestCodexOTelWriteCreatesPendingWithoutChangingDedupKey(t *testing.T) {
	db := openTestDB(t)
	a := codexPrompt("codex.user_prompt", "hello", "message-a", baseTime, 0)
	b := a
	b.Event.MessageID = "message-b"
	if a.Event.DedupKey() != b.Event.DedupKey() {
		t.Fatal("명시 message ID가 기존 중복 키를 변경함")
	}
	res := mustWrite(t, db, Batch{Events: []EventRecord{a}})
	if res.CodexTurnsTouched == 0 {
		t.Fatalf("commit 후 알림 대상이 없음: %+v", res)
	}
	var label, state, message, completeness string
	var provenance, pending int
	err := db.SQL().QueryRow(`SELECT p.label,p.processing_state,t.prompt_message_id,t.prompt_completeness,
(SELECT COUNT(*) FROM codex_turn_provenance),(SELECT COUNT(*) FROM codex_pending)
FROM turns t JOIN codex_turn_provenance p ON p.turn_id=t.id`).Scan(&label, &state, &message, &completeness, &provenance, &pending)
	if err != nil {
		t.Fatal(err)
	}
	if label != "unknown" || state != "pending" || message != "message-a" || completeness != "complete" || provenance != 1 || pending != 1 {
		t.Fatalf("OTel 단서 저장 = %s/%s/%s/%s provenance=%d pending=%d", label, state, message, completeness, provenance, pending)
	}
	res = mustWrite(t, db, Batch{Events: []EventRecord{b}})
	if res.EventsDuplicate != 1 || res.CodexTurnsTouched != 0 {
		t.Fatalf("message ID만 다른 재전송 = %+v", res)
	}
}

func TestCodexConflictingOTelPromptDoesNotMixProofs(t *testing.T) {
	db := openTestDB(t)
	first := codexPrompt("codex.user_prompt", "short", "m1", baseTime, 0)
	first.Contents[0].Truncated = true
	first.Contents[0].CapBytes = 5
	first.Contents[0].OriginalBytes = 10
	first.Contents[0].OriginalHash = "abc"
	mustWrite(t, db, Batch{Events: []EventRecord{first}})
	second := codexPrompt("codex.user_prompt", "different", "m2", baseTime.Add(time.Second), 1)
	mustWrite(t, db, Batch{Events: []EventRecord{second}})
	var body, completeness, proof string
	var conflict int
	err := db.SQL().QueryRow(`SELECT prompt_text,prompt_completeness,prompt_completeness_evidence,prompt_conflict FROM turns`).Scan(&body, &completeness, &proof, &conflict)
	if err != nil {
		t.Fatal(err)
	}
	if body != "short" || completeness != "unknown" || proof != "conflicting OTel prompt records" || conflict != 1 {
		t.Fatalf("서로 다른 OTel 근거 혼합: body=%q completeness=%s proof=%s conflict=%d", body, completeness, proof, conflict)
	}
}

func TestCodexNewOTelTurnClosesAndRequeuesPreviousOpenInterval(t *testing.T) {
	db := openTestDB(t)
	first := codexPrompt("codex.user_prompt", "repeat", "", baseTime, 0)
	mustWrite(t, db, Batch{Events: []EventRecord{first}})
	var firstID int64
	if err := db.SQL().QueryRow(`SELECT id FROM turns WHERE turn_key='p1'`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`UPDATE codex_turn_provenance SET label='client_submitted',processing_state='finalized',link_state='ambiguous' WHERE turn_id=?`, firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`DELETE FROM codex_pending WHERE turn_id=?`, firstID); err != nil {
		t.Fatal(err)
	}
	second := codexPrompt("codex.user_prompt", "repeat", "", baseTime.Add(10*time.Second), 1)
	second.TurnKey = "p2"
	res := mustWrite(t, db, Batch{Events: []EventRecord{second}})
	if res.CodexTurnsTouched < 2 {
		t.Fatalf("이전 열린 턴 재검토 알림 없음: %+v", res)
	}
	var state string
	var pending int
	if err := db.SQL().QueryRow(`SELECT p.processing_state,(SELECT COUNT(*) FROM codex_pending q WHERE q.turn_id=p.turn_id)
FROM codex_turn_provenance p WHERE p.turn_id=?`, firstID).Scan(&state, &pending); err != nil {
		t.Fatal(err)
	}
	if state != "retrying" || pending != 1 {
		t.Fatalf("새 턴 뒤 직전 판정 = %s pending=%d", state, pending)
	}
	rows, err := db.ListCodexPending(context.Background(), time.Now().Unix(), 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == firstID {
			if row.EndedAt != 0 || row.NextStartedAt != baseTime.Add(10*time.Second).Unix() {
				t.Fatalf("직전 턴 exclusive 상한 = end=%d next=%d", row.EndedAt, row.NextStartedAt)
			}
			return
		}
	}
	t.Fatal("재조회 가능한 직전 턴이 없다")
}

func TestCodexPurgePreservesLabelAndRejectsOldContentReplay(t *testing.T) {
	db := openTestDB(t)
	first := codexPrompt("codex.user_prompt", "old body", "m1", baseTime, 0)
	mustWrite(t, db, Batch{Events: []EventRecord{first}})
	if _, err := db.SQL().Exec(`UPDATE codex_turn_provenance SET label='client_submitted',processing_state='finalized'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`DELETE FROM codex_pending`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PurgeContent(context.Background(), time.Time{}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, db, Batch{Events: []EventRecord{first}})
	late := codexPrompt("codex.user_prompt", "replayed old body", "m2", baseTime.Add(time.Second), 1)
	mustWrite(t, db, Batch{Events: []EventRecord{late}})
	var body sql.NullString
	var purged int
	var label, state string
	var pending int
	err := db.SQL().QueryRow(`SELECT t.prompt_text,t.content_purged,p.label,p.processing_state,
(SELECT COUNT(*) FROM codex_pending q WHERE q.turn_id=t.id)
FROM turns t JOIN codex_turn_provenance p ON p.turn_id=t.id`).Scan(&body, &purged, &label, &state, &pending)
	if err != nil {
		t.Fatal(err)
	}
	if body.Valid || purged != 1 || label != "client_submitted" || state != "finalized" || pending != 0 {
		t.Fatalf("purge 이후 원문/분류 = body=%v purged=%d label=%s state=%s pending=%d", body, purged, label, state, pending)
	}
	var eventPayload sql.NullString
	if err := db.SQL().QueryRow(`SELECT payload FROM events WHERE record_hash=?`, late.Event.DedupKey()).Scan(&eventPayload); err != nil {
		t.Fatal(err)
	}
	if eventPayload.Valid {
		t.Fatal("purge된 턴에 새 이벤트 payload가 복구됨")
	}
	newTurn := codexPrompt("codex.user_prompt", "fresh body", "m3", time.Now().Add(time.Second), 2)
	newTurn.TurnKey = "p2"
	newTurn.Contents = []event.Content{{Kind: event.ContentPrompt, Body: "fresh body"}}
	mustWrite(t, db, Batch{Events: []EventRecord{newTurn}})
	var fresh sql.NullString
	if err := db.SQL().QueryRow(`SELECT prompt_text FROM turns WHERE turn_key='p2'`).Scan(&fresh); err != nil {
		t.Fatal(err)
	}
	if !fresh.Valid || fresh.String != "fresh body" {
		t.Fatalf("purge 뒤 새 제출까지 차단: %v", fresh)
	}
}
