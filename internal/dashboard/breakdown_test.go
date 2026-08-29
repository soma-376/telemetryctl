package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// 「Agent 사용 비율」 — v3 에는 rollup_hourly 가 없으므로 llm_calls 를 벤더별로 묶는다.
func TestBreakdownByVendor(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	f.write(store.Batch{Events: []store.EventRecord{
		llmRecord("s-claude", "t1", at, 1, llmSpec{Cost: 9}),
		llmRecord("s-codex", "t2", at, 2, llmSpec{Vendor: vendorCodex, Cost: 3}),
	}})

	rows, err := f.reader.Breakdown(context.Background(), BreakdownQuery{Dim: DimVendor, TZ: seoul})
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("행 = %d, want 2 (%+v)", len(rows), rows)
	}
	if rows[0].Key != vendorClaude || rows[0].CostUSD != 9 {
		t.Errorf("1행 = %+v, want claude_code/9 (비용 내림차순)", rows[0])
	}
	if rows[1].Key != vendorCodex || rows[1].CostUSD != 3 {
		t.Errorf("2행 = %+v, want codex/3", rows[1])
	}
	if rows[0].Label != rows[0].Key {
		t.Errorf("Label = %q, want Key 와 같음", rows[0].Label)
	}
}

// 한 출처의 행이 다른 출처의 SUM 을 부풀리면 안 된다.
//
// 승격 테이블을 하나의 JOIN 으로 묶는 구현은 여기서 걸린다 — 도구 호출이 3건인 턴의
// 비용이 정확히 3배가 된다 (store/promote.go 가 경고한 "비용 2배" 와 같은 사고다).
func TestBreakdownDoesNotMultiplyAcrossSources(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	recs := []store.EventRecord{
		llmRecord("s-mix", "t1", at, 1, llmSpec{Cost: 5, Input: 100, Output: 20}),
	}
	for i := range 3 {
		recs = append(recs, toolRecord("s-mix", "t1", "call-mix-"+string(rune('a'+i)),
			at, 10+i, toolSpec{
				ToolName: "Edit",
				Success:  event.Some(true),
				Target:   workspaceA + "/apply.go",
				File:     fileChange(workspaceA+"/apply.go", 4, 1),
			}))
	}
	f.write(store.Batch{Events: recs})

	rows, err := f.reader.Breakdown(context.Background(), BreakdownQuery{TZ: utc})
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("행 = %+v, want total 한 줄", rows)
	}
	got := rows[0].Totals
	switch {
	case got.CostUSD != 5:
		t.Errorf("cost = %v, want 5 — 도구 호출 행 수만큼 곱해졌다", got.CostUSD)
	case got.InputTokens != 100 || got.OutputTokens != 20:
		t.Errorf("토큰 = %d/%d, want 100/20", got.InputTokens, got.OutputTokens)
	case got.ToolCalls != 3:
		t.Errorf("tool_calls = %d, want 3", got.ToolCalls)
	case got.LinesAdded != 12 || got.LinesRemoved != 3:
		t.Errorf("라인 = +%d/-%d, want +12/-3", got.LinesAdded, got.LinesRemoved)
	case got.APIRequests != 1:
		t.Errorf("api_requests = %d, want 1", got.APIRequests)
	case got.Prompts != 1:
		t.Errorf("prompts = %d, want 1 (실제 턴 하나)", got.Prompts)
	}
}

