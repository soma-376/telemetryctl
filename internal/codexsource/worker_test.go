package codexsource

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/store"
)

func workerFixture(t *testing.T, content bool) (*store.DB, Worker, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(root, "store.db"), store.WithContentStorage(content))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	jsonl := filepath.Join(root, "sessions")
	if err := os.Mkdir(jsonl, 0o700); err != nil {
		t.Fatal(err)
	}
	epoch, err := db.BeginCodexWorkerEpoch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return db, Worker{DB: db, Root: jsonl, Epoch: epoch}, jsonl
}

func addPrompt(t *testing.T, db *store.DB, id int, owner, body, message, turn string, started, ended int64) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO vendors(vendor,first_seen,last_seen,status) VALUES ('codex',?,?,'enabled') ON CONFLICT(vendor) DO NOTHING`, []any{started, ended}},
		{`INSERT INTO sessions(vendor_id,session_key,started_at,ended_at) VALUES ('codex',?,?,?) ON CONFLICT(vendor_id,session_key) DO NOTHING`, []any{owner, started, ended}},
		{`INSERT INTO turns(id,session_id,turn_key,turn_index,started_at,ended_at,prompt_text,prompt_message_id,prompt_source_turn_id,prompt_completeness)
VALUES (?,(SELECT id FROM sessions WHERE vendor_id='codex' AND session_key=?),?,?,?,?,?,?,?,'unknown')`, []any{id, owner, turn, id, started, ended, body, message, turn}},
		{`INSERT INTO codex_turn_provenance(turn_id,label,processing_state,link_state) VALUES (?,'unknown','pending','not_attempted')`, []any{id}},
		{`INSERT INTO codex_pending(turn_id) VALUES (?)`, []any{id}},
	}
	for _, statement := range statements {
		if _, err := db.SQL().Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("fixture insert: %v", err)
		}
	}
}

func writeRows(t *testing.T, path string, rows ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func promptState(t *testing.T, db *store.DB, id int) (string, string, string, int) {
	t.Helper()
	var label, state, link string
	var pending int
	err := db.SQL().QueryRow(`SELECT p.label,p.processing_state,p.link_state,
