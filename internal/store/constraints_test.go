package store

import "testing"

func TestRequiredKeysAndOrderConstraints(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)
	expectConstraint(t, db, `INSERT INTO meta ("key", value) VALUES (NULL, 'value')`)
	expectConstraint(t, db, `INSERT INTO vendors (vendor, first_seen, last_seen, status) VALUES (NULL, 1, 1, 'enabled')`)
	for _, value := range []any{nil, 0, -1, 1.5, "invalid"} {
		expectConstraint(t, db, `UPDATE events SET seq = ? WHERE id = (SELECT MIN(id) FROM events)`, value)
	}
	for _, value := range []any{-1, 0.5, "invalid"} {
		expectConstraint(t, db, `UPDATE turns SET turn_index = ? WHERE turn_key = 'p-old'`, value)
	}
	for _, value := range []any{nil, 0, 1} {
		mustExec(t, db, `UPDATE turns SET turn_index = ? WHERE turn_key = 'p-old'`, value)
	}
}

func TestTimestampOrderConstraints(t *testing.T) {
	for _, table := range []string{"sessions", "turns"} {
		t.Run(table, func(t *testing.T) {
			db := openTestDB(t)
			seedRetention(t, db, baseTime)
			query := `UPDATE ` + table + ` SET started_at = ?, ended_at = ?`
			for _, pair := range [][2]any{{nil, nil}, {100, nil}, {nil, 100}, {100, 100}, {100, 101}} {
				mustExec(t, db, query, pair[0], pair[1])
			}
			expectConstraint(t, db, query, 100, 99)
		})
	}
}

// 이벤트 수신 전에도 독립 저장할 수 있으며 JSON 타입과 상태 값을 제한한다.
func TestVendorLimitSnapshotConstraints(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, `INSERT INTO vendor_limit_snapshots (vendor, state, checked_at) VALUES ('codex', 'unavailable', 1)`)
	mustExec(t, db, `UPDATE vendor_limit_snapshots SET state = 'available', windows_json = '[{}]', extra_json = '{"usage":0}'`)
	expectConstraint(t, db, `UPDATE vendor_limit_snapshots SET state = 'unknown'`)
	for column, invalid := range map[string][]any{
		"windows_json": {nil, "{", "{}", "null", "1"},
		"extra_json":   {nil, "{", "[]", "null", "1"},
	} {
		for _, value := range invalid {
			expectConstraint(t, db, `UPDATE vendor_limit_snapshots SET `+column+` = ?`, value)
		}
	}
}
