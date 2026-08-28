package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/your-org/pulsemetry/internal/event"
)

// virtualTurnKey 는 세션 수준 이벤트를 담는 가상 턴의 turn_key 다.
//
// **벤더가 만들 수 없는 값이어야 한다.** 벤더가 준 prompt.id 와 부딪히면 가상 턴과 실제 턴이
// 한 행으로 합쳐지고, 그 순간 세션 수준 이벤트가 특정 턴의 것으로 둔갑한다.
// NUL 바이트는 어떤 벤더 식별자에도 들어가지 않는다.
const virtualTurnKey = "\x00virtual"

// hashChunk 는 record_hash 사전 조회 한 번에 넣을 파라미터 수다.
// SQLite 의 기본 변수 상한(SQLITE_MAX_VARIABLE_NUMBER)은 999 라 그 아래로 잡는다.
const hashChunk = 500

// writer 는 트랜잭션 하나 동안만 사는 상태다.
//
// 캐시가 전부 트랜잭션 범위인 것이 중요하다. DB 핸들에 붙여 두면 다른 트랜잭션이 롤백된 뒤에도
// 존재하지 않는 id 를 돌려주고, 그 id 로 만든 자식 행이 외래 키 위반으로 배치 전체를 죽인다.
type writer struct {
	ctx context.Context
	db  *DB
	tx  *sql.Tx
	res *WriteResult

	sessionIDs map[sessionRef]int64
	turnIDs    map[turnRef]int64
	// turnSeq 는 턴별 events.seq high-water mark 다. 트랜잭션마다 한 번만 조회한다.
	turnSeq map[int64]int64
	// nextIndex 는 세션별 다음 turn_index 다. 트랜잭션마다 한 번만 조회한다.
	nextIndex map[int64]int64
}

type sessionRef struct{ vendor, key string }

type turnRef struct {
	sessionID int64
	key       string
}

func newWriter(ctx context.Context, db *DB, tx *sql.Tx, res *WriteResult) *writer {
	return &writer{
		ctx: ctx, db: db, tx: tx, res: res,
		sessionIDs: map[sessionRef]int64{},
		turnIDs:    map[turnRef]int64{},
		turnSeq:    map[int64]int64{},
		nextIndex:  map[int64]int64{},
	}
}

// ── vendors ─────────────────────────────────────────────────────────────────

// upsertVendorSQL 은 관측 범위만 갱신한다.
//
// **status 는 ON CONFLICT 에서 건드리지 않는다.** 그 컬럼은 사용자가 Settings 화면에서
// 켜고 끄는 설정이지 우리가 관측한 사실이 아니다. 배치마다 덮어쓰면 사용자가 disabled 로
// 바꾼 벤더가 다음 이벤트 한 건에 enabled 로 되돌아가 토글이 동작하지 않는 것처럼 보인다.
const upsertVendorSQL = `INSERT INTO vendors (vendor, first_seen, last_seen, status)
VALUES (?,?,?,'enabled')
ON CONFLICT(vendor) DO UPDATE SET
  first_seen = MIN(vendors.first_seen, excluded.first_seen),
  last_seen  = MAX(vendors.last_seen,  excluded.last_seen)`

type vendorSpan struct{ first, last event.UnixSec }

func (w *writer) writeVendors(b Batch) error {
	order := make([]string, 0, 4)
	spans := map[string]*vendorSpan{}
	observe := func(vendor string, ts event.UnixSec) {
		if vendor == "" || ts <= 0 {
			return
		}
		s := spans[vendor]
		if s == nil {
			spans[vendor] = &vendorSpan{first: ts, last: ts}
			order = append(order, vendor)
			return
		}
		if ts < s.first {
			s.first = ts
		}
		if ts > s.last {
			s.last = ts
		}
	}

	for _, rec := range b.Events {
		observe(rec.Event.Vendor, rec.Event.TS.Sec())
	}
	for _, s := range b.Sessions {
		observe(s.Vendor, s.StartedAt)
		observe(s.Vendor, s.LastEventAt)
	}

	for _, v := range order {
		s := spans[v]
		if _, err := w.tx.ExecContext(w.ctx, upsertVendorSQL, v, int64(s.first), int64(s.last)); err != nil {
			return fmt.Errorf("store: vendors UPSERT (%s): %w", v, err)
		}
		w.res.VendorsTouched++
	}
	return nil
}

