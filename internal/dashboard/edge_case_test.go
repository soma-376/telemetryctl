package dashboard

// 화면 계약의 경계 조건 (PROJ-97).
//
// 티켓이 지목한 여섯 가지를 화면 **여러 장에 걸쳐** 확인한다. 개별 화면의 경계 처리는
// 앞선 티켓들이 각자 고정했지만, 경계에서 화면들이 서로 어긋나는지는 아무도 보지 않았다.
//
//	시간대 경계·DST — 하루의 길이가 24시간이 아닌 날에도 화면들이 같은 구간을 본다
//	순서 역전       — 도착 순서가 뒤집혀도 결과가 같다 (ADR 0009 의 events.seq 결정)
//	중복 이벤트     — 같은 페이로드를 두 번 넣어도 수치가 두 배가 되지 않는다
//	빈 DB           — 데이터가 하나도 없어도 화면 골격이 유지된다
//	대용량 세션     — 상한에서 잘리되 **합계는 잘리지 않는다**
//	사용량 API 장애 — 벤더 조회가 실패해도 트레이의 나머지가 살아 있다

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// ── 시간대 경계와 DST ───────────────────────────────────────────────────────

// TestEdgeCase_DSTDayKeepsSurfacesAligned 는 하루가 23시간인 날에도 Home·Activity·
// Breakdown 이 같은 구간을 보는지 확인한다.
//
// 2026-03-08 America/New_York 은 01:59 다음이 03:00 이라 하루가 23시간이다. 구간 계산을
// "자정 + 86400" 으로 하는 코드는 여기서만 어긋나고, 그 어긋남은 한 화면에서만 나타나면
// 다른 화면과의 대조로만 잡힌다.
func TestEdgeCase_DSTDayKeepsSurfacesAligned(t *testing.T) {
	const (
		tzNY   = "America/New_York"
		dstDay = "2026-03-08"
	)
	f := newFixture(t)
	ctx := context.Background()

	// 전환 전(00:30)·전환 후(04:30)·늦은 밤(23:30) 세 시각에 하나씩.
	for i, hhmm := range []string{"00:30", "04:30", "23:30"} {
		at := mustTime(t, "2006-01-02 15:04", dstDay+" "+hhmm, tzNY)
		key := fmt.Sprintf("dst-%d", i)
		f.write(store.Batch{
			Sessions: []session.Session{newSession(key, at)},
			Events: []store.EventRecord{
				promptRecord(key, key+"-t1", at, 1, "DST 경계 확인 "+key),
				llmRecord(key, key+"-t1", at, 2, llmSpec{
					Model: "claude-sonnet-4-5", Cost: 0.1, Input: 100, Output: 50,
				}),
			},
		})
	}

	home, err := f.reader.Home(ctx, HomeQuery{TZ: tzNY, Date: dstDay})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	// 하루가 23시간이다. 86400 이면 구간 계산이 시간대를 무시한 것이다.
	if span := home.EndAt - home.StartAt; span != 23*3600 {
		t.Errorf("하루 길이 = %d초, want %d초 (봄철 DST 전환일)", span, 23*3600)
	}

	page, err := f.reader.Activity(ctx, ActivityQuery{
		Since: home.StartAt, Until: home.EndAt, Limit: maxActivityLimit,
	})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(page.Rows) != 3 {
		t.Fatalf("Activity 줄 = %d, want 3 — 구간이 하루를 다 덮지 못했다", len(page.Rows))
	}
	got := sumActivity(page.Rows)
	if got.Tokens != home.Totals.Tokens() {
		t.Errorf("DST 날: Activity 줄 합 = %d, Home 카드 = %d", got.Tokens, home.Totals.Tokens())
	}

	// 2시간 창은 하루를 빈틈없이·겹침 없이 덮는다. DST 때문에 마지막 창이 짧다.
	windows := home.TwoHour.Windows
	if len(windows) == 0 {
		t.Fatal("2시간 창이 없다")
	}
	if windows[0].StartAt != home.StartAt {
		t.Errorf("첫 창 시작 = %d, want %d", windows[0].StartAt, home.StartAt)
	}
	if last := windows[len(windows)-1]; last.EndAt != home.EndAt {
		t.Errorf("마지막 창 끝 = %d, want %d", last.EndAt, home.EndAt)
	}
	for i := 1; i < len(windows); i++ {
		if windows[i].StartAt != windows[i-1].EndAt {
			t.Errorf("창 %d 와 %d 사이에 틈/겹침이 있다: %d ≠ %d",
				i-1, i, windows[i-1].EndAt, windows[i].StartAt)
		}
	}

	// 날짜 축 집계도 같은 구간을 본다.
	rows, err := f.reader.Breakdown(ctx, BreakdownQuery{
		TZ: tzNY, Bucket: BucketDay, From: home.StartAt, To: home.EndAt,
	})
	if err != nil {
		t.Fatalf("Breakdown(day): %v", err)
	}
	if len(rows) != 1 || rows[0].Key != dstDay {
		t.Fatalf("날짜 행 = %+v, want %s 하나", rows, dstDay)
	}
	if rows[0].Tokens() != home.Totals.Tokens() {
		t.Errorf("날짜 행 토큰 = %d, Home 카드 = %d", rows[0].Tokens(), home.Totals.Tokens())
	}
}

