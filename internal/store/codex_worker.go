package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrCodexWorkerEpoch = errors.New("store: Codex 워커 세대가 만료되었다")

type CodexFileState struct {
	ID, Generation, CommittedOffset, ObservedSize, ObservedModNS   int64
	Path, OSFileID, Owner, Parent, OwnerSource, PrefixHash, Status string
}

type CodexRecord struct {
	ID, FileID, StartOffset, EndOffset, EventTime                                                                            int64
	Type, MessageID, TurnID, Body, BodyHash, Completeness, CompletenessEvidence, Structure, RepresentationKey, SessionMetaID string
}

type CodexLineError struct {
	StartOffset, EndOffset int64
	Kind, Diagnostic       string
}

type CodexFileBatch struct {
	Path, OSFileID, Owner, Parent, OwnerSource, PrefixHash, Status, PriorDisposition string
	PriorID, PriorOffset                                                             int64
	NewGeneration                                                                    bool
	CommittedOffset                                                                  int64
	ObservedSize, ObservedModNS                                                      int64
	Records                                                                          []CodexRecord
	Errors                                                                           []CodexLineError
}

type CodexPromptRow struct {
	ID, StartedAt, EndedAt, NextStartedAt                                                              int64
	Owner, MessageID, TurnID, Body, Completeness, CompletenessEvidence, Source, ProcessingState, Label string
	Conflict, Purged, TooLarge, SameSecond                                                             bool
}

type CodexDecision struct {
	TurnID, LinkedRecordID                                                                      int64
	Label, ProcessingState, LinkState, LinkMethod, StructureEvidence, Reason, ClassifierVersion string
	NextCheckAt                                                                                 int64
}

// BeginCodexWorkerEpoch는 기존 워커가 늦게 도착해도 저장하지 못하도록 세대를 올린다.
// 단일 쓰기 연결에서 이 갱신과 모든 보강 커밋이 직렬화된다.
func (d *DB) BeginCodexWorkerEpoch(ctx context.Context) (int64, error) {
	var epoch int64
	err := d.db.QueryRowContext(ctx, `UPDATE codex_worker_state SET epoch=epoch+1,status='running',heartbeat_at=?,job_started_at=0,reason='' WHERE id=1 RETURNING epoch`, time.Now().Unix()).Scan(&epoch)
	if err != nil {
		return 0, fmt.Errorf("store: Codex 워커 세대 시작: %w", err)
	}
	return epoch, nil
}

func (d *DB) CodexWorkerHeartbeat(ctx context.Context, epoch int64, jobStarted bool) error {
	job := int64(0)
	if jobStarted {
		job = time.Now().Unix()
	}
	r, err := d.db.ExecContext(ctx, `UPDATE codex_worker_state SET heartbeat_at=?,job_started_at=? WHERE id=1 AND epoch=? AND status='running'`, time.Now().Unix(), job, epoch)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrCodexWorkerEpoch
	}
	return nil
}

func (d *DB) SetCodexWorkerStatus(ctx context.Context, epoch int64, status, reason string, restarts int) error {
	r, err := d.db.ExecContext(ctx, `UPDATE codex_worker_state SET status=?,reason=?,restart_count=?,heartbeat_at=? WHERE id=1 AND epoch=?`, status, reason, restarts, time.Now().Unix(), epoch)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrCodexWorkerEpoch
	}
	return nil
}

func (d *DB) CodexFile(ctx context.Context, path string) (CodexFileState, bool, error) {
	var f CodexFileState
	err := d.db.QueryRowContext(ctx, `SELECT id,path,generation,os_file_id,owner_session_key,parent_session_key,owner_source,committed_offset,committed_prefix_hash,observed_size,observed_mod_ns,status
FROM codex_jsonl_files WHERE path=? ORDER BY generation DESC LIMIT 1`, path).Scan(&f.ID, &f.Path, &f.Generation, &f.OSFileID, &f.Owner, &f.Parent, &f.OwnerSource, &f.CommittedOffset, &f.PrefixHash, &f.ObservedSize, &f.ObservedModNS, &f.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return f, false, nil
	}
	if err != nil {
		return f, false, err
	}
	return f, true, nil
}