// dim=project 의 Key 는 워크스페이스 원경로이고 Label 은 basename 이다 (ADR 0010).
// 전체 경로를 그대로 표에 넣으면 한 줄이 화면을 넘긴다.
func TestBreakdownLabelsProjectByBaseName(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("s-a", at),
			newSession("s-b", at, func(s *session.Session) { s.WorkspacePath = workspaceB }),
			// 워크스페이스를 관측하지 못한 세션. 키가 빈 문자열로 남는다.
			newSession("s-none", at, func(s *session.Session) { s.WorkspacePath = "" }),
		},
		Events: []store.EventRecord{
			llmRecord("s-a", "t-a", at, 1, llmSpec{Cost: 2}),
			llmRecord("s-b", "t-b", at, 2, llmSpec{Cost: 1}),
		},
	})

	rows, err := f.reader.Breakdown(context.Background(), BreakdownQuery{Dim: DimProject, TZ: seoul})
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	byKey := map[string]Row{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	if got := byKey[workspaceA]; got.Label != "telemetryctl" || got.CostUSD != 2 {
		t.Errorf("%s = %+v, want label=telemetryctl cost=2", workspaceA, got)
	}
	if got := byKey[workspaceB]; got.Label != "pulsemetry-backend" {
		t.Errorf("%s Label = %q, want pulsemetry-backend", workspaceB, got.Label)
	}
	// 경로를 모르는 세션도 행에서 사라지지 않는다 — 조용히 빠지면 합계가 안 맞는다.
	if _, ok := byKey[""]; !ok {
		t.Errorf("워크스페이스 없는 세션의 행이 없다: %+v", rows)
	}
}

// 「시간대별 집중도」 — 현지 시각 기준으로 묶여야 한다.
//
// UTC 18:00 은 서울에서 다음 날 03시다. UTC 로 묶는 구현은 18시 칸에 값을 넣어 이 테스트를
// 통과하지 못한다.
func TestBreakdownHourOfDayUsesLocalHours(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Events: []store.EventRecord{
		promptRecord("s-h1", "t-h1", time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC), 1, "저녁 작업"),
		promptRecord("s-h2", "t-h2", time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC), 2, "새벽 작업"),
	}})
	ctx := context.Background()

	seoulRows, err := f.reader.Breakdown(ctx, BreakdownQuery{TZ: seoul, Days: 2, Bucket: BucketHourOfDay})
	if err != nil {
		t.Fatalf("Breakdown(Seoul): %v", err)
	}
	if len(seoulRows) != 24 {
		t.Fatalf("행 = %d, want 24 (빈 시간대도 채워야 한다)", len(seoulRows))
	}
	if seoulRows[3].Key != "03" || seoulRows[3].Prompts != 1 {
		t.Errorf("서울 03시 = %+v, want prompts=1", seoulRows[3])
	}
	if seoulRows[10].Prompts != 1 {
		t.Errorf("서울 10시 prompts = %d, want 1", seoulRows[10].Prompts)
	}
	if seoulRows[18].Prompts != 0 {
		t.Errorf("서울 18시 prompts = %d, want 0 — UTC 시각으로 묶었다", seoulRows[18].Prompts)
	}

	utcRows, err := f.reader.Breakdown(ctx, BreakdownQuery{TZ: utc, Days: 2, Bucket: BucketHourOfDay})
	if err != nil {
		t.Fatalf("Breakdown(UTC): %v", err)
	}
	if utcRows[18].Prompts != 1 {
		t.Errorf("UTC 18시 prompts = %d, want 1", utcRows[18].Prompts)
	}
}

func TestBreakdownByDayFillsGaps(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Events: []store.EventRecord{
		// 서울 기준 08-10 (오늘)
		llmRecord("s-d1", "t-d1", time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC), 1, llmSpec{Cost: 3}),
		// 서울 기준 08-08
		llmRecord("s-d2", "t-d2", time.Date(2026, 8, 8, 3, 0, 0, 0, time.UTC), 2, llmSpec{Cost: 1}),
	}})

	rows, err := f.reader.Breakdown(context.Background(), BreakdownQuery{TZ: seoul, Days: 3, Bucket: BucketDay})
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	want := []struct {
		key  string
		cost float64
	}{
		{"2026-08-08", 1},
		{"2026-08-09", 0}, // 값이 없는 날도 행이 있어야 그래프가 밀리지 않는다
		{"2026-08-10", 3},
	}
	if len(rows) != len(want) {
		t.Fatalf("행 = %d, want %d (%+v)", len(rows), len(want), rows)
	}
	for i, w := range want {
		if rows[i].Key != w.key || rows[i].CostUSD != w.cost {
			t.Errorf("%d행 = %s/%v, want %s/%v", i, rows[i].Key, rows[i].CostUSD, w.key, w.cost)
		}
		if rows[i].StartAt == 0 {
			t.Errorf("%d행 StartAt = 0 — 날짜 축은 구간 시작을 줘야 한다", i)
		}
	}
}