// TestEdgeCase_TimezoneBoundaryMovesSessionsBetweenDays 는 같은 사실이 시간대에 따라
// 다른 날로 귀속되는지 본다. 저장값은 그대로이고 자르는 위치만 달라져야 한다.
func TestEdgeCase_TimezoneBoundaryMovesSessionsBetweenDays(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// 2026-08-09 20:00 UTC = 2026-08-10 05:00 서울. UTC 로는 9일, 서울로는 10일이다.
	at := time.Date(2026, 8, 9, 20, 0, 0, 0, time.UTC)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("tz-edge", at)},
		Events: []store.EventRecord{
			promptRecord("tz-edge", "tz-edge-t1", at, 1, "시간대 경계"),
			llmRecord("tz-edge", "tz-edge-t1", at, 2, llmSpec{
				Model: "claude-sonnet-4-5", Cost: 0.4, Input: 200, Output: 80,
			}),
		},
	})

	cases := []struct {
		tz       string
		date     string
		wantRows int
	}{
		{tz: utc, date: "2026-08-09", wantRows: 1},
		{tz: utc, date: "2026-08-10", wantRows: 0},
		{tz: seoul, date: "2026-08-09", wantRows: 0},
		{tz: seoul, date: "2026-08-10", wantRows: 1},
	}
	for _, tc := range cases {
		t.Run(tc.tz+"/"+tc.date, func(t *testing.T) {
			home, err := f.reader.Home(ctx, HomeQuery{TZ: tc.tz, Date: tc.date})
			if err != nil {
				t.Fatalf("Home: %v", err)
			}
			if len(home.Recent) != tc.wantRows {
				t.Errorf("최근 세션 = %d건, want %d", len(home.Recent), tc.wantRows)
			}
			page, err := f.reader.Activity(ctx, ActivityQuery{Since: home.StartAt, Until: home.EndAt})
			if err != nil {
				t.Fatalf("Activity: %v", err)
			}
			// 두 화면이 같은 답이어야 한다. 하나만 옮겨 가면 목록과 카드가 갈린다.
			if len(page.Rows) != tc.wantRows {
				t.Errorf("Activity 줄 = %d건, want %d", len(page.Rows), tc.wantRows)
			}
			if (home.Totals.Tokens() > 0) != (tc.wantRows > 0) {
				t.Errorf("카드 토큰 = %d 인데 줄은 %d건이다", home.Totals.Tokens(), tc.wantRows)
			}
		})
	}
}

// ── 순서 역전 ───────────────────────────────────────────────────────────────