// ── sessions ────────────────────────────────────────────────────────────────

// upsertSessionSQL 은 (vendor_id, session_key) 로 대리 키를 얻는다.
//
// DO NOTHING 이 아니라 DO UPDATE 인 이유: DO NOTHING 은 충돌 시 행을 만들지 않으므로
// RETURNING 이 **아무 행도 주지 않는다**. 그러면 이미 있는 세션의 id 를 못 받고, 그 자리에서
// LastInsertId() 로 대신하면 직전에 삽입한 **다른** 행의 id 가 나온다 — 조용히 틀린 부모다.
//
// title 은 COALESCE(sessions.title, ...) 다. 스키마 문서가 "ETL 은 NULL 일 때만 기록" 이라고
// 못 박았다 — 조립기의 제목은 휴리스틱이고 매 스냅샷마다 바뀔 수 있어, 덮어쓰면 화면의 제목이
// 이유 없이 흔들린다. 나머지 식별 정보는 반대로 새 관측을 우선한다(처음엔 비어 있다가 나중에
// 리소스 속성이 도착하는 것이 정상 경로다).
const upsertSessionSQL = `INSERT INTO sessions (
  vendor_id, session_key, title, workspace_path, user_email, user_account_id,
  terminal_type, started_at, ended_at, active_time_sec
) VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(vendor_id, session_key) DO UPDATE SET
  title           = COALESCE(sessions.title, excluded.title),
  workspace_path  = COALESCE(excluded.workspace_path,  sessions.workspace_path),
  user_email      = COALESCE(excluded.user_email,      sessions.user_email),
  user_account_id = COALESCE(excluded.user_account_id, sessions.user_account_id),
  terminal_type   = COALESCE(excluded.terminal_type,   sessions.terminal_type),
  started_at      = MIN(COALESCE(excluded.started_at, sessions.started_at),
                        COALESCE(sessions.started_at, excluded.started_at)),
  ended_at        = COALESCE(excluded.ended_at, sessions.ended_at),
  active_time_sec = COALESCE(excluded.active_time_sec, sessions.active_time_sec)
RETURNING id`

// sessionSeed 는 sessions 한 행에 쓸 값이다. 이벤트에서 오는 최소 씨앗과 조립기 스냅샷의
// 완전한 값이 같은 문장을 타므로 순서에 관계없이 결과가 같다.
type sessionSeed struct {
	vendor, key string

	title         string
	workspacePath string
	userEmail     string
	userAccountID string
	terminalType  string

	startedAt  any
	endedAt    any
	activeTime any
}

func (s sessionSeed) args() []any {
	return []any{
		s.vendor, s.key, nullStr(s.title), nullStr(s.workspacePath),
		nullStr(s.userEmail), nullStr(s.userAccountID), nullStr(s.terminalType),
		s.startedAt, s.endedAt, s.activeTime,
	}
}

// writeSessions 는 조립기 스냅샷을 먼저 쓰고, 스냅샷에 없는 세션 키를 이벤트에서 채운다.
//
// 스냅샷이 먼저인 이유는 title 때문이다. 이벤트 씨앗이 먼저 들어가면 title 이 NULL 인 행이
// 만들어지고, 그 뒤 스냅샷의 COALESCE 가 제목을 채운다 — 결과는 같지만 한 배치 안에서
// UPDATE 가 한 번 더 돈다.
func (w *writer) writeSessions(b Batch) error {
	for _, s := range b.Sessions {
		if s.SessionID == "" {
			return errors.New("store: session_key 가 빈 세션")
		}
		if s.Vendor == "" {
			return fmt.Errorf("store: vendor 가 빈 세션 (%s)", s.SessionID)
		}
		var active any
		if s.ActiveSeconds > 0 {
			active = int64(s.ActiveSeconds)
		}
		seed := sessionSeed{
			vendor: s.Vendor, key: s.SessionID,
			title:         s.Title,
			workspacePath: s.WorkspacePath,
			userEmail:     s.UserEmail,
			userAccountID: s.UserAccountID,
			terminalType:  s.TerminalType,
			startedAt:     nullSec(s.StartedAt),
			endedAt:       optSec(s.EndedAt),
			activeTime:    active,
		}
		if _, err := w.sessionID(seed); err != nil {
			return err
		}
		w.res.SessionsUpserted++
	}

	for _, rec := range b.Events {
		e := rec.Event
		if e.SessionID == "" {
			continue
		}
		if _, ok := w.sessionIDs[sessionRef{e.Vendor, e.SessionID}]; ok {
			continue
		}
		seed := sessionSeed{
			vendor: e.Vendor, key: e.SessionID,
			workspacePath: e.Attr.WorkspacePath,
			userEmail:     e.Attr.UserEmail,
			userAccountID: e.Attr.UserAccountID,
			terminalType:  e.Attr.TerminalType,
			startedAt:     nullSec(e.TS.Sec()),
		}
		if _, err := w.sessionID(seed); err != nil {
			return err
		}
		w.res.SessionsUpserted++
	}
	return nil
}