(SELECT COUNT(*) FROM codex_pending q WHERE q.turn_id=p.turn_id)
FROM codex_turn_provenance p WHERE p.turn_id=?`, id).Scan(&label, &state, &link, &pending)
	if err != nil {
		t.Fatal(err)
	}
	return label, state, link, pending
}

func TestWorkerRealFileClassifiesOnlyExistingTurn(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
	path := filepath.Join(root, "owner.jsonl")
	writeRows(t, path,
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.300Z","payload":{"type":"user_message","message":"not an OTel turn","message_id":"m2"}}`,
	)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, link, pending := promptState(t, db, 1)
	if label != "client_submitted" || state != "finalized" || link != "unique" || pending != 0 {
		t.Fatalf("분류 = %s/%s/%s pending=%d", label, state, link, pending)
	}
	var turns, calls, provenance, records int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM turns`).Scan(&turns)
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM llm_calls`).Scan(&calls)
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_turn_provenance`).Scan(&provenance)
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&records)
	if turns != 1 || calls != 0 || provenance != 1 || records != 3 {
		t.Fatalf("JSONL이 OTel 없는 턴·호출·분류 작업을 만들거나 기록을 잃음: turns=%d calls=%d provenance=%d records=%d", turns, calls, provenance, records)
	}
}

func TestWorkerConcurrentNotificationsProcessFilesSequentially(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	for id, owner := range []string{"owner-a", "owner-b", "owner-c"} {
		id++
		message := fmt.Sprintf("m%d", id)
		turn := fmt.Sprintf("t%d", id)
		addPrompt(t, db, id, owner, "hello", message, turn, 1_700_000_000, 1_700_000_000)
		writeRows(t, filepath.Join(root, owner+".jsonl"),
			fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"source":"cli"}}`, owner),
			fmt.Sprintf(`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":%q,"turn_id":%q}}`, message, turn),
		)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	notifications := make(chan struct{}, 1)
	var classifications atomic.Int32
	worker.Notify = notifications
	worker.ScanInterval = time.Hour
	worker.PhaseHook = func(phase string) {
		if phase != "during_classification" {
			return
		}
		if classifications.Add(1) == 1 {
			close(entered)
			<-release
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		releaseOnce.Do(func() { close(release) })
		cancel()
	}()
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("A 분류 전에 워커 종료: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("A 분류 장벽 진입 없음")
	}

	var senders sync.WaitGroup
	for range 8 {
		senders.Add(1)
		go func() {
			defer senders.Done()
			for range 32 {
				select {
				case notifications <- struct{}{}:
				default:
				}
			}
		}()
	}
	senders.Wait()
	select {
	case <-time.After(100 * time.Millisecond):
	case err := <-done:
		t.Fatalf("A 정지 중 워커 종료: %v", err)
	}
	if got := classifications.Load(); got != 1 {
		t.Fatalf("A 정지 중 B/C가 분류에 진입함: count=%d", got)
	}
	for _, id := range []int{2, 3} {
		_, state, _, pending := promptState(t, db, id)
		if (state != "pending" && state != "retrying") || pending != 1 {
			t.Fatalf("A 정지 중 후속 턴 %d 상태=%s pending=%d", id, state, pending)
		}
	}

	releaseOnce.Do(func() { close(release) })
	deadline := time.After(10 * time.Second)
	for {
		complete := true
		for _, id := range []int{1, 2, 3} {
			label, state, _, pending := promptState(t, db, id)
			if label != "client_submitted" || state != "finalized" || pending != 0 {
				complete = false
			}
		}
		if complete {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("모든 파일 처리 전 워커 종료: %v", err)
		case <-deadline:
			t.Fatal("알림 합치기 후 B/C 처리 지연")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if got := classifications.Load(); got != 3 {
		t.Fatalf("분류 진입 횟수=%d, want 3", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("정상 워커 종료 지연")
	}
}

func TestWorkerArrivalOrderConvergesWithoutNotification(t *testing.T) {
	for _, order := range []string{"otel-first", "jsonl-first", "jsonl-delayed"} {
		t.Run(order, func(t *testing.T) {
			db, worker, root := workerFixture(t, true)
			path := filepath.Join(root, "owner.jsonl")
			meta := `{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`
			user := `{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}`
			if order == "jsonl-first" {
				writeRows(t, path, meta, user)
				if err := worker.RunOnce(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
			if order == "otel-first" {
				if err := worker.RunOnce(context.Background()); err != nil {
					t.Fatal(err)
				}
				_, state, _, pending := promptState(t, db, 1)
				if state != "pending" || pending != 1 {
					t.Fatalf("알림 없이 근거 대기 = %s pending=%d", state, pending)
				}
				writeRows(t, path, meta, user)
			} else if order == "jsonl-delayed" {
				writeRows(t, path, meta)
				if err := worker.RunOnce(context.Background()); err != nil {
					t.Fatal(err)
				}
				label, state, _, pending := promptState(t, db, 1)
				if label != "unknown" || state != "finalized" || pending != 1 {
					t.Fatalf("현재 완결된 근거의 unknown 재검토 = %s/%s pending=%d", label, state, pending)
				}
				f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString(user + "\n")
				closeErr := f.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("append: %v %v", err, closeErr)
				}
			}
			if err := worker.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			label, state, link, pending := promptState(t, db, 1)
			if label != "client_submitted" || state != "finalized" || link != "unique" || pending != 0 {
				t.Fatalf("도착 순서 %s: %s/%s/%s pending=%d", order, label, state, link, pending)
			}
			var turns, calls, records int
			_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM turns`).Scan(&turns)
			_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM llm_calls`).Scan(&calls)
			_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&records)
			if turns != 1 || calls != 0 || records != 2 {
				t.Fatalf("JSONL이 턴/호출을 생성하거나 중복 저장함: turns=%d calls=%d records=%d", turns, calls, records)
			}
		})
	}
}

func TestWorkerActiveBodyOnlyTurnsUseOpenIntervalUntilNextTurn(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "repeat", "", "t1", 1_700_000_000, 1_700_000_000)
	addPrompt(t, db, 2, "owner", "repeat", "", "t2", 1_700_000_010, 1_700_000_010)
	if _, err := db.SQL().Exec(`UPDATE turns SET ended_at=NULL; UPDATE sessions SET ended_at=NULL`); err != nil {
		t.Fatal(err)
	}
	writeRows(t, filepath.Join(root, "owner.jsonl"),
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:22.250Z","payload":{"type":"user_message","message":"repeat"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:30.250Z","payload":{"type":"user_message","message":"repeat"}}`,
	)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1, 2} {
		label, state, link, pending := promptState(t, db, id)
		if label != "client_submitted" || state != "finalized" || link != "unique" || pending != 0 {
			t.Fatalf("진행 중 턴 %d = %s/%s/%s pending=%d", id, label, state, link, pending)
		}
	}
	var firstRecord, secondRecord int64
	if err := db.SQL().QueryRow(`SELECT linked_record_id FROM codex_turn_provenance WHERE turn_id=1`).Scan(&firstRecord); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL().QueryRow(`SELECT linked_record_id FROM codex_turn_provenance WHERE turn_id=2`).Scan(&secondRecord); err != nil {
		t.Fatal(err)
	}
	if firstRecord == secondRecord {
		t.Fatal("서로 다른 반복 제출이 같은 JSONL 메시지로 연결됨")
	}
}

func TestWorkerSameSecondTurnsRequireExplicitIdentity(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "repeat", "m1", "t1", 1_700_000_000, 1_700_000_000)
	addPrompt(t, db, 2, "owner", "repeat", "", "t2", 1_700_000_000, 1_700_000_000)
	if _, err := db.SQL().Exec(`UPDATE turns SET ended_at=NULL; UPDATE sessions SET ended_at=NULL`); err != nil {
		t.Fatal(err)
	}
	writeRows(t, filepath.Join(root, "owner.jsonl"),
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"repeat","message_id":"m1"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.750Z","payload":{"type":"user_message","message":"repeat"}}`,
	)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, link, _ := promptState(t, db, 1)
	if label != "client_submitted" || state != "finalized" || link != "unique" {
		t.Fatalf("같은 초 명시 ID = %s/%s/%s", label, state, link)
	}
	label, state, link, _ = promptState(t, db, 2)
	if label != "unknown" || state != "finalized" || link != "unmatched" {
		t.Fatalf("같은 초 본문만 있는 턴을 추정함: %s/%s/%s", label, state, link)
	}
}