// TestEdgeCase_OutOfOrderArrivalGivesTheSameScreens 는 도착 순서가 뒤집혀도 화면이
// 같은지 본다.
//
// ADR 0009 는 events.seq 를 **도착 순서** 로 정하고 "순서가 뒤집혀 도착해도 정상 입력으로
// 취급하며, 독자는 ORDER BY occurred_at, seq 로 읽는다" 고 못 박았다. 그 결정이 지켜지면
// 저장 순서와 무관하게 조회 결과가 같아야 한다.
func TestEdgeCase_OutOfOrderArrivalGivesTheSameScreens(t *testing.T) {
	at := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)

	// 같은 사실 다섯 건. 시각은 오름차순이고 배치 순서만 바꿔 넣는다.
	build := func(key string) []store.EventRecord {
		turn := key + "-t1"
		return []store.EventRecord{
			promptRecord(key, turn, at, 1, "순서 역전 확인"),
			llmRecord(key, turn, at.Add(1*time.Minute), 2, llmSpec{
				Model: "claude-sonnet-4-5", Cost: 0.2, Input: 100, Output: 40,
			}),
			toolRecord(key, turn, key+"-c1", at.Add(2*time.Minute), 3, toolSpec{
				ToolName: "Read", Success: event.Some(true), Target: workspaceA + "/a.go",
			}),
			toolRecord(key, turn, key+"-c2", at.Add(3*time.Minute), 4, toolSpec{
				ToolName: "Edit", Success: event.Some(true),
				Target: workspaceA + "/a.go", File: fileChange(workspaceA+"/a.go", 5, 1),
			}),
			llmRecord(key, turn, at.Add(4*time.Minute), 5, llmSpec{
				Model: "claude-sonnet-4-5", Cost: 0.3, Input: 150, Output: 60,
			}),
		}
	}

	// 순서대로 넣은 쪽.
	forward := newFixture(t)
	forward.write(store.Batch{Sessions: []session.Session{newSession("ooo", at)}})
	for _, rec := range build("ooo") {
		forward.write(store.Batch{Events: []store.EventRecord{rec}})
	}

	// 거꾸로 넣은 쪽. 세션 스냅샷도 이벤트 뒤에 온다.
	reverse := newFixture(t)
	recs := build("ooo")
	for i := len(recs) - 1; i >= 0; i-- {
		reverse.write(store.Batch{Events: []store.EventRecord{recs[i]}})
	}
	reverse.write(store.Batch{Sessions: []session.Session{newSession("ooo", at)}})

	ctx := context.Background()
	surfaces := []struct {
		name string
		call func(*fixture) (any, error)
	}{
		{
			name: "Home",
			call: func(f *fixture) (any, error) {
				h, err := f.reader.Home(ctx, HomeQuery{TZ: utc, Date: "2026-08-09"})
				return h.Totals, err
			},
		},
		{
			name: "Activity",
			call: func(f *fixture) (any, error) {
				p, err := f.reader.Activity(ctx, ActivityQuery{})
				return p.Rows, err
			},
		},
		{
			name: "SessionDetail",
			call: func(f *fixture) (any, error) {
				d, err := f.reader.Session(ctx, f.sessionID(vendorClaude, "ooo"))
				return d, err
			},
		},
		{
			name: "SessionMetrics",
			call: func(f *fixture) (any, error) {
				m, err := f.reader.SessionMetrics(ctx,
					SessionMetricsQuery{SessionID: f.sessionID(vendorClaude, "ooo")})
				// 턴 목록의 turn_id 는 저장 순서에서 나오는 대리 키라 두 DB 에서 다르다.
				// 화면이 그리는 것은 합계이므로 그쪽만 비교한다.
				return m.Totals, err
			},
		},
		{
			name: "FileChanges",
			call: func(f *fixture) (any, error) {
				c, err := f.reader.FileChanges(ctx, f.sessionID(vendorClaude, "ooo"))
				return c.Totals, err
			},
		},
	}
	for _, tc := range surfaces {
		t.Run(tc.name, func(t *testing.T) {
			want, err := tc.call(forward)
			if err != nil {
				t.Fatalf("순서대로: %v", err)
			}
			got, err := tc.call(reverse)
			if err != nil {
				t.Fatalf("거꾸로: %v", err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("도착 순서가 화면을 바꿨다:\n순서대로 = %+v\n거꾸로   = %+v", want, got)
			}
		})
	}

	// 툴 타임라인은 시각 오름차순이어야 한다 — 도착 순서를 그대로 그리면 안 된다.
	detail, err := reverse.reader.Session(ctx, reverse.sessionID(vendorClaude, "ooo"))
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	for i := 1; i < len(detail.Tools); i++ {
		if detail.Tools[i].TS < detail.Tools[i-1].TS {
			t.Errorf("타임라인이 시간순이 아니다: [%d]=%d < [%d]=%d",
				i, detail.Tools[i].TS, i-1, detail.Tools[i-1].TS)
		}
	}
}

// ── 중복 이벤트 ─────────────────────────────────────────────────────────────

