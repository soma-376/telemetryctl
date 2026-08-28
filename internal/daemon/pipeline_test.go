package daemon

import (
	"context"
	"database/sql"
	"log"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/store"
)

// newTestPipeline 은 수신기 없이 파이프라인만 띄운다. 저장 실패·조립기 정리처럼
// HTTP 를 거치지 않고 봐야 하는 경로를 위한 것이다.
func newTestPipeline(t *testing.T, db *store.DB, logs *syncBuffer, now func() time.Time) *pipeline {
	t.Helper()
	p := newPipeline(pipelineConfig{
		DB:           db,
		Logger:       log.New(logs, "", 0),
		Now:          now,
		BatchEvents:  DefaultBatchEvents,
		QueueSize:    pipelineQueue,
		WriteTimeout: 2 * time.Second,
		PruneTimeout: 2 * time.Second,
		SessionTTL:   sessionMemoryTTL,
	})
	t.Cleanup(func() { p.close(time.Now().Add(5 * time.Second)) })
	return p
}

func walkthroughBatch(t *testing.T) receiver.Batch {
	t.Helper()
	body := fixture(t, "logs_session_walkthrough.json")
	res, err := otlpdecode.Decode(otlpdecode.PayloadLogs, body, otlpdecode.EncodingJSON,
		otlpdecode.Options{InstallationID: "inst-unit"})
	if err != nil {
		t.Fatalf("픽스처 디코드: %v", err)
	}
	if len(res.Events) == 0 {
		t.Fatal("픽스처가 이벤트를 만들지 않았다")
	}
	return receiver.Batch{
		Kind:       otlpdecode.PayloadLogs,
		Encoding:   otlpdecode.EncodingJSON,
		Body:       body,
		ReceivedAt: time.Unix(fixtureUnix, 0),
		Result:     res,
	}
}

func openTestStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "pulsemetry.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return db
}

// TestFlushFailureRetriesInsteadOfLosing 는 저장 실패가 치명적이지도, 조용하지도 않은지 본다.
//
// 실패한 배치는 다음 틱으로 넘어가고, 성공하면 그때 들어간다. 실패를 무시하고 버리면
// "왜 이벤트가 보낸 것보다 적은가" 를 아무도 설명하지 못한다.
func TestFlushFailureRetriesInsteadOfLosing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pulsemetry.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	p := newTestPipeline(t, db, logs, func() time.Time { return time.Unix(fixtureUnix, 0).UTC() })

	// 핸들을 닫아 쓰기를 실패하게 만든다.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	p.cmds <- command{kind: cmdBatch, batch: walkthroughBatch(t)}
	p.submit(cmdFlush)
	// 커맨드가 처리될 때까지 기다린다 — submit 은 큐에 넣기만 한다.
	waitFor(t, "저장 실패 기록", func() bool { return p.Stats().WriteErrors > 0 })

	if !strings.Contains(logs.String(), "다음 틱에 재시도") {
		t.Errorf("실패가 조용히 넘어갔다:\n%s", logs.String())
	}
	if p.Stats().EventsStored != 0 {
		t.Errorf("EventsStored = %d, want 0", p.Stats().EventsStored)
	}
	p.close(time.Now().Add(5 * time.Second))

	// 새 핸들로 열어 보면 실패한 배치는 실제로 들어가지 않았다.
	db2, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if n := countRows(t, db2.SQL(), `SELECT COUNT(*) FROM events`); n != 0 {
		t.Errorf("events = %d행, want 0 (쓰기가 실패했으므로)", n)
	}
}