func (d *DB) CodexFileByOSID(ctx context.Context, id string) (CodexFileState, bool, error) {
	var f CodexFileState
	err := d.db.QueryRowContext(ctx, `SELECT id,path,generation,os_file_id,owner_session_key,parent_session_key,owner_source,committed_offset,committed_prefix_hash,observed_size,observed_mod_ns,status
FROM codex_jsonl_files WHERE os_file_id=? AND status IN ('active','rotated')
ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, observed_at DESC,id DESC LIMIT 1`, id).Scan(&f.ID, &f.Path, &f.Generation, &f.OSFileID, &f.Owner, &f.Parent, &f.OwnerSource, &f.CommittedOffset, &f.PrefixHash, &f.ObservedSize, &f.ObservedModNS, &f.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return f, false, nil
	}
	if err != nil {
		return f, false, err
	}
	return f, true, nil
}

func (d *DB) CodexOwnerPresent(ctx context.Context, owner string) (bool, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE vendor_id='codex' AND session_key=?`, owner).Scan(&n)
	return n > 0, err
}

func (d *DB) CodexOwnerHasErrors(ctx context.Context, owner string) (bool, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM codex_jsonl_files f
WHERE f.owner_session_key=? AND f.status IN ('active','quarantined')
AND (f.status='quarantined' OR EXISTS (SELECT 1 FROM codex_jsonl_errors e WHERE e.file_id=f.id))`, owner).Scan(&n)
	return n > 0, err
}

func (d *DB) CodexOwnerFiles(ctx context.Context, owner string) ([]CodexFileState, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id,path,generation,os_file_id,owner_session_key,parent_session_key,owner_source,committed_offset,committed_prefix_hash,observed_size,observed_mod_ns,status
FROM codex_jsonl_files WHERE owner_session_key=? ORDER BY id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodexFileState
	for rows.Next() {
		var f CodexFileState
		if err := rows.Scan(&f.ID, &f.Path, &f.Generation, &f.OSFileID, &f.Owner, &f.Parent, &f.OwnerSource, &f.CommittedOffset, &f.PrefixHash, &f.ObservedSize, &f.ObservedModNS, &f.Status); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (d *DB) DropCodexPending(ctx context.Context, epoch, turnID int64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err = checkCodexEpoch(ctx, tx, epoch); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM codex_pending WHERE turn_id=?`, turnID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) RequeueCodexUnknown(ctx context.Context, version string) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO codex_pending(turn_id,next_check_at)
SELECT turn_id,0 FROM codex_turn_provenance WHERE label='unknown' AND classifier_version<>?
ON CONFLICT(turn_id) DO UPDATE SET next_check_at=0`, version)
	return err
}

// BackfillCodexPending은 현재 지원 DDL에 이미 저장된 과거 Codex 실제 턴만
// 보강 대상으로 등록한다. DB 구조를 변환하거나 JSONL에서 턴을 생성하지 않는다.
func (d *DB) BackfillCodexPending(ctx context.Context) (int64, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	result, err := tx.ExecContext(ctx, `INSERT INTO codex_turn_provenance
(turn_id,label,processing_state,link_state,reason)
SELECT t.id,'unknown','pending','not_attempted','stored v1 turn awaiting JSONL'
FROM turns t JOIN sessions s ON s.id=t.session_id
WHERE s.vendor_id='codex' AND t.turn_index IS NOT NULL
AND NOT EXISTS (SELECT 1 FROM codex_turn_provenance p WHERE p.turn_id=t.id)`)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO codex_pending(turn_id,next_check_at)
SELECT p.turn_id,0 FROM codex_turn_provenance p
JOIN turns t ON t.id=p.turn_id JOIN sessions s ON s.id=t.session_id
WHERE s.vendor_id='codex' AND p.processing_state IN ('pending','retrying')
ON CONFLICT(turn_id) DO NOTHING`); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

func (d *DB) ContentStorageEnabled() bool { return d.cfg.storeContent }

func checkCodexEpoch(ctx context.Context, tx *sql.Tx, epoch int64) error {
	var current int64
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT epoch,status FROM codex_worker_state WHERE id=1`).Scan(&current, &status); err != nil {
		return err
	}
	if current != epoch || status != "running" {
		return ErrCodexWorkerEpoch
	}
	return nil
}