// TestEdgeCase_DuplicateEventsDoNotDoubleCount 는 같은 페이로드를 두 번 넣어도 화면의
// 수치가 두 배가 되지 않는지 본다. 벤더 exporter 의 재전송은 정상 동작이다.
func TestEdgeCase_DuplicateEventsDoNotDoubleCount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	at := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)

	batch := func() store.Batch {
		return store.Batch{
			Sessions: []session.Session{newSession("dup", at)},
			Events: []store.EventRecord{
				promptRecord("dup", "dup-t1", at, 1, "중복 전송 확인"),
				llmRecord("dup", "dup-t1", at.Add(time.Minute), 2, llmSpec{
					Model: "claude-sonnet-4-5", Cost: 0.25, Input: 100, Output: 40,
				}),
				toolRecord("dup", "dup-t1", "dup-c1", at.Add(2*time.Minute), 3, toolSpec{
					ToolName: "Edit", Success: event.Some(true),
					Target: workspaceA + "/a.go", File: fileChange(workspaceA+"/a.go", 7, 2),
				}),
			},
		}
	}

	f.write(batch())
	first := snapshotSurfaces(t, f, "dup")

	// 같은 배치를 그대로 다시 넣는다. record_hash 가 UNIQUE 라 events 는 접히고,
	// call_key 가 UNIQUE 라 tool_calls 도 접힌다.
	res, err := f.db.Write(ctx, batch())
	if err != nil {
		t.Fatalf("두 번째 Write: %v", err)
	}
	if res.EventsInserted != 0 {
		t.Errorf("두 번째 Write 가 이벤트 %d건을 새로 넣었다 — 중복이 접히지 않았다", res.EventsInserted)
	}

	second := snapshotSurfaces(t, f, "dup")
	if !reflect.DeepEqual(first, second) {
		t.Errorf("재전송이 화면을 바꿨다:\n처음 = %+v\n재전송 후 = %+v", first, second)
	}
	if first.Tokens != 140 {
		t.Errorf("토큰 = %d, want 140 — 씨앗이 화면에 닿지 않았다", first.Tokens)
	}
	if first.ToolCalls != 1 || first.FileChanges != 1 {
		t.Errorf("툴 %d건 / 파일변경 %d건, want 1/1", first.ToolCalls, first.FileChanges)
	}
}

// surfaceSnapshot 은 중복·순서 검사가 비교하는 화면 수치 묶음이다.
type surfaceSnapshot struct {
	Tokens      int64
	CostUSD     float64
	ToolCalls   int64
	LLMCalls    int64
	FileChanges int64
	Rows        int
	Turns       int64
}

func snapshotSurfaces(t *testing.T, f *fixture, key string) surfaceSnapshot {
	t.Helper()
	ctx := context.Background()
	id := f.sessionID(vendorClaude, key)

	home, err := f.reader.Home(ctx, HomeQuery{TZ: utc, Date: "2026-08-09"})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	page, err := f.reader.Activity(ctx, ActivityQuery{})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	metrics, err := f.reader.SessionMetrics(ctx, SessionMetricsQuery{SessionID: id})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	changes, err := f.reader.FileChanges(ctx, id)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}
	return surfaceSnapshot{
		Tokens:      home.Totals.Tokens(),
		CostUSD:     home.Totals.CostUSD,
		ToolCalls:   home.Totals.ToolCalls,
		LLMCalls:    metrics.Totals.LLMCalls,
		FileChanges: changes.Totals.Changes,
		Rows:        len(page.Rows),
		Turns:       metrics.Totals.TurnCount,
	}
}

// ── 빈 DB ───────────────────────────────────────────────────────────────────