func TestWorkerStrongIDSurvivesLongSessionAndWeakDuplicates(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "repeat", "strong-message", "strong-turn", 1_700_000_000, 1_700_000_000)
	rows := []string{`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`}
	for i := range 350 {
		rows = append(rows, fmt.Sprintf(`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"repeat","message_id":"weak-%d","turn_id":"weak-%d"}}`, i, i))
	}
	rows = append(rows, `{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"repeat","message_id":"strong-message","turn_id":"strong-turn"}}`)
	writeRows(t, filepath.Join(root, "owner.jsonl"), rows...)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, link, pending := promptState(t, db, 1)
	if label != "client_submitted" || state != "finalized" || link != "unique" || pending != 0 {
		t.Fatalf("강한 ID가 약한 후보에 묻힘: %s/%s/%s pending=%d", label, state, link, pending)
	}
	var count, checkpoint int64
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&count)
	_ = db.SQL().QueryRow(`SELECT committed_offset FROM codex_jsonl_files WHERE path=?`, filepath.Join(root, "owner.jsonl")).Scan(&checkpoint)
	info, err := os.Stat(filepath.Join(root, "owner.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if count != int64(len(rows)) || checkpoint != info.Size() {
		t.Fatalf("긴 파일 보관/체크포인트 count=%d/%d offset=%d/%d", count, len(rows), checkpoint, info.Size())
	}
}