func TestBreakdownRejectsUnknownInput(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	tests := []struct {
		name string
		q    BreakdownQuery
	}{
		{name: "오타난 dim", q: BreakdownQuery{Dim: "vender"}},
		// v1 의 type 축은 v3 에 입력이 없어 사라졌다. 조용히 빈 결과를 주면 안 된다.
		{name: "사라진 type 축", q: BreakdownQuery{Dim: "type"}},
		{name: "알 수 없는 bucket", q: BreakdownQuery{Bucket: "weekly"}},
		{name: "잘못된 시간대", q: BreakdownQuery{TZ: "Mars/Phobos"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.reader.Breakdown(ctx, tc.q); err == nil {
				t.Error("에러가 없다 — 잘못된 입력이 '데이터 없음' 으로 보인다")
			}
		})
	}
}

// dim 을 비우면 total 로 본다 — 가장 흔한 호출이 기본값이어야 한다.
func TestBreakdownDefaultsToTotal(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	f.write(store.Batch{Events: []store.EventRecord{
		llmRecord("s-t1", "t1", at, 1, llmSpec{Cost: 5}),
		llmRecord("s-t2", "t2", at, 2, llmSpec{Vendor: vendorCodex, Cost: 5}),
	}})

	rows, err := f.reader.Breakdown(context.Background(), BreakdownQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	if len(rows) != 1 || rows[0].Key != "" || rows[0].CostUSD != 10 {
		t.Fatalf("행 = %+v, want total 한 줄(10)", rows)
	}
}

// 도구 축에는 모델별 비용이 붙을 수 없고, 모델 축에는 도구 호출 수가 붙을 수 없다.
// 붙이려면 없는 관계를 지어내야 하고, 그렇게 만든 숫자는 되짚을 근거가 없다.
func TestBreakdownDimensionsUseOnlyTheirOwnSources(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	f.write(store.Batch{Events: []store.EventRecord{
		llmRecord("s-dim", "t1", at, 1, llmSpec{Model: "claude-sonnet-4", Cost: 7}),
		toolRecord("s-dim", "t1", "call-dim", at, 2, toolSpec{ToolName: "Bash", Success: event.Some(true)}),
	}})
	ctx := context.Background()

	models, err := f.reader.Breakdown(ctx, BreakdownQuery{Dim: DimModel, TZ: utc})
	if err != nil {
		t.Fatalf("Breakdown(model): %v", err)
	}
	if len(models) != 1 || models[0].Key != "claude-sonnet-4" || models[0].CostUSD != 7 {
		t.Fatalf("모델 축 = %+v", models)
	}
	if models[0].ToolCalls != 0 {
		t.Errorf("모델 축의 tool_calls = %d, want 0 — 도구 호출에는 모델이 없다", models[0].ToolCalls)
	}

	tools, err := f.reader.Breakdown(ctx, BreakdownQuery{Dim: DimTool, TZ: utc})
	if err != nil {
		t.Fatalf("Breakdown(tool): %v", err)
	}
	if len(tools) != 1 || tools[0].Key != "Bash" || tools[0].ToolCalls != 1 {
		t.Fatalf("도구 축 = %+v", tools)
	}
	if tools[0].CostUSD != 0 {
		t.Errorf("도구 축의 cost = %v, want 0 — 비용에는 도구가 없다", tools[0].CostUSD)
	}
}