// CommitCodexFile은 오류나 pending 재조회 단서와 완성된 행 체크포인트를 함께 커밋한다.
// 잘못된 세대와 기존 체크포인트 불일치는 아무것도 쓰지 않는다.
func (d *DB) CommitCodexFile(ctx context.Context, epoch int64, b CodexFileBatch) (CodexFileState, error) {
	var out CodexFileState
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := checkCodexEpoch(ctx, tx, epoch); err != nil {
		return out, err
	}
	var fileID, generation int64
	if b.NewGeneration || b.PriorID == 0 {
		if b.PriorID != 0 {
			priorDisposition := b.PriorDisposition
			if priorDisposition == "" {
				priorDisposition = "rotated"
			}
			if priorDisposition != "rotated" && priorDisposition != "superseded" {
				return out, errors.New("store: Codex 이전 파일 상태가 잘못되었다")
			}
			var changed sql.Result
			if changed, err = tx.ExecContext(ctx, `UPDATE codex_jsonl_files SET status=? WHERE id=? AND committed_offset=?`, priorDisposition, b.PriorID, b.PriorOffset); err != nil {
				return out, err
			}
			if n, _ := changed.RowsAffected(); n != 1 {
				return out, errors.New("store: Codex 파일 세대 경계 충돌")
			}
		}
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0)+1 FROM codex_jsonl_files WHERE path=?`, b.Path).Scan(&generation); err != nil {
			return out, err
		}
		result, e := tx.ExecContext(ctx, `INSERT INTO codex_jsonl_files
(path,generation,os_file_id,owner_session_key,parent_session_key,owner_source,committed_offset,committed_prefix_hash,observed_size,observed_mod_ns,status,observed_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, b.Path, generation, b.OSFileID, b.Owner, b.Parent, b.OwnerSource, b.CommittedOffset, b.PrefixHash, b.ObservedSize, b.ObservedModNS, b.Status, time.Now().Unix())
		if e != nil {
			return out, e
		}
		fileID, e = result.LastInsertId()
		if e != nil {
			return out, e
		}
	} else {
		var oldOffset int64
		var oldOS, oldOwner string
		if err = tx.QueryRowContext(ctx, `SELECT generation,committed_offset,os_file_id,owner_session_key FROM codex_jsonl_files WHERE id=?`, b.PriorID).Scan(&generation, &oldOffset, &oldOS, &oldOwner); err != nil {
			return out, err
		}
		if oldOffset != b.PriorOffset || oldOS != b.OSFileID || oldOwner != b.Owner {
			return out, errors.New("store: Codex 파일 체크포인트 충돌")
		}
		fileID = b.PriorID
		if _, err = tx.ExecContext(ctx, `UPDATE codex_jsonl_files SET path=?,committed_offset=?,committed_prefix_hash=?,observed_size=?,observed_mod_ns=?,status=?,observed_at=? WHERE id=?`, b.Path, b.CommittedOffset, b.PrefixHash, b.ObservedSize, b.ObservedModNS, b.Status, time.Now().Unix(), fileID); err != nil {
			return out, err
		}
	}
	for _, r := range b.Records {
		var body any = r.Body
		if body == "" {
			body = nil
		}
		if !d.cfg.storeContent {
			body = nil
			r.BodyHash = ""
		}
		var tombstone sql.NullInt64
		if err = tx.QueryRowContext(ctx, `SELECT before_at FROM codex_content_tombstones WHERE owner_session_key=?`, b.Owner).Scan(&tombstone); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if tombstone.Valid && (r.EventTime == 0 || r.EventTime < tombstone.Int64) {
			body = nil
			r.BodyHash = ""
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO codex_jsonl_records
(file_id,start_offset,end_offset,record_type,event_time,message_id,turn_id,body,body_hash,completeness,completeness_evidence,structure_evidence,representation_key)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(file_id,start_offset) DO NOTHING`, fileID, r.StartOffset, r.EndOffset, r.Type, nullInt64Value(r.EventTime), r.MessageID, r.TurnID, body, r.BodyHash, r.Completeness, r.CompletenessEvidence, r.Structure, r.RepresentationKey); err != nil {
			return out, err
		}
	}
	for _, lineErr := range b.Errors {
		if _, err = tx.ExecContext(ctx, `INSERT INTO codex_jsonl_errors(file_id,start_offset,end_offset,error_kind,diagnostic,observed_at)
VALUES (?,?,?,?,?,?) ON CONFLICT(file_id,start_offset) DO NOTHING`, fileID, lineErr.StartOffset, lineErr.EndOffset, lineErr.Kind, lineErr.Diagnostic, time.Now().Unix()); err != nil {
			return out, err
		}
	}
	if len(b.Records) > 0 || len(b.Errors) > 0 {
		// 새 구조 근거가 기존 완료 분류와 충돌할 수도 있으므로 모든 해당
		// 소유자 턴을 다시 본다. purge된 확정 라벨은 원문 삭제 계약상 유지한다.
		if _, err = tx.ExecContext(ctx, `UPDATE codex_turn_provenance SET processing_state='retrying'
WHERE turn_id IN (SELECT t.id FROM turns t JOIN sessions s ON s.id=t.session_id
 WHERE s.vendor_id='codex' AND s.session_key=? AND NOT (t.content_purged=1 AND codex_turn_provenance.processing_state='finalized' AND codex_turn_provenance.label!='unknown'))`, b.Owner); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO codex_pending(turn_id,next_check_at)
SELECT t.id,0 FROM turns t JOIN sessions s ON s.id=t.session_id
WHERE s.vendor_id='codex' AND s.session_key=? AND EXISTS (SELECT 1 FROM codex_turn_provenance p WHERE p.turn_id=t.id AND (p.label='unknown' OR p.processing_state!='finalized'))
ON CONFLICT(turn_id) DO UPDATE SET next_check_at=0`, b.Owner); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out = CodexFileState{ID: fileID, Generation: generation, Path: b.Path, OSFileID: b.OSFileID, Owner: b.Owner, Parent: b.Parent, OwnerSource: b.OwnerSource, CommittedOffset: b.CommittedOffset, PrefixHash: b.PrefixHash, ObservedSize: b.ObservedSize, ObservedModNS: b.ObservedModNS, Status: b.Status}
	return out, nil
}