func TestWorkerPartialUTF8AndCorruptCompleteRow(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "한글", "m1", "t1", 1_700_000_000, 1_700_000_000)
	path := filepath.Join(root, "owner.jsonl")
	first := `{"type":"session_meta","payload":{"id":"owner","source":"cli"}}` + "\n"
	valid := `{"type":"event_msg","timestamp":"2023-11-14T22:13:20.999Z","payload":{"type":"user_message","message":"한글","message_id":"m1","turn_id":"t1"}}` + "\n"
	cut := strings.Index(valid, "한글") + 1
	if err := os.WriteFile(path, append([]byte(first), []byte(valid[:cut])...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, state, _, pending := promptState(t, db, 1)
	if state != "pending" || pending != 1 {
		t.Fatalf("부분 행 상태 = %s pending=%d", state, pending)
	}
	var checkpoint int64
	_ = db.SQL().QueryRow(`SELECT committed_offset FROM codex_jsonl_files WHERE path=?`, path).Scan(&checkpoint)
	if checkpoint != int64(len(first)) {
		t.Fatalf("부분 UTF-8 체크포인트 = %d, want %d", checkpoint, len(first))
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	structureLimit := `{"type":"event_msg","payload":{"items":[` + strings.Repeat(`{},`, 20_000) + `{}]}}` + "\n"
	goodTail := `{"type":"event_msg","timestamp":"2023-11-14T22:13:20.999Z","payload":{"type":"tool_note","text":"after-errors"}}` + "\n"
	_, err = f.Write([]byte(valid[cut:] + "{bad json}\n" + structureLimit + goodTail))
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("append/close: %v %v", err, closeErr)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, link, pending := promptState(t, db, 1)
	if label != "client_submitted" || state != "finalized" || link != "unique" || pending != 0 {
		t.Fatalf("완성 행 판정 = %s/%s/%s pending=%d", label, state, link, pending)
	}
	var errors, records int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_errors`).Scan(&errors)
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&records)
	if errors != 2 || records != 3 {
		t.Fatalf("손상 행 독립 처리 = errors %d records %d", errors, records)
	}
	var structureError int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_errors WHERE error_kind='structure_limit'`).Scan(&structureError); err != nil || structureError != 1 {
		t.Fatalf("구조 한도 진단 = %d, %v", structureError, err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var after int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&after)
	if after != records {
		t.Fatalf("재스캔 중복: %d -> %d", records, after)
	}
}

func TestWorkerFourMiBLineAndSixtyFourRowBatchCheckpoint(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "unmatched", "", "", 1_700_000_000, 1_700_000_000)
	path := filepath.Join(root, "owner.jsonl")
	metadata := `{"type":"session_meta","payload":{"id":"owner","source":"cli"}}` + "\n"
	short := `{"type":"event_msg","payload":{"type":"tool_note","text":"short"}}` + "\n"
	largePrefix := `{"type":"event_msg","payload":{"type":"tool_note","text":"`
	largeSuffix := `"}}`
	large := largePrefix + strings.Repeat("x", maxJSONLLine-len(largePrefix)-len(largeSuffix)-1) + largeSuffix + "\n"
	oversized := strings.Repeat("x", maxJSONLLine) + "\n"
	tail := `{"type":"event_msg","payload":{"type":"tool_note","text":"partial"}}`
	content := metadata + strings.Repeat(short, 64) + large + oversized + tail
	if len(large) != maxJSONLLine {
		t.Fatalf("경계 행 크기 = %d", len(large))
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	commits := 0
	worker.PhaseHook = func(phase string) {
		if phase == "after_db_commit" {
			commits++
		}
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var offset int64
	var records, errs int
	_ = db.SQL().QueryRow(`SELECT committed_offset FROM codex_jsonl_files WHERE path=?`, path).Scan(&offset)
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&records)
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_errors`).Scan(&errs)
	if offset != int64(len(content)-len(tail)) || records != 66 || errs != 1 || commits < 3 {
		t.Fatalf("행/배치/부분 tail = offset %d/%d records=%d errors=%d commits=%d", offset, len(content)-len(tail), records, errs, commits)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\n")
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("tail 완성: %v %v", err, closeErr)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&records)
	if records != 67 {
		t.Fatalf("tail 재개 레코드 = %d", records)
	}
}

func TestWorkerOwnerCoverageAndRepairedQuarantine(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	worker.Now = func() time.Time { return time.Unix(1_700_000_100, 0) }
	addPrompt(t, db, 1, "owner", "missing", "", "", 1_700_000_000, 1_700_000_000)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, state, _, pending := promptState(t, db, 1)
	if state != "pending" || pending != 1 {
		t.Fatalf("소유 파일 없음 = %s pending=%d", state, pending)
	}
	path := filepath.Join(root, "owner.jsonl")
	writeRows(t, path, `{"type":"session_meta","payload":{}}`)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = db.SQL().QueryRow(`SELECT status FROM codex_jsonl_files ORDER BY id DESC LIMIT 1`).Scan(&status)
	if status != "quarantined" {
		t.Fatalf("손상 owner 격리 = %s", status)
	}
	writeRows(t, path, `{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, _, pending := promptState(t, db, 1)
	if label != "unknown" || state != "finalized" || pending != 1 {
		t.Fatalf("복구 owner의 완료 unknown = %s/%s pending=%d", label, state, pending)
	}
	var generations int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_files WHERE path=?`, path).Scan(&generations)
	if generations != 2 {
		t.Fatalf("수정된 격리 파일 새 세대 = %d", generations)
	}
}

func TestWorkerRepairedQuarantineCannotReuseEarlierUserEvidence(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "old", "m1", "t1", 1_700_000_000, 1_700_000_000)
	path := filepath.Join(root, "owner.jsonl")
	writeRows(t, path,
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"old","message_id":"m1","turn_id":"t1"}}`,
		`{"type":"turn_context",broken}`,
	)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeRows(t, path,
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"new","message_id":"m2","turn_id":"t2"}}`,
	)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, _, _ := promptState(t, db, 1)
	if label != "unknown" || state != "finalized" {
		t.Fatalf("격리 세대 근거 재사용: %s/%s", label, state)
	}
	var status string
	if err := db.SQL().QueryRow(`SELECT status FROM codex_jsonl_files WHERE generation=1 AND path=?`, path).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "superseded" {
		t.Fatalf("격리 세대 상태 = %s", status)
	}
}

func TestWorkerRenameAndTruncateCreateCorrectGeneration(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
	path := filepath.Join(root, "before.jsonl")
	rows := []string{`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`, `{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}`}
	writeRows(t, path, rows...)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var fileID int64
	_ = db.SQL().QueryRow(`SELECT id FROM codex_jsonl_files WHERE path=?`, path).Scan(&fileID)
	renamed := filepath.Join(root, "after.jsonl")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var renamedID int64
	_ = db.SQL().QueryRow(`SELECT id FROM codex_jsonl_files WHERE path=?`, renamed).Scan(&renamedID)
	if renamedID != fileID {
		t.Fatalf("동일 inode rename이 새 세대가 됨: %d -> %d", fileID, renamedID)
	}
	writeRows(t, renamed, rows[0])
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var generations int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_files`).Scan(&generations)
	if generations != 2 {
		t.Fatalf("truncate 새 세대 = %d", generations)
	}
	label, state, _, _ := promptState(t, db, 1)
	if label != "unknown" || state != "finalized" {
		t.Fatalf("truncate 후 이전 근거 사용 = %s/%s", label, state)
	}
	var previousStatus string
	if err := db.SQL().QueryRow(`SELECT status FROM codex_jsonl_files WHERE id=?`, fileID).Scan(&previousStatus); err != nil {
		t.Fatal(err)
	}
	if previousStatus != "superseded" {
		t.Fatalf("truncate 이전 상태 = %s", previousStatus)
	}
}