func (w *writer) sessionID(seed sessionSeed) (int64, error) {
	ref := sessionRef{seed.vendor, seed.key}
	if id, ok := w.sessionIDs[ref]; ok {
		return id, nil
	}
	var id int64
	err := w.tx.QueryRowContext(w.ctx, upsertSessionSQL, seed.args()...).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: sessions UPSERT (%s/%s): %w", seed.vendor, seed.key, err)
	}
	w.sessionIDs[ref] = id
	return id, nil
}

// ── turns ───────────────────────────────────────────────────────────────────

// upsertTurnSQL 은 (session_id, turn_key) 로 대리 키를 얻는다.
//
// 가상 턴은 갱신할 것이 하나도 없다 — 그래도 DO UPDATE 가 필요하다. `turn_key =
// turns.turn_key` 라는 자기 대입을 두는 이유는 DO NOTHING 이면 RETURNING 이 아무 행도 주지
// 않아 id 를 못 받기 때문이다.
const upsertTurnSQL = `INSERT INTO turns (
  session_id, turn_key, turn_index, client_version, started_at, prompt_text
) VALUES (?,?,?,?,?,?)
ON CONFLICT(session_id, turn_key) DO UPDATE SET
  turn_key       = turns.turn_key,
  client_version = COALESCE(turns.client_version, excluded.client_version),
  started_at     = MIN(COALESCE(excluded.started_at, turns.started_at),
                       COALESCE(turns.started_at, excluded.started_at)),
  prompt_text    = COALESCE(turns.prompt_text, excluded.prompt_text)
RETURNING id`

const selectTurnSQL = `SELECT id FROM turns WHERE session_id = ? AND turn_key = ?`

// writeTurns 는 이벤트마다 붙을 턴을 확정하고 그 id 를 이벤트와 같은 순서로 돌려준다.
// session.id 가 없는 이벤트는 붙을 세션이 없으므로 0 이다 — events 는 turn_id 가 NOT NULL 이라
// 그런 이벤트를 저장할 수 없다.
func (w *writer) writeTurns(recs []EventRecord) ([]int64, error) {
	out := make([]int64, len(recs))
	for i, rec := range recs {
		e := rec.Event
		if err := e.Validate(); err != nil {
			// 경계에서 막는다. NOT NULL 위반이 SQL 에러로 뒤늦게 터지면 배치 전체가 날아간다.
			return nil, fmt.Errorf("store: 이벤트 검증: %w", err)
		}
		if e.SessionID == "" {
			continue
		}
		sid, ok := w.sessionIDs[sessionRef{e.Vendor, e.SessionID}]
		if !ok {
			return nil, fmt.Errorf("store: 세션 id 를 못 찾음 (%s/%s)", e.Vendor, e.SessionID)
		}
		id, err := w.turnID(sid, rec)
		if err != nil {
			return nil, err
		}
		out[i] = id
	}
	return out, nil
}