// TestEdgeCase_EmptyDatabaseKeepsEveryScreenShape 는 **파일은 있는데 행이 없는** 상태를
// 본다. absent_test.go 가 보는 것은 파일 자체가 없는 상태라 두 상황이 다르다 — 데몬이
// 한 번 돌아 스키마만 만든 직후가 여기다.
func TestEdgeCase_EmptyDatabaseKeepsEveryScreenShape(t *testing.T) {
	f := newFixture(t) // 아무것도 쓰지 않는다.
	ctx := context.Background()

	if !f.reader.Available() {
		t.Fatal("Available = false — 파일은 있다")
	}

	home, err := f.reader.Home(ctx, HomeQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if len(home.Cards) != 4 || len(home.TwoHour.Windows) != 12 {
		t.Errorf("골격이 무너졌다: 카드 %d장 / 창 %d개", len(home.Cards), len(home.TwoHour.Windows))
	}
	if home.Recent == nil || home.ActiveAgents == nil {
		t.Error("슬라이스가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다")
	}

	bd, err := f.reader.HomeBreakdown(ctx, HomeBreakdownQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("HomeBreakdown: %v", err)
	}
	if bd.Peak.Found || bd.Peak.Index != -1 {
		t.Errorf("사용량이 없는데 최고 시간대를 골랐다: %+v", bd.Peak)
	}
	if bd.Totals != home.Totals {
		t.Errorf("빈 날에도 두 화면이 어긋난다: %+v vs %+v", bd.Totals, home.Totals)
	}

	page, err := f.reader.Activity(ctx, ActivityQuery{})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if page.Rows == nil || len(page.Rows) != 0 || page.HasMore {
		t.Errorf("Activity = %+v, want 빈 슬라이스 / HasMore=false", page)
	}

	// 없는 세션을 물어도 에러가 아니다. 보존 정책이 지운 id 를 화면이 들고 있는 것은 정상이다.
	for _, tc := range []struct {
		name string
		call func() (bool, error)
	}{
		{"Session", func() (bool, error) { d, err := f.reader.Session(ctx, 1); return d.Found, err }},
		{"SessionMetrics", func() (bool, error) {
			m, err := f.reader.SessionMetrics(ctx, SessionMetricsQuery{SessionID: 1})
			return m.Found, err
		}},
		{"FileChanges", func() (bool, error) { c, err := f.reader.FileChanges(ctx, 1); return c.Found, err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, err := tc.call()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if found {
				t.Errorf("Found = true — 빈 DB 에서 세션 1 을 찾았다")
			}
		})
	}

	// 분류도 빈 결과다.
	cls, err := NewClassifier(f.reader).Session(ctx, 1)
	if err != nil {
		t.Fatalf("Classifier: %v", err)
	}
	if cls.WorkType != WorkTypeUnknown || len(cls.Turns) != 0 {
		t.Errorf("빈 DB 분류 = %+v, want unknown / 턴 0", cls)
	}
}

// ── 대용량 세션 ─────────────────────────────────────────────────────────────

// TestEdgeCase_LargeSessionTruncatesListsButNotTotals 는 상한을 넘는 세션에서
// **목록만 잘리고 합계는 온전한지** 본다.
//
// 세 화면이 각자 다른 상한을 갖는다 (maxToolEvents · defaultSessionTurns ·
// maxFileTimeline). 어느 하나라도 합계까지 잘라 버리면 사용자는 "이 세션은 200줄만
// 고쳤다" 같은 잘못된 사실을 읽는다.
func TestEdgeCase_LargeSessionTruncatesListsButNotTotals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	at := time.Date(2026, 8, 9, 1, 0, 0, 0, time.UTC)

	const (
		turns       = defaultSessionTurns + 5 // 205
		tools       = maxToolEvents + 3       // 1003
		fileChanges = maxFileTimeline + 7     // 207
		hotFile     = workspaceA + "/hot.go"  //  잘리는 타임라인의 대상
	)

	// 턴 205개. 각 턴에 프롬프트 하나씩.
	events := make([]store.EventRecord, 0, turns+tools)
	for i := range turns {
		turn := fmt.Sprintf("big-t%03d", i)
		events = append(events, promptRecord("big", turn, at.Add(time.Duration(i)*time.Second),
			i+1, fmt.Sprintf("대용량 세션 턴 %d", i)))
	}
	// 툴 호출 1003개. 앞의 207건은 같은 파일을 고쳐 타임라인 상한을 넘긴다.
	for i := range tools {
		turn := fmt.Sprintf("big-t%03d", i%turns)
		spec := toolSpec{ToolName: "Bash", Success: event.Some(true)}
		if i < fileChanges {
			spec = toolSpec{
				ToolName: "Edit", Success: event.Some(true),
				Target: hotFile, File: fileChange(hotFile, 1, 1),
			}
		}
		events = append(events, toolRecord("big", turn, fmt.Sprintf("big-c%05d", i),
			at.Add(time.Duration(turns+i)*time.Second), turns+i+1, spec))
	}
	f.write(store.Batch{
		Sessions: []session.Session{newSession("big", at)},
		Events:   events,
	})
	id := f.sessionID(vendorClaude, "big")

	t.Run("SessionDetail", func(t *testing.T) {
		detail, err := f.reader.Session(ctx, id)
		if err != nil {
			t.Fatalf("Session: %v", err)
		}
		if !detail.ToolsTruncated {
			t.Errorf("ToolsTruncated = false — 툴 %d건인데 잘렸다고 하지 않는다", tools)
		}
		if len(detail.Tools) != maxToolEvents {
			t.Errorf("타임라인 = %d건, want %d", len(detail.Tools), maxToolEvents)
		}
		// 세션 줄의 수치는 잘리지 않는다. 이것이 이 테스트의 핵심이다.
		if detail.Session.ToolCalls != tools {
			t.Errorf("세션 줄의 툴 호출 = %d, want %d (타임라인이 잘려도 합계는 온전하다)",
				detail.Session.ToolCalls, tools)
		}
	})

	t.Run("SessionMetrics", func(t *testing.T) {
		m, err := f.reader.SessionMetrics(ctx, SessionMetricsQuery{SessionID: id})
		if err != nil {
			t.Fatalf("SessionMetrics: %v", err)
		}
		if !m.TurnsTruncated {
			t.Errorf("TurnsTruncated = false — 턴 %d개인데 잘렸다고 하지 않는다", turns)
		}
		if len(m.Turns) != defaultSessionTurns || m.TurnLimit != defaultSessionTurns {
			t.Errorf("턴 목록 = %d건 (상한 %d), want %d", len(m.Turns), m.TurnLimit, defaultSessionTurns)
		}
		if m.Totals.TurnCount != turns {
			t.Errorf("TurnCount = %d, want %d (목록이 잘려도 합계는 세션 전체다)",
				m.Totals.TurnCount, turns)
		}
		if m.Totals.ToolCalls != tools {
			t.Errorf("합계 툴 호출 = %d, want %d", m.Totals.ToolCalls, tools)
		}
		// 잘린 목록의 합은 상단 합계보다 **작다**. 같으면 상한이 동작하지 않은 것이다.
		var listed int64
		for _, tm := range m.Turns {
			listed += tm.ToolCalls
		}
		if listed >= m.Totals.ToolCalls {
			t.Errorf("잘린 턴 목록의 합 %d ≥ 전체 %d", listed, m.Totals.ToolCalls)
		}
	})

	t.Run("FileChanges", func(t *testing.T) {
		c, err := f.reader.FileChanges(ctx, id)
		if err != nil {
			t.Fatalf("FileChanges: %v", err)
		}
		if len(c.Files) != 1 {
			t.Fatalf("파일 = %d건, want 1", len(c.Files))
		}
		file := c.Files[0]
		if !file.TimelineTruncated {
			t.Errorf("TimelineTruncated = false — 변경 %d건인데 잘렸다고 하지 않는다", fileChanges)
		}
		if len(file.Timeline) != maxFileTimeline {
			t.Errorf("타임라인 = %d건, want %d", len(file.Timeline), maxFileTimeline)
		}
		if file.Changes != fileChanges || c.Totals.Changes != fileChanges {
			t.Errorf("변경 건수 = %d / 합계 %d, want %d (타임라인이 잘려도 합계는 온전하다)",
				file.Changes, c.Totals.Changes, fileChanges)
		}
		// 줄 수 합계도 전체를 덮는다.
		if added, ok := c.Totals.Additions.Get(); !ok || added != fileChanges {
			t.Errorf("추가 줄 합계 = %v, want %d", c.Totals.Additions, fileChanges)
		}
	})

	t.Run("Activity", func(t *testing.T) {
		// 목록 화면의 한 줄도 세션 전체를 센다.
		page, err := f.reader.Activity(ctx, ActivityQuery{})
		if err != nil {
			t.Fatalf("Activity: %v", err)
		}
		if len(page.Rows) != 1 {
			t.Fatalf("줄 = %d, want 1", len(page.Rows))
		}
		if page.Rows[0].ToolCalls != tools {
			t.Errorf("Activity 줄의 툴 호출 = %d, want %d", page.Rows[0].ToolCalls, tools)
		}
	})
}