// TestAssemblerPruneStopsSnapshotResurrection 은 계약 4 를 고정한다.
//
// 조립기 메모리를 정리하지 않으면 세션 맵이 무한히 자라고, 보존 정책이 지운 세션을 다음
// 스냅샷이 되살린다. 시계를 sessionTTL 너머로 밀어 정리가 실제로 일어나는지 본다.
//
// **정리는 반드시 저장 뒤에 온다.** 앞에 두면 마감되는 순간 이미 TTL 을 넘긴 세션
// (데몬이 몇 시간 잠들었다 깨는 경우)이 스냅샷에 들어가기 전에 사라져 한 번도 저장되지 못한다.
func TestAssemblerPruneStopsSnapshotResurrection(t *testing.T) {
	db := openTestStore(t)
	defer db.Close()

	var nowNano atomic.Int64
	nowNano.Store(time.Unix(fixtureUnix, 0).UTC().UnixNano())
	logs := &syncBuffer{}
	p := newTestPipeline(t, db, logs, func() time.Time { return time.Unix(0, nowNano.Load()).UTC() })

	p.cmds <- command{kind: cmdBatch, batch: walkthroughBatch(t)}
	p.submit(cmdSessions)
	waitFor(t, "세션 저장", func() bool { return p.Stats().SessionsWritten > 0 })

	if n := countRows(t, db.SQL(), `SELECT COUNT(*) FROM tool_calls`); n != 3 {
		t.Fatalf("tool_calls = %d행, want 3", n)
	}

	// 시계를 sessionTTL 너머로 민다. 이 틱에서 세션이 마감되고, 마감된 세션이 저장된
	// **뒤에** 조립기 메모리에서 정리된다.
	nowNano.Store(time.Unix(fixtureUnix, 0).Add(sessionMemoryTTL + time.Hour).UTC().UnixNano())
	before := p.Stats().SessionsWritten
	p.submit(cmdSessions)
	waitFor(t, "마감 + 조립기 정리", func() bool {
		return p.Stats().SessionsWritten > before && strings.Contains(logs.String(), "조립기 정리")
	})

	// v3 는 세션 상태를 저장하지 않는다. 마감 결과는 ended_at 으로만 드러난다 (ADR 0009).
	var endedAt sql.NullInt64
	if err := db.SQL().QueryRow(`SELECT ended_at FROM sessions`).Scan(&endedAt); err != nil {
		t.Fatal(err)
	}
	if !endedAt.Valid {
		t.Error("ended_at 이 NULL — 정리 전에 마감 결과가 저장되어야 한다")
	}

	// 이제 보존 정책이 승격 행을 지운 상황을 흉내 낸다. 자식부터 지운다 — NO ACTION 이다.
	for _, q := range []string{`DELETE FROM file_changes`, `DELETE FROM tool_calls`} {
		if _, err := db.SQL().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	before = p.Stats().SessionsWritten
	p.submit(cmdSessions)
	p.submit(cmdFlush)
	waitFor(t, "정리 후 세션 틱", func() bool { return p.Stats().SessionsWritten == before })

	// 조립기가 그 세션을 더 이상 들고 있지 않으므로 스냅샷에도 없다.
	if n := countRows(t, db.SQL(), `SELECT COUNT(*) FROM tool_calls`); n != 0 {
		t.Errorf("tool_calls = %d행, want 0 — 보존 정책이 지운 행을 스냅샷이 되살렸다", n)
	}
	if n := countRows(t, db.SQL(), `SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Errorf("sessions = %d행, want 1 — 조립기 정리가 저장된 세션까지 지우면 안 된다", n)
	}
}

// TestDedupWindowEvictsOldest 는 창이 유계인지 본다. 무한히 자라면 오래 사는 데몬에서
// 그대로 메모리 누수다.
func TestDedupWindowEvictsOldest(t *testing.T) {
	w := newDedupWindow(2)
	for _, k := range []string{"a", "b"} {
		if !w.add(k) {
			t.Fatalf("%q 첫 등장이 중복으로 판정됐다", k)
		}
	}
	if w.add("a") {
		t.Error("창 안의 중복을 놓쳤다")
	}
	if !w.add("c") { // a 가 밀려난다
		t.Fatal("새 키가 중복으로 판정됐다")
	}
	if !w.add("a") {
		t.Error("창 밖으로 밀려난 키가 여전히 중복으로 잡힌다 — 창이 자라고 있다")
	}
	if len(w.seen) > 2 || len(w.ring) > 2 {
		t.Errorf("창이 상한을 넘었다: seen=%d ring=%d", len(w.seen), len(w.ring))
	}
}

// 키를 만들 수 없는 이벤트끼리 서로를 중복으로 판정하면 서로 다른 이벤트가 하나로 접힌다.
func TestDedupWindowPassesEmptyKeys(t *testing.T) {
	w := newDedupWindow(4)
	if !w.add("") || !w.add("") {
		t.Error("빈 키가 중복으로 판정됐다")
	}
}

// TestPipelineIgnoresCommandsAfterClose 는 종료 뒤 들어온 배치가 panic 을 내지 않는지 본다.
// cmds 채널을 close 하지 않고 센티널을 쓰는 이유가 여기에 있다.
func TestPipelineIgnoresCommandsAfterClose(t *testing.T) {
	db := openTestStore(t)
	defer db.Close()
	p := newTestPipeline(t, db, &syncBuffer{}, func() time.Time { return time.Unix(fixtureUnix, 0).UTC() })

	if !p.close(time.Now().Add(5 * time.Second)) {
		t.Fatal("파이프라인이 제한 시간 안에 닫히지 않았다")
	}
	if err := p.Consume(context.Background(), walkthroughBatch(t)); err == nil {
		t.Error("종료된 파이프라인이 배치를 받아들였다")
	}
	p.submit(cmdFlush) // 막히지도 panic 하지도 않아야 한다
	if !p.close(time.Now().Add(time.Second)) {
		t.Error("close 를 두 번 부르면 안전해야 한다")
	}
}