func (w *writer) turnID(sessionID int64, rec EventRecord) (int64, error) {
	key := rec.TurnKey
	virtual := key == ""
	if virtual {
		key = virtualTurnKey
	}

	ref := turnRef{sessionID, key}
	if id, ok := w.turnIDs[ref]; ok {
		return id, nil
	}

	// 이미 있는 턴이면 여기서 끝난다. UPSERT 로 바로 가지 않는 이유는 turn_index 때문이다 —
	// 삽입 시도마다 번호를 하나씩 태우면 배치가 돌 때마다 현재 열린 턴의 번호가 밀려
	// turn_index 에 커다란 구멍이 생긴다.
	var id int64
	err := w.tx.QueryRowContext(w.ctx, selectTurnSQL, sessionID, key).Scan(&id)
	switch {
	case err == nil:
		w.turnIDs[ref] = id
		return id, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("store: turns 조회 (session=%d): %w", sessionID, err)
	}

	// 새 턴이다. turn_index 는 세션별 단조 증가 카운터에서 뽑는다.
	//
	// UNIQUE 가 세 개(=(session_id,turn_key), (session_id,turn_index), ux_turns_virtual)라
	// ON CONFLICT 대상이 아닌 둘 중 하나에 걸리면 upsert 가 아니라 오류가 된다. 번호를
	// 카운터에서 뽑으면 (session_id,turn_index) 는 절대 부딪히지 않고, 가상 턴은 세션당
	// 하나뿐이라 캐시와 위 SELECT 가 ux_turns_virtual 을 앞서 막는다.
	var index any
	if !virtual {
		n, err := w.nextTurnIndex(sessionID)
		if err != nil {
			return 0, err
		}
		index = n
	}

	e := rec.Event
	prompt := w.promptText(rec)
	err = w.tx.QueryRowContext(w.ctx, upsertTurnSQL,
		sessionID, key, index, nullStr(e.Attr.AppVersion), nullSec(e.TS.Sec()), nullStr(prompt),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: turns UPSERT (session=%d): %w", sessionID, err)
	}
	if prompt != "" {
		w.res.PromptsStored++
	}
	w.turnIDs[ref] = id
	w.res.TurnsUpserted++
	return id, nil
}

// promptText 는 turns.prompt_text 에 쓸 사용자 프롬프트 원문이다.
//
// v3 에는 원문 테이블이 없다. 프롬프트만 여기 남고 나머지 원문(응답·tool_input·tool_result)은
// 저장될 자리가 없어 버려진다. --no-store-content 는 프롬프트까지 버린다 — 저장소가
// 프라이버시 모드의 집행 지점이라는 계약은 그대로다.
func (w *writer) promptText(rec EventRecord) string {
	body := ""
	for _, c := range rec.Contents {
		if c.Kind == event.ContentPrompt && w.db.cfg.storeContent {
			body = c.Body
			continue
		}
		w.res.ContentsDropped++
	}
	if body == "" {
		return ""
	}
	return body
}

func (w *writer) nextTurnIndex(sessionID int64) (int64, error) {
	if n, ok := w.nextIndex[sessionID]; ok {
		w.nextIndex[sessionID] = n + 1
		return n, nil
	}
	var n int64
	err := w.tx.QueryRowContext(w.ctx,
		`SELECT COALESCE(MAX(turn_index), -1) + 1 FROM turns WHERE session_id = ?`, sessionID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: turn_index 워터마크 조회 (session=%d): %w", sessionID, err)
	}
	w.nextIndex[sessionID] = n + 1
	return n, nil
}

// ── events ──────────────────────────────────────────────────────────────────

// insertEventSQL 은 payload 를 NULL 로 남긴다.
//
// v3 는 원본 OTLP 를 통째로 담는 catch-all 을 두지 않기로 한 ADR 0002·0003 의 결정을
// 그대로 잇는다. 어느 경로도 원본 바이트를 붙들고 있지 않으므로 채울 값 자체가 없다.
// 나중에 쓰게 되면 `jsonb(?)` 로 바인딩해야 한다 — CHECK 가 json_valid(payload, 8),
// 즉 텍스트 JSON 이 아니라 **JSONB** 를 요구한다.
const insertEventSQL = `INSERT INTO events (
  turn_id, seq, event_name, occurred_at, record_hash, payload
) VALUES (?,?,?,?,?,NULL)
ON CONFLICT(record_hash) DO NOTHING`