func TestWorkerRotationKeepsRenamedCheckpointWhenNewPathScannedFirst(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
	path := filepath.Join(root, "owner.jsonl")
	oldRows := []string{`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`, `{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}`}
	writeRows(t, path, oldRows...)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var oldID int64
	if err := db.SQL().QueryRow(`SELECT id FROM codex_jsonl_files WHERE path=?`, path).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	rotated := filepath.Join(root, "z-old.jsonl")
	if err := os.Rename(path, rotated); err != nil {
		t.Fatal(err)
	}
	writeRows(t, path, oldRows[0], `{"type":"event_msg","payload":{"type":"tool_note","text":"new file"}}`)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var files, records int
	var renamedID int64
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_files`).Scan(&files); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL().QueryRow(`SELECT id FROM codex_jsonl_files WHERE path=?`, rotated).Scan(&renamedID); err != nil {
		t.Fatal(err)
	}
	if files != 2 || records != 4 || renamedID != oldID {
		t.Fatalf("rotation 재스캔 중복: files=%d records=%d renamed=%d old=%d", files, records, renamedID, oldID)
	}
	label, state, _, _ := promptState(t, db, 1)
	if label != "client_submitted" || state != "finalized" {
		t.Fatalf("유효한 옛파일의 출처가 사라짐: %s/%s", label, state)
	}
}

func TestWorkerNoContentStorageAtJSONLCommit(t *testing.T) {
	db, worker, root := workerFixture(t, false)
	addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
	writeRows(t, filepath.Join(root, "owner.jsonl"),
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}`)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var body, hash *string
	if err := db.SQL().QueryRow(`SELECT body,body_hash FROM codex_jsonl_records WHERE message_id='m1'`).Scan(&body, &hash); err != nil {
		t.Fatal(err)
	}
	if body != nil || hash == nil || *hash != "" {
		t.Fatalf("원문 OFF에서 JSONL body/hash 남음: body=%v hash=%v", body, hash)
	}
}

