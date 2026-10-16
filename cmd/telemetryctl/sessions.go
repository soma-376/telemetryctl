package main

// telemetryctl sessions — 로컬 세션 목록 조회 (계획서 「CLI 변경」).

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
)

// sessionsResult 는 --json 출력이자 사람용 표의 원본이다 (statsResult 와 같은 이유).
//
// Sessions 는 dashboard.SessionRow 를 그대로 싣는다. CLI 가 필드를 골라 담으면 GUI 와
// CLI 가 같은 세션을 다른 이름으로 부르게 된다 — 조회 계약은 dashboard 가 소유한다.
type sessionsResult struct {
	Available    bool   `json:"available"`
	DatabasePath string `json:"database_path"`

	// Since·Until 은 started_at 기준 구간이다 (UTC unix 초, Until 배타).
	// **JSON 의 모든 시각은 UTC unix 초** 다. 사람용 표만 로컬 시간대로 옮긴다.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	// Status 는 적용한 필터다. 비어 있으면 전체.
	Status           []string `json:"status"`
	Limit            int      `json:"limit"`
	Count            int      `json:"count"`
	Timezone         string   `json:"timezone"`
	UTCOffsetSeconds int      `json:"utc_offset_seconds"`

	Sessions []dashboard.SessionRow `json:"sessions"`
}

func cmdSessions(args []string) int { return runSessions(os.Stdout, os.Stderr, args) }

func runSessions(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	since := fs.String("since", "7d", "조회 구간 (7d·24h·90m 처럼 지금부터 거슬러 올라간다)")
	status := fs.String("status", "", "세션 상태 필터 (running|completed|abandoned|handoff, 쉼표로 여러 개)")
	asJSON := fs.Bool("json", false, "사람용 표 대신 JSON 출력")
	limit := fs.Int("limit", 50, "표시할 세션 수 상한 (1~1000)")
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
	statuses, err := parseStatuses(*status)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 2
	}
	if *limit < 1 || *limit > 1000 {
		fmt.Fprintf(stderr, "오류: --limit 은 1~1000 이어야 합니다: %d\n", *limit)
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
	res, err := collectSessions(context.Background(), reader, now, window, statuses, *limit)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}

	if *asJSON {
		return writeJSON(stdout, stderr, res)
	}
	printSessions(stdout, res)
	return 0
}

func collectSessions(ctx context.Context, reader *dashboard.Reader, now time.Time, window time.Duration, statuses []string, limit int) (sessionsResult, error) {
	from := now.Add(-window).Unix()
	to := now.Unix()

	res := sessionsResult{
		Available:        reader.Available(),
		DatabasePath:     reader.Path(),
		Since:            from,
		Until:            to,
		Status:           statuses,
		Limit:            limit,
		Timezone:         zoneLabel(now),
		UTCOffsetSeconds: zoneOffsetSeconds(now),
		Sessions:         []dashboard.SessionRow{},
	}
	if res.Status == nil {
		res.Status = []string{}
	}

	// Until 을 넘기지 않는다. 진행 중인 세션의 started_at 이 미래일 수는 없고, 상한을 걸면
	// 시계가 조금 어긋난 환경에서 방금 시작한 세션이 목록에서 빠진다.
	rows, err := reader.Sessions(ctx, dashboard.SessionQuery{
		Since:  from,
		Status: statuses,
		Limit:  limit,
	})
	if err != nil {
		return sessionsResult{}, err
	}
	res.Sessions = rows
	res.Count = len(rows)
	return res, nil
}

// sessionColumns 는 Activity 화면의 세션 목록에 대응하는 열이다. 제목은 길이가 들쭉날쭉해
// 맨 뒤에 둔다 — 앞에 두면 뒤 열들이 행마다 밀린다.
var sessionColumns = []column{
	{Header: "시작"},
	{Header: "소요", Right: true},
	{Header: "상태"},
	{Header: "벤더"},
	{Header: "프로젝트"},
	{Header: "툴", Right: true},
	{Header: "비용(USD)", Right: true},
	{Header: "제목"},
}

func sessionCells(s dashboard.SessionRow) []string {
	project := s.ProjectName
	if project == "" {
		project = "-"
	}
	title := s.Title
	if title == "" {
		title = "-"
	}
	return []string{
		formatUnixLocal(s.StartedAt),
		formatDurationMS(s.DurationMS),
		s.Status,
		s.Vendor,
		project,
		formatInt(s.ToolCalls),
		formatCost(s.CostUSD),
		title,
	}
}

func printSessions(w io.Writer, res sessionsResult) {
	filter := "전체"
	if len(res.Status) > 0 {
		filter = strings.Join(res.Status, ",")
	}
	fmt.Fprintf(w, "구간: %s ~ %s (%s) · 상태: %s\n",
		formatUnixLocal(res.Since), formatUnixLocal(res.Until), res.Timezone, filter)
	fmt.Fprintf(w, "DB: %s\n", res.DatabasePath)
	if !res.Available {
		fmt.Fprintln(w, "로컬 데이터가 아직 없습니다 — 데몬이 실행된 적이 없거나 로컬 파이프라인이 꺼져 있습니다.")
		return
	}
	if res.Count == 0 {
		fmt.Fprintln(w, "이 구간에 세션이 없습니다.")
		return
	}

	rows := make([][]string, 0, res.Count)
	for _, s := range res.Sessions {
		rows = append(rows, sessionCells(s))
	}
	writeTable(w, sessionColumns, rows)

	fmt.Fprintf(w, "세션 %d개\n", res.Count)
	if res.Count == res.Limit {
		// 상한에 딱 걸린 것과 실제로 그만큼인 것을 구분할 방법이 없으므로 알려만 준다.
		// 조용히 자르면 사용자는 목록이 전부인 줄 안다.
		fmt.Fprintf(w, "상한 %d행에 도달했습니다 — 더 있을 수 있으니 --limit 을 늘리세요.\n", res.Limit)
	}
}
