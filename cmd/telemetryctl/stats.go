package main

// telemetryctl stats — 로컬 롤업 집계 조회 (계획서 「CLI 변경」).

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
)

// statsResult 는 --json 출력이자 사람용 표의 원본이다.
//
// 두 출력을 **한 구조체에서** 만드는 것이 필드가 어긋나지 않게 하는 유일한 방법이다.
// 사람용 표는 이 구조체의 부분집합을 그리고, 그 값은 JSON 과 같은 필드에서 온다.
// 표에 없는 필드(api_requests·lines_added 등)는 JSON 에만 있다 — 표는 좁아야 읽히고
// 기계는 전부 필요하기 때문이다.
type statsResult struct {
	// Available 이 false 면 DB 파일이 아직 없다. 에러가 아니라 "아직 수집 전" 이다.
	Available    bool   `json:"available"`
	DatabasePath string `json:"database_path"`

	Group string `json:"group"`
	// Since·Until 은 조회한 구간이다 (UTC unix 초, Until 배타).
	// **JSON 의 모든 시각은 UTC unix 초** 다. 사람용 출력만 로컬 시간대로 옮긴다.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	// Timezone·UTCOffsetSeconds 는 날짜 버킷(--group day)을 자른 기준 시간대다.
	// 약어만으로는 기계가 시각을 복원할 수 없어 오프셋을 함께 싣는다.
	Timezone         string `json:"timezone"`
	UTCOffsetSeconds int    `json:"utc_offset_seconds"`

	Rows []dashboard.Row `json:"rows"`
	// Total 은 구간 전체의 합계다. --limit 으로 잘린 행까지 포함하므로 Rows 의 합보다
	// 클 수 있다 — 표에서 잘린 뒷부분을 사용자가 눈치챌 수 있어야 한다.
	Total dashboard.Totals `json:"total"`
}

func cmdStats(args []string) int { return runStats(os.Stdout, os.Stderr, args) }

func runStats(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	since := fs.String("since", "7d", "조회 구간 (7d·24h·90m 처럼 지금부터 거슬러 올라간다)")
	group := fs.String("group", "vendor", "집계 축 (vendor|model|tool|project|day)")
	asJSON := fs.Bool("json", false, "사람용 표 대신 JSON 출력")
	limit := fs.Int("limit", 20, "표시할 행 수 상한 (1~500, --group day 에는 적용되지 않는다)")
	dataDir := fs.String("data-dir", "", "데이터 디렉터리 (미지정 시 상태 파일 설정 → ~/.pulsemetry)")
	statePath := fs.String("state", "", "설치 상태 파일 경로")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	window, err := parseSince(*since)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 2
	}
	g, err := parseGroup(*group)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 2
	}
	if *limit < 1 || *limit > 500 {
		fmt.Fprintf(stderr, "오류: --limit 은 1~500 이어야 합니다: %d\n", *limit)
		return 2
	}

	target, err := resolveLocalTarget(*dataDir, *statePath)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}
	warnStateErr(stderr, target)

	reader, err := openReader(target)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}
	defer reader.Close() //nolint:errcheck // 조회 전용 핸들이라 닫기 실패에 할 일이 없다

	now := time.Now()
	res, err := collectStats(context.Background(), reader, g, now, window, *limit)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}

	if *asJSON {
		return writeJSON(stdout, stderr, res)
	}
	printStats(stdout, res, g)
	return 0
}

