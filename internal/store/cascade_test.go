package store

import "testing"

// 결정과 결과가 다른 턴에 있어도 세션 삭제 한 번으로 전체 소유 데이터를 지운다.
func TestSessionCascadeAcrossTurns(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)
	mustExec(t, db, `INSERT INTO turns (session_id, turn_key, turn_index)
		SELECT id, 'decision-turn', 1 FROM sessions WHERE session_key = 'sess-old'`)
	mustExec(t, db, `INSERT INTO events (turn_id, seq, event_name, record_hash)
		SELECT id, 1, 'tool_decision', 'decision-hash' FROM turns WHERE turn_key = 'decision-turn'`)
	mustExec(t, db, `UPDATE tool_calls SET decision_event_id = (SELECT id FROM events WHERE record_hash = 'decision-hash')`)
	expectConstraint(t, db, `DELETE FROM vendors`)
	expectConstraint(t, db, `DELETE FROM events WHERE record_hash = 'decision-hash'`)
	mustExec(t, db, `DELETE FROM sessions WHERE session_key = 'sess-old'`)
	for table, want := range map[string]int{"vendors": 1, "sessions": 1, "turns": 1, "events": 1, "llm_calls": 0, "tool_calls": 0, "file_changes": 0} {
		if got := countRows(t, db, table); got != want {
			t.Errorf("%s: %d행, want %d", table, got, want)
		}
	}
	assertNoOrphans(t, db)
}

func TestOwnedDataCascade(t *testing.T) {
	for _, table := range []string{"turns", "tool_calls"} {
		t.Run(table, func(t *testing.T) {
			db := openTestDB(t)
			seedRetention(t, db, baseTime)
			if table == "turns" {
				mustExec(t, db, `DELETE FROM turns WHERE session_id IN (SELECT id FROM sessions WHERE session_key = 'sess-old')`)
				if countRows(t, db, "llm_calls") != 0 || countRows(t, db, "events") != 1 {
					t.Fatal("턴의 호출·이벤트 연쇄 삭제 결과가 다르다")
				}
			} else {
				mustExec(t, db, `DELETE FROM tool_calls`)
				if countRows(t, db, "events") != 4 || countRows(t, db, "llm_calls") != 1 {
					t.Fatal("도구 호출 삭제가 원본 이벤트나 LLM 호출을 지웠다")
				}
			}
			if countRows(t, db, "file_changes") != 0 || countRows(t, db, "tool_calls") != 0 || countRows(t, db, "sessions") != 2 {
				t.Fatal("소유 데이터 삭제 범위가 다르다")
			}
			assertNoOrphans(t, db)
		})
	}
}

func TestToolSuccessConstraint(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)
	for _, value := range []any{nil, 0, 1} {
		mustExec(t, db, `UPDATE tool_calls SET success = ?`, value)
	}
	for _, value := range []any{-1, 2, 0.5, "invalid"} {
		expectConstraint(t, db, `UPDATE tool_calls SET success = ?`, value)
	}
}