// writeEvents 는 중복을 걸러 낸 뒤 도착 순서대로 seq 를 매겨 넣는다.
// 돌려주는 id 슬라이스에서 0 은 "이번 트랜잭션이 넣지 않았다"(중복이거나 저장 불가)는 뜻이다.
//
// seq 는 **로컬 수집 도착 순서**다. occurred_at 으로 다시 정렬해 재번호하지 않는다 —
// 이미 저장된 행의 seq 를 바꾸면 (turn_id, seq) UNIQUE 가 중간 상태에서 깨지고, 무엇보다
// 독자가 ORDER BY occurred_at, seq 로 읽으므로 재번호가 아무것도 개선하지 않는다.
func (w *writer) writeEvents(recs []EventRecord, turnIDs []int64) ([]int64, error) {
	out := make([]int64, len(recs))
	if len(recs) == 0 {
		return out, nil
	}

	hashes := make([]string, len(recs))
	for i, rec := range recs {
		hashes[i] = rec.Event.DedupKey()
	}
	seen, err := w.existingHashes(hashes)
	if err != nil {
		return nil, err
	}

	stmt, err := w.tx.PrepareContext(w.ctx, insertEventSQL)
	if err != nil {
		return nil, fmt.Errorf("store: events INSERT 준비: %w", err)
	}
	defer stmt.Close()

	for i, rec := range recs {
		turnID := turnIDs[i]
		if turnID == 0 {
			// 붙을 턴이 없다. events.turn_id 가 NOT NULL 이라 저장할 자리가 없다.
			continue
		}
		if seen[hashes[i]] {
			w.res.EventsDuplicate++
			continue
		}
		// 같은 배치 안의 중복도 여기서 접는다. 사전 조회는 트랜잭션 시작 시점의 DB 만 본다.
		seen[hashes[i]] = true

		seq := w.nextSeq(turnID)
		e := rec.Event
		outRes, err := stmt.ExecContext(w.ctx, turnID, seq, e.Name, nullSec(e.TS.Sec()), hashes[i])
		if err != nil {
			return nil, fmt.Errorf("store: events INSERT (name=%q): %w", e.Name, err)
		}
		affected, err := outRes.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("store: events INSERT 결과 확인: %w", err)
		}
		if affected == 0 {
			// 사전 조회 뒤에 밝혀진 중복이다. 태운 seq 를 되돌린다 — 안 그러면 턴 안의
			// seq 에 이유 없는 구멍이 생긴다.
			w.turnSeq[turnID] = seq - 1
			w.res.EventsDuplicate++
			continue
		}
		id, err := outRes.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("store: events id 확인: %w", err)
		}
		out[i] = id
		w.res.EventsInserted++
	}
	return out, nil
}

// existingHashes 는 이미 저장된 record_hash 를 미리 모은다.
//
// 삽입 시도마다 seq 를 태우고 되돌리는 것보다 한 번에 걸러 내는 쪽이 싸다. 재전송이 흔한
// 경로(exporter 재시도·Windows+WSL 이중 설정)라 배치의 대부분이 중복인 경우가 드물지 않다.
func (w *writer) existingHashes(hashes []string) (map[string]bool, error) {
	seen := make(map[string]bool, len(hashes))
	for start := 0; start < len(hashes); start += hashChunk {
		end := min(start+hashChunk, len(hashes))
		chunk := hashes[start:end]

		args := make([]any, len(chunk))
		for i, h := range chunk {
			args[i] = h
		}
		query := `SELECT record_hash FROM events WHERE record_hash IN (` +
			strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",") + `)`

		rows, err := w.tx.QueryContext(w.ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("store: record_hash 사전 조회: %w", err)
		}
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: record_hash 사전 조회 읽기: %w", err)
			}
			seen[h] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: record_hash 사전 조회: %w", err)
		}
		rows.Close()
	}
	return seen, nil
}

// nextSeq 는 턴 안의 다음 도착 순번이다. 워터마크는 트랜잭션마다 턴별로 한 번만 조회한다.
//
// 조회가 실패하면 0 에서 시작한다. 그 경우 (turn_id, seq) UNIQUE 가 삽입을 막아 조용한
// 덮어쓰기 대신 눈에 보이는 실패가 된다.
func (w *writer) nextSeq(turnID int64) int64 {
	if n, ok := w.turnSeq[turnID]; ok {
		w.turnSeq[turnID] = n + 1
		return n + 1
	}
	var maxSeq int64
	_ = w.tx.QueryRowContext(w.ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM events WHERE turn_id = ?`, turnID).Scan(&maxSeq)
	w.turnSeq[turnID] = maxSeq + 1
	return maxSeq + 1
}