func nullInt64Value(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func (d *DB) ListCodexPending(ctx context.Context, now int64, limit int) ([]CodexPromptRow, error) {
	if limit <= 0 || limit > 64 {
		limit = 64
	}
	// 각 행을 256 KiB 이내로 제한하므로 64개 pending을 한 번에 읽어도
	// 원본 OTel 문자열이 보강 캐시를 무한히 키우지 않는다. 잘라서 판정하지 않고
	// 초과 행은 별도 플래그로 retrying 진단만 남긴다.
	const maxPendingEvidence = 256 << 10
	const evidenceBytes = `length(CAST(s.session_key AS BLOB))+length(CAST(COALESCE(t.prompt_message_id,'') AS BLOB))+
length(CAST(COALESCE(t.prompt_source_turn_id,'') AS BLOB))+length(CAST(COALESCE(t.prompt_text,'') AS BLOB))+
length(CAST(t.prompt_completeness AS BLOB))+length(CAST(COALESCE(t.prompt_completeness_evidence,'') AS BLOB))`
	bounded := func(value string) string {
		return `CASE WHEN ` + evidenceBytes + `>? THEN '' ELSE ` + value + ` END`
	}
	query := `SELECT t.id,` + bounded(`s.session_key`) + `,` + bounded(`COALESCE(t.prompt_message_id,'')`) + `,` + bounded(`COALESCE(t.prompt_source_turn_id,'')`) + `,
` + bounded(`COALESCE(t.prompt_text,'')`) + `,` + bounded(`t.prompt_completeness`) + `,` + bounded(`COALESCE(t.prompt_completeness_evidence,'')`) + `,
COALESCE(t.started_at,s.started_at,0),COALESCE(t.ended_at,s.ended_at,0),
COALESCE((SELECT MIN(next_turn.started_at) FROM turns next_turn WHERE next_turn.session_id=t.session_id
 AND next_turn.turn_index>t.turn_index),0),
EXISTS(SELECT 1 FROM turns sibling WHERE sibling.session_id=t.session_id AND sibling.id!=t.id
 AND sibling.turn_index IS NOT NULL AND sibling.started_at=t.started_at),
t.prompt_conflict,t.content_purged,
'{}',p.processing_state,p.label,(` + evidenceBytes + `>?)
FROM codex_pending q JOIN turns t ON t.id=q.turn_id JOIN sessions s ON s.id=t.session_id
JOIN codex_turn_provenance p ON p.turn_id=t.id
WHERE s.vendor_id='codex' AND q.next_check_at<=? ORDER BY q.next_check_at,t.id LIMIT ?`
	rows, err := d.db.QueryContext(ctx, query, maxPendingEvidence, maxPendingEvidence, maxPendingEvidence,
		maxPendingEvidence, maxPendingEvidence, maxPendingEvidence, maxPendingEvidence, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CodexPromptRow, 0, limit)
	for rows.Next() {
		var p CodexPromptRow
		if err = rows.Scan(&p.ID, &p.Owner, &p.MessageID, &p.TurnID, &p.Body, &p.Completeness, &p.CompletenessEvidence, &p.StartedAt, &p.EndedAt, &p.NextStartedAt, &p.SameSecond, &p.Conflict, &p.Purged, &p.Source, &p.ProcessingState, &p.Label, &p.TooLarge); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CodexCandidateRecords는 ID/본문으로 후보를 먼저 좁힌 뒤 최대 8 MiB만 읽는다.
// 긴 세션의 무관한 행은 한도에 포함하지 않는다. 후보 자체가 넘치면 unknown을 남긴다.
func (d *DB) CodexCandidateRecords(ctx context.Context, prompt CodexPromptRow) ([]CodexRecord, bool, error) {
	hash := sha256.Sum256([]byte(prompt.Body))
	const eligible = `r.event_time IS NOT NULL AND (?=0 OR r.event_time>=?*1000000000)
AND (?=0 OR r.event_time<(?+1)*1000000000) AND (?=0 OR r.event_time<?*1000000000)`
	nextUpper := prompt.NextStartedAt
	if nextUpper <= prompt.StartedAt {
		nextUpper = 0 // 초 정밀도가 같으면 명시 ID만 허용하고 본문 구간은 판단하지 않는다.
	}
	window := []any{prompt.StartedAt, prompt.StartedAt, prompt.EndedAt, prompt.EndedAt, nextUpper, nextUpper}
	has := func(field, value string) (bool, error) {
		if value == "" {
			return false, nil
		}
		var yes int
		err := d.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM codex_jsonl_records r JOIN codex_jsonl_files f ON f.id=r.file_id
WHERE f.owner_session_key=? AND f.status IN ('active','rotated') AND `+eligible+` AND r.`+field+`=? AND json_extract(r.structure_evidence,'$.form') IS NOT NULL)`, append([]any{prompt.Owner}, append(window, value)...)...).Scan(&yes)
		return yes == 1, err
	}
	method := "body"
	if yes, err := has("message_id", prompt.MessageID); err != nil {
		return nil, false, err
	} else if yes {
		method = "message"
	}
	if method == "body" {
		if yes, err := has("turn_id", prompt.TurnID); err != nil {
			return nil, false, err
		} else if yes {
			method = "turn"
		}
	}
	filter := "0"
	args := []any{}
	switch method {
	case "message":
		filter = `r.message_id=?`
		args = append(args, prompt.MessageID)
	case "turn":
		filter = `r.turn_id=?`
		args = append(args, prompt.TurnID)
	default:
		if prompt.Body != "" && prompt.StartedAt != 0 && !prompt.SameSecond && (prompt.NextStartedAt == 0 || prompt.NextStartedAt > prompt.StartedAt) {
			filter = `(r.body_hash=? OR (?='truncated' AND r.completeness='complete' AND substr(CAST(r.body AS BLOB),1,?)=CAST(? AS BLOB)))`
			args = append(args, hex.EncodeToString(hash[:]), prompt.Completeness, len(prompt.Body), prompt.Body)
		}
	}
	const maxCandidateBytes = 8 << 20
	const recordBytes = `length(CAST(r.record_type AS BLOB))+length(CAST(r.message_id AS BLOB))+
length(CAST(r.turn_id AS BLOB))+length(CAST(COALESCE(r.body,'') AS BLOB))+
length(CAST(r.body_hash AS BLOB))+length(CAST(r.completeness AS BLOB))+
length(CAST(r.completeness_evidence AS BLOB))+length(CAST(r.structure_evidence AS BLOB))+
length(CAST(r.representation_key AS BLOB))`
	bounded := func(value string) string {
		return `CASE WHEN ` + recordBytes + `>? THEN '' ELSE ` + value + ` END`
	}
	query := `SELECT r.id,r.file_id,r.start_offset,r.end_offset,` + bounded(`r.record_type`) + `,COALESCE(r.event_time,0),
` + bounded(`r.message_id`) + `,` + bounded(`r.turn_id`) + `,` + bounded(`COALESCE(r.body,'')`) + `,` + bounded(`r.body_hash`) + `,
` + bounded(`r.completeness`) + `,` + bounded(`r.completeness_evidence`) + `,` + bounded(`r.structure_evidence`) + `,` + bounded(`r.representation_key`) + `,(` + recordBytes + `>?)
FROM codex_jsonl_records r JOIN codex_jsonl_files f ON f.id=r.file_id
WHERE f.owner_session_key=? AND f.status IN ('active','rotated') AND (r.record_type='session_meta' OR (` + eligible + ` AND ` + filter + `))
ORDER BY r.id LIMIT 257`
	queryArgs := []any{maxCandidateBytes, maxCandidateBytes, maxCandidateBytes, maxCandidateBytes,
		maxCandidateBytes, maxCandidateBytes, maxCandidateBytes, maxCandidateBytes, maxCandidateBytes, maxCandidateBytes, prompt.Owner}
	queryArgs = append(queryArgs, window...)
	queryArgs = append(queryArgs, args...)
	rows, err := d.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := make([]CodexRecord, 0, 64)
	bytes := 0
	overflow := false
	for rows.Next() {
		var r CodexRecord
		var oversized bool
		if err = rows.Scan(&r.ID, &r.FileID, &r.StartOffset, &r.EndOffset, &r.Type, &r.EventTime, &r.MessageID, &r.TurnID, &r.Body, &r.BodyHash, &r.Completeness, &r.CompletenessEvidence, &r.Structure, &r.RepresentationKey, &oversized); err != nil {
			return nil, false, err
		}
		if oversized {
			overflow = true
			break
		}
		bytes += len(r.Type) + len(r.MessageID) + len(r.TurnID) + len(r.Body) + len(r.BodyHash) + len(r.Completeness) + len(r.CompletenessEvidence) + len(r.Structure) + len(r.RepresentationKey)
		if len(out) >= 256 || bytes > 8<<20 {
			overflow = true
			break
		}
		if r.Type == "session_meta" {
			var meta map[string]any
			_ = json.Unmarshal([]byte(r.Structure), &meta)
			if s, ok := meta["session_meta_id"].(string); ok {
				r.SessionMetaID = s
			}
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	return out, overflow, nil
}

func (d *DB) CommitCodexDecision(ctx context.Context, epoch int64, b CodexDecision) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err = checkCodexEpoch(ctx, tx, epoch); err != nil {
		return err
	}
	var linked any
	if b.LinkedRecordID != 0 {
		linked = b.LinkedRecordID
	}
	if _, err = tx.ExecContext(ctx, `UPDATE codex_turn_provenance SET label=?,processing_state=?,link_state=?,link_method=?,linked_record_id=?,
classifier_version=?,structure_evidence=?,reason=?,checked_at=? WHERE turn_id=?`, b.Label, b.ProcessingState, b.LinkState, b.LinkMethod, linked, b.ClassifierVersion, b.StructureEvidence, b.Reason, time.Now().Unix(), b.TurnID); err != nil {
		return err
	}
	if b.NextCheckAt == 0 {
		_, err = tx.ExecContext(ctx, `DELETE FROM codex_pending WHERE turn_id=?`, b.TurnID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE codex_pending SET next_check_at=?,attempts=attempts+1,last_error='' WHERE turn_id=?`, b.NextCheckAt, b.TurnID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) CodexWorkerStatus(ctx context.Context) (epoch int64, status, reason string, heartbeat, jobStarted int64, err error) {
	err = d.db.QueryRowContext(ctx, `SELECT epoch,status,reason,heartbeat_at,job_started_at FROM codex_worker_state WHERE id=1`).Scan(&epoch, &status, &reason, &heartbeat, &jobStarted)
	return
}