// collectStats 는 두 번 묻는다. 한 번은 축별 행, 한 번은 구간 전체 합계다.
// 합계를 행의 합으로 계산하지 않는 이유는 --limit 이다 — 잘린 행이 있으면 그 합은
// 구간의 합이 아니고, 표 아래에 그런 숫자를 두면 조용히 틀린 보고가 된다.
func collectStats(ctx context.Context, reader *dashboard.Reader, g statsGroup, now time.Time, window time.Duration, limit int) (statsResult, error) {
	from := now.Add(-window).Unix()
	to := now.Unix()

	res := statsResult{
		Available:        reader.Available(),
		DatabasePath:     reader.Path(),
		Group:            g.Name,
		Since:            from,
		Until:            to,
		Timezone:         zoneLabel(now),
		UTCOffsetSeconds: zoneOffsetSeconds(now),
		Rows:             []dashboard.Row{},
	}

	rows, err := reader.Breakdown(ctx, dashboard.BreakdownQuery{
		Dim:    g.Dim,
		TZ:     localTZ,
		From:   from,
		To:     to,
		Bucket: g.Bucket,
		Limit:  limit,
	})
	if err != nil {
		return statsResult{}, err
	}
	res.Rows = rows

	total, err := reader.Breakdown(ctx, dashboard.BreakdownQuery{
		Dim:    dashboard.DimTotal,
		TZ:     localTZ,
		From:   from,
		To:     to,
		Bucket: dashboard.BucketKey,
		Limit:  1,
	})
	if err != nil {
		return statsResult{}, err
	}
	if len(total) > 0 {
		res.Total = total[0].Totals
	}
	return res, nil
}

// localTZ 는 dashboard 에 넘기는 시간대 이름이다. time.LoadLocation("Local") 이
// time.Local 을 준다 — 사용자에게 보이는 날짜 경계는 사용자의 시계를 따라야 한다.
const localTZ = "Local"

// statsColumns 는 표의 열이다. 각 열은 statsResult.Rows 의 한 필드에서만 온다
// (statsCells 참고) — 표에서 계산한 값은 두지 않는다.
var statsColumns = []column{
	{Header: "그룹"},
	{Header: "세션", Right: true},
	{Header: "프롬프트", Right: true},
	{Header: "툴 호출", Right: true},
	{Header: "입력", Right: true},
	{Header: "출력", Right: true},
	{Header: "캐시 읽기", Right: true},
	{Header: "비용(USD)", Right: true},
}

func statsCells(label string, t dashboard.Totals) []string {
	return []string{
		label,
		formatInt(t.SessionsStarted),
		formatInt(t.Prompts),
		formatInt(t.ToolCalls),
		formatInt(t.InputTokens),
		formatInt(t.OutputTokens),
		formatInt(t.CacheReadTokens),
		formatCost(t.CostUSD),
	}
}

func printStats(w io.Writer, res statsResult, g statsGroup) {
	fmt.Fprintf(w, "구간: %s ~ %s (%s) · 축: %s\n",
		formatUnixLocal(res.Since), formatUnixLocal(res.Until), res.Timezone, res.Group)
	fmt.Fprintf(w, "DB: %s\n", res.DatabasePath)
	if !res.Available {
		fmt.Fprintln(w, "로컬 데이터가 아직 없습니다 — 데몬이 실행된 적이 없거나 로컬 파이프라인이 꺼져 있습니다.")
		return
	}
	if len(res.Rows) == 0 {
		fmt.Fprintln(w, "이 구간에 집계된 데이터가 없습니다.")
		return
	}

	cols := make([]column, len(statsColumns))
	copy(cols, statsColumns)
	cols[0].Header = g.Header

	rows := make([][]string, 0, len(res.Rows)+1)
	for _, r := range res.Rows {
		// Label 을 쓴다. dim=project 의 Key 는 해시라 사람이 읽을 수 없다 (JSON 에는 둘 다 있다).
		rows = append(rows, statsCells(r.Label, r.Totals))
	}
	rows = append(rows, statsCells("합계", res.Total))
	writeTable(w, cols, rows)

	if g.Bucket == dashboard.BucketKey {
		fmt.Fprintln(w, "합계는 구간 전체 값입니다. --limit 으로 잘린 행이 있으면 표의 합보다 큽니다.")
	}
}

// writeJSON 은 결과를 사람이 읽을 수 있는 들여쓰기로 내보낸다. 파이프로 넘기는 것이
// 목적이지만 눈으로 확인하는 일도 잦아서 압축하지 않는다.
func writeJSON(stdout, stderr io.Writer, v any) int {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "오류: JSON 생성 실패:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