func TestWorkerOversizedOTLPEvidenceStaysRetrying(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "body", strings.Repeat("m", 300<<10), "t1", 1_700_000_000, 1_700_000_000)
	writeRows(t, filepath.Join(root, "owner.jsonl"), `{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, link, pending := promptState(t, db, 1)
	if label != "unknown" || state != "retrying" || link != "unavailable" || pending != 1 {
		t.Fatalf("한도 초과 근거를 분류함: %s/%s/%s pending=%d", label, state, link, pending)
	}
}

func TestWorkerReclassifiesFinalClientWhenConflictingEvidenceArrives(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
	path := filepath.Join(root, "owner.jsonl")
	writeRows(t, path,
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}`)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, _, _ := promptState(t, db, 1)
	if label != "client_submitted" || state != "finalized" {
		t.Fatalf("초기 분류 = %s/%s", label, state)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.300Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1","source":"subagent"}}` + "\n")
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("append/close: %v %v", err, closeErr)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, link, pending := promptState(t, db, 1)
	if label != "unknown" || state != "finalized" || link != "ambiguous" || pending != 1 {
		t.Fatalf("새 충돌 근거 후 재분류 = %s/%s/%s pending=%d", label, state, link, pending)
	}
}

func TestWorkerOldEpochCannotCommitAfterRestart(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
	writeRows(t, filepath.Join(root, "owner.jsonl"),
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}`)
	nextEpoch, err := db.BeginCodexWorkerEpoch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); !errors.Is(err, store.ErrCodexWorkerEpoch) {
		t.Fatalf("늦은 이전 세대 commit = %v", err)
	}
	var files int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_files`).Scan(&files)
	if files != 0 {
		t.Fatalf("이전 세대 파일 기록 %d", files)
	}
	worker.Epoch = nextEpoch
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, _, _ := promptState(t, db, 1)
	if label != "client_submitted" || state != "finalized" {
		t.Fatalf("새 세대 복구 = %s/%s", label, state)
	}
}

