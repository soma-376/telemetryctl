package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/your-org/pulsemetry/internal/event"
)

// writeCodexEvidence는 기존 턴 조립 결과에 출처 판정용 단서만 보탠다.
// JSONL에서 턴이나 호출을 만들지 않도록 OTel Write 트랜잭션에만 진입한다.
func (w *writer) writeCodexEvidence(recs []EventRecord, turnIDs, eventIDs []int64) error {
	seen := map[int64]bool{}
	for i, rec := range recs {
		if rec.Event.Vendor != "codex" || turnIDs[i] == 0 || rec.TurnKey == "" {
			continue
		}
		turnID := turnIDs[i]
		if !seen[turnID] {
			seen[turnID] = true
			result, err := w.tx.ExecContext(w.ctx, `INSERT INTO codex_turn_provenance
  (turn_id, label, processing_state, link_state, reason)
VALUES (?, 'unknown', 'pending', 'not_attempted', 'OTel turn awaiting JSONL')
ON CONFLICT(turn_id) DO NOTHING`, turnID)
			if err != nil {
				return fmt.Errorf("store: Codex 턴 pending 생성: %w", err)
			}
			if n, _ := result.RowsAffected(); n > 0 {
				if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO codex_pending (turn_id) VALUES (?)
ON CONFLICT(turn_id) DO NOTHING`, turnID); err != nil {
					return fmt.Errorf("store: Codex 재처리 항목 생성: %w", err)
				}
				w.res.CodexTurnsTouched++
			}
		}
		if rec.Event.Name != "codex.user_prompt" || eventIDs[i] == 0 {
			continue
		}
		var body string
		completeness, proof := "unknown", ""
		if w.db.cfg.storeContent {
			for _, c := range rec.Contents {
				if c.Kind != event.ContentPrompt {
					continue
				}
				body = c.Body
				if c.Truncated && c.OriginalBytes > len(c.Body) && c.OriginalHash != "" {
					completeness = "truncated"
					proof = codexProof(map[string]any{"source": "decoder_utf8_cap", "cap_bytes": c.CapBytes,
						"stored_bytes": len(c.Body), "original_bytes": c.OriginalBytes, "original_sha256": c.OriginalHash})
				} else if !c.Truncated {
					completeness = "complete"
					proof = codexProof(map[string]any{"source": "decoder_whole_content", "bytes": len(c.Body)})
				}
				break
			}
		}
		var oldBody, oldMessage, oldTurn, oldState sql.NullString
		var oldEvidenceID sql.NullInt64
		var purged int
		if err := w.tx.QueryRowContext(w.ctx, `SELECT prompt_text, prompt_message_id,
 prompt_source_turn_id, prompt_completeness, prompt_evidence_event_id, content_purged
FROM turns WHERE id=?`, turnID).Scan(&oldBody, &oldMessage, &oldTurn, &oldState, &oldEvidenceID, &purged); err != nil {
			return fmt.Errorf("store: Codex 프롬프트 근거 조회: %w", err)
		}
		if purged != 0 {
			var label, state string
			if err := w.tx.QueryRowContext(w.ctx, `SELECT label,processing_state FROM codex_turn_provenance WHERE turn_id=?`, turnID).Scan(&label, &state); err != nil {
				return err
			}
			// purge는 이미 확정된 집계를 지우지 않는다. 늦게 들어온 OTel은
			// 이벤트 자체만 저장하고 삭제된 본문 근거를 되살리지 않는다.
			if state == "finalized" && label != "unknown" {
				continue
			}
		}
		conflict := (oldBody.Valid && body != "" && oldBody.String != body) ||
			(oldMessage.Valid && rec.Event.MessageID != "" && oldMessage.String != rec.Event.MessageID) ||
			(oldTurn.Valid && rec.Event.TurnKey != "" && oldTurn.String != rec.Event.TurnKey) ||
			(oldEvidenceID.Valid && oldState.String != "unknown" && completeness != "unknown" && oldState.String != completeness)
		if conflict {
			if _, err := w.tx.ExecContext(w.ctx, `UPDATE turns SET prompt_conflict=1,
  prompt_completeness='unknown', prompt_completeness_evidence='conflicting OTel prompt records'
WHERE id=?`, turnID); err != nil {
				return err
			}
		} else if !oldEvidenceID.Valid {
			var storedBody any
			var storedProof any
			if purged == 0 {
				storedBody, storedProof = nullStr(body), nullStr(proof)
			} else {
				completeness = "unknown"
			}
			if _, err := w.tx.ExecContext(w.ctx, `UPDATE turns SET
  prompt_text = CASE WHEN content_purged=0 THEN COALESCE(prompt_text, ?) ELSE NULL END,
  prompt_message_id = COALESCE(prompt_message_id, ?),
  prompt_source_turn_id = COALESCE(prompt_source_turn_id, ?),
  prompt_completeness = ?,
  prompt_completeness_evidence = ?,
  prompt_evidence_event_id = ?
WHERE id=?`, storedBody, nullStr(rec.Event.MessageID), nullStr(rec.Event.TurnKey),
				completeness, storedProof, eventIDs[i], turnID); err != nil {
				return fmt.Errorf("store: Codex 프롬프트 근거 저장: %w", err)
			}
		}
		// 새로운 프롬프트 이벤트는 이전 완료 판정을 재검토할 이유가 된다.
		if _, err := w.tx.ExecContext(w.ctx, `UPDATE codex_turn_provenance
SET processing_state='retrying' WHERE turn_id=? AND processing_state='finalized'`, turnID); err != nil {
			return err
		}
		if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO codex_pending (turn_id, next_check_at) VALUES (?,0)
ON CONFLICT(turn_id) DO UPDATE SET next_check_at=0`, turnID); err != nil {
			return err
		}
		w.res.CodexTurnsTouched++
		// 진행 중인 직전 턴의 유효 구간은 이 턴의 시작에서 닫힌다.
		// 같은 본문이 먼저 JSONL에 들어온 경우 이전 판정도 다시 확인한다.
		var previousID int64
		err := w.tx.QueryRowContext(w.ctx, `SELECT previous.id FROM turns current
JOIN turns previous ON previous.session_id=current.session_id
WHERE current.id=? AND previous.turn_index<current.turn_index AND previous.ended_at IS NULL
ORDER BY previous.turn_index DESC LIMIT 1`, turnID).Scan(&previousID)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("store: 이전 Codex 턴 경계 조회: %w", err)
		}
		if err == nil {
			changed, e := w.tx.ExecContext(w.ctx, `UPDATE codex_turn_provenance SET processing_state='retrying'
WHERE turn_id=? AND NOT (processing_state='finalized' AND label!='unknown'
 AND (SELECT content_purged FROM turns WHERE id=codex_turn_provenance.turn_id)=1)`, previousID)
			if e != nil {
				return e
			}
			if n, _ := changed.RowsAffected(); n > 0 {
				if _, e := w.tx.ExecContext(w.ctx, `INSERT INTO codex_pending(turn_id,next_check_at) VALUES (?,0)
ON CONFLICT(turn_id) DO UPDATE SET next_check_at=0`, previousID); e != nil {
					return e
				}
				w.res.CodexTurnsTouched++
			}
		}
	}
	return nil
}

func codexProof(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