func TestWorkerCrashHelper(t *testing.T) {
	if os.Getenv("PROJ173_WORKER_CRASH_HELPER") != "1" {
		return
	}
	epoch, err := strconv.ParseInt(os.Getenv("PROJ173_WORKER_EPOCH"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), os.Getenv("PROJ173_WORKER_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	barrier := os.Getenv("PROJ173_WORKER_PHASE")
	worker := Worker{DB: db, Root: os.Getenv("PROJ173_WORKER_ROOT"), Epoch: epoch,
		PhaseHook: func(phase string) {
			if phase == barrier {
				fmt.Println("BARRIER")
				time.Sleep(time.Hour)
			}
		}}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerProcessCrashBeforeAndAfterCommitRecovers(t *testing.T) {
	for _, phase := range []string{"waiting_for_db_write", "after_db_commit"} {
		t.Run(phase, func(t *testing.T) {
			db, worker, root := workerFixture(t, true)
			addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
			path := filepath.Join(root, "owner.jsonl")
			writeRows(t, path,
				`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
				`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}`)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerCrashHelper$")
			cmd.Env = append(os.Environ(), "PROJ173_WORKER_CRASH_HELPER=1", "PROJ173_WORKER_DB="+filepath.Join(filepath.Dir(root), "store.db"), "PROJ173_WORKER_ROOT="+root, "PROJ173_WORKER_EPOCH="+strconv.FormatInt(worker.Epoch, 10), "PROJ173_WORKER_PHASE="+phase)
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			ready := make(chan string, 1)
			go func() {
				line, _ := bufio.NewReader(out).ReadString('\n')
				ready <- line
			}()
			select {
			case line := <-ready:
				if line != "BARRIER\n" {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatalf("크래시 장벽 = %q", line)
				}
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatal("독립 워커가 commit 장벽에 도달하지 못함")
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			var files, records, turns int
			_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_files`).Scan(&files)
			_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&records)
			_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM turns`).Scan(&turns)
			wantFiles, wantRecords := 0, 0
			if phase == "after_db_commit" {
				wantFiles, wantRecords = 1, 2
			}
			if files != wantFiles || records != wantRecords || turns != 1 {
				t.Fatalf("commit 경계 상태: files=%d records=%d turns=%d", files, records, turns)
			}
			epoch, err := db.BeginCodexWorkerEpoch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			worker.Epoch = epoch
			if err := worker.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			label, state, _, pending := promptState(t, db, 1)
			if label != "client_submitted" || state != "finalized" || pending != 0 {
				t.Fatalf("재시작 복구 = %s/%s pending=%d", label, state, pending)
			}
			_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&records)
			if records != 2 {
				t.Fatalf("재처리 중복 JSONL = %d", records)
			}
		})
	}
}

func TestWorkerSeparateSQLiteWriterLockRetriesWithoutAdvancingCheckpoint(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
	writeRows(t, filepath.Join(root, "owner.jsonl"),
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}`)
	lock, err := sql.Open(store.DriverName, "file:"+filepath.ToSlash(filepath.Join(filepath.Dir(root), "store.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	lock.SetMaxOpenConns(1)
	conn, err := lock.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	locked := false
	worker.PhaseHook = func(phase string) {
		if phase != "waiting_for_db_write" || locked {
			return
		}
		if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
			t.Fatal(err)
		}
		locked = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = worker.RunOnce(ctx)
	if err == nil || time.Since(started) > 6*time.Second {
		t.Fatalf("독립 writer 잠금/취소 = %v elapsed=%v", err, time.Since(started))
	}
	if !locked {
		t.Fatal("체크포인트 저장 장벽에 도달하지 않았다")
	}
	if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	var files int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_files`).Scan(&files)
	if files != 0 {
		t.Fatalf("실패한 commit이 체크포인트를 전진시킴: %d", files)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, _, _ := promptState(t, db, 1)
	if label != "client_submitted" || state != "finalized" {
		t.Fatalf("잠금 해제 후 재시도 = %s/%s", label, state)
	}
}

func TestWorkerRepairedOwnerErrorDoesNotPoisonLaterUnknown(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "unmatched", "", "", 1_700_000_000, 1_700_000_000)
	path := filepath.Join(root, "owner.jsonl")
	writeRows(t, path,
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"turn_context","payload":`,
	)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var previousStatus string
	_ = db.SQL().QueryRow(`SELECT status FROM codex_jsonl_files ORDER BY id DESC LIMIT 1`).Scan(&previousStatus)
	if previousStatus != "quarantined" {
		t.Fatalf("필수 문맥 손상 격리 = %s", previousStatus)
	}
	writeRows(t, path, `{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, _, pending := promptState(t, db, 1)
	if label != "unknown" || state != "finalized" || pending != 1 {
		t.Fatalf("복구 세대의 정상 unknown = %s/%s pending=%d", label, state, pending)
	}
	var oldErrors int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_errors`).Scan(&oldErrors)
	if oldErrors != 1 {
		t.Fatalf("과거 손상 진단 유실 = %d", oldErrors)
	}
}

func TestWorkerDetectsChangedMiddleWithSameInodeAndSize(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
	path := filepath.Join(root, "owner.jsonl")
	rows := []string{`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`}
	for range 600 {
		rows = append(rows, `{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"tool_note","text":"middle-sentinel"}}`)
	}
	rows = append(rows, `{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}`)
	writeRows(t, path, rows...)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	middle := len(content) / 2
	at := bytes.Index(content[middle:], []byte("middle-sentinel")) + middle
	if at < 4096 || at >= len(content)-4096 {
		t.Fatalf("중간 변경 위치 = %d / %d", at, len(content))
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt([]byte("X"), int64(at))
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("middle rewrite: %v %v", err, closeErr)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var generations int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_files WHERE path=?`, path).Scan(&generations)
	if generations != 2 {
		t.Fatalf("동일 길이 중간 변경 새 세대 = %d", generations)
	}
}

func TestWorkerPurgeCannotRestoreOldJSONLContentOnRotation(t *testing.T) {
	db, worker, root := workerFixture(t, true)
	addPrompt(t, db, 1, "owner", "private prompt", "m1", "t1", 1_700_000_000, 1_700_000_000)
	rows := []string{
		`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}`,
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"private prompt","message_id":"m1","turn_id":"t1"}}`,
	}
	writeRows(t, filepath.Join(root, "first.jsonl"), rows...)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	label, state, _, _ := promptState(t, db, 1)
	if label != "client_submitted" || state != "finalized" {
		t.Fatalf("purge 전 분류 = %s/%s", label, state)
	}
	if _, err := db.PurgeContent(context.Background(), time.Time{}); err != nil {
		t.Fatal(err)
	}
	writeRows(t, filepath.Join(root, "rotated.jsonl"), rows...)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var storedBody, storedHash, turnBody *string
	var nonempty int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records WHERE body IS NOT NULL OR body_hash!=''`).Scan(&nonempty); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL().QueryRow(`SELECT body,body_hash FROM codex_jsonl_records WHERE message_id='m1' ORDER BY id DESC LIMIT 1`).Scan(&storedBody, &storedHash); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL().QueryRow(`SELECT prompt_text FROM turns WHERE id=1`).Scan(&turnBody); err != nil {
		t.Fatal(err)
	}
	label, state, _, _ = promptState(t, db, 1)
	if nonempty != 0 || storedBody != nil || storedHash == nil || *storedHash != "" || turnBody != nil || label != "client_submitted" || state != "finalized" {
		t.Fatalf("purge/rotation 재생성: nonempty=%d body=%v hash=%v turn=%v label=%s state=%s", nonempty, storedBody, storedHash, turnBody, label, state)
	}
}

func independentAppendBytes() []byte {
	return []byte(`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.300Z","payload":{"type":"user_message","message":"additional","message_id":"m2"}}` + "\n" +
		`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.350Z","payload":{"type":"tool_note","text":"한글"}}` + "\n")
}

func TestJSONLProducerHelper(t *testing.T) {
	path := os.Getenv("PROJ173_PRODUCER_PATH")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	data := independentAppendBytes()
	cut := bytes.Index(data, []byte("한글")) + 1
	if cut <= 0 {
		t.Fatal("UTF-8 분할 위치 없음")
	}
	if _, err := f.Write(data[:cut]); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data[cut:]); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerNeverBlocksIndependentProducerAtFourBarriers(t *testing.T) {
	for _, phase := range []string{"after_open", "during_read", "during_classification", "waiting_for_db_write"} {
		t.Run(phase, func(t *testing.T) {
			db, worker, root := workerFixture(t, true)
			addPrompt(t, db, 1, "owner", "hello", "m1", "t1", 1_700_000_000, 1_700_000_000)
			path := filepath.Join(root, "owner.jsonl")
			initial := []byte(`{"type":"session_meta","payload":{"id":"owner","source":"cli"}}` + "\n" +
				`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":"hello","message_id":"m1","turn_id":"t1"}}` + "\n")
			if err := os.WriteFile(path, initial, 0o600); err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			worker.PhaseHook = func(name string) {
				if name == phase {
					once.Do(func() { close(entered); <-release })
				}
			}
			workerDone := make(chan error, 1)
			go func() { workerDone <- worker.RunOnce(context.Background()) }()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatal("워커 barrier 진입 없음")
			}
			self, err := os.Executable()
			if err != nil {
				close(release)
				t.Fatal(err)
			}
			cmd := exec.Command(self, "-test.run=^TestJSONLProducerHelper$")
			cmd.Env = append(os.Environ(), "PROJ173_PRODUCER_PATH="+path)
			if err := cmd.Start(); err != nil {
				close(release)
				t.Fatal(err)
			}
			producerDone := make(chan error, 1)
			go func() { producerDone <- cmd.Wait() }()
			select {
			case err := <-producerDone:
				if err != nil {
					close(release)
					t.Fatalf("독립 생산자 append/flush/close: %v", err)
				}
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				close(release)
				t.Fatal("리더 해제 전 생산자 완료 실패")
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				close(release)
				t.Fatal(err)
			}
			if !bytes.Equal(contents, append(initial, independentAppendBytes()...)) {
				close(release)
				t.Fatal("워커 정지 중 원본 파일 바이트 변경 또는 생산자 쓰기 누락")
			}
			close(release)
			select {
			case err := <-workerDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("워커 재개 실패")
			}
			worker.PhaseHook = nil
			if err := worker.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			var count, errors int
			_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_records`).Scan(&count)
			_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_jsonl_errors`).Scan(&errors)
			if count != 4 || errors != 0 {
				t.Fatalf("재개 뒤 정확히 한 번 처리: records=%d errors=%d", count, errors)
			}
		})
	}
}
