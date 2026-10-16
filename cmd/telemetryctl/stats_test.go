package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// DB 가 없는 상태는 정상이다 (미설치·데몬 첫 실행 전). 여기서 에러로 죽으면 사용자는
// "아직 데이터가 없다" 와 "명령이 고장났다" 를 구분할 수 없다.
func TestStatsWithoutDatabase(t *testing.T) {
	_, args := tempTarget(t)

	res := runCmd(t, runStats, args...)
	if res.code != 0 {
		t.Fatalf("code = %d, want 0 (stderr: %s)", res.code, res.stderr)
	}
	res.mustContain(t, "로컬 데이터가 아직 없습니다")

	jsonRes := runCmd(t, runStats, append(args, "--json")...)
	if jsonRes.code != 0 {
		t.Fatalf("--json code = %d, want 0 (stderr: %s)", jsonRes.code, jsonRes.stderr)
	}
	var parsed statsResult
	if err := json.Unmarshal([]byte(jsonRes.stdout), &parsed); err != nil {
		t.Fatalf("JSON 파싱: %v\n%s", err, jsonRes.stdout)
	}
	if parsed.Available {
		t.Error("available = true, want false (DB 파일이 없다)")
	}
	if parsed.Rows == nil {
		t.Error("rows = null, want [] (기계 판독 측이 null 에 반복문을 걸면 터진다)")
	}
}

func TestStatsInvalidFlags(t *testing.T) {
	_, args := tempTarget(t)
	tests := []struct {
		name string
		flag []string
	}{
		{name: "잘못된 --since", flag: []string{"--since", "7일"}},
		{name: "잘못된 --group", flag: []string{"--group", "vendors"}},
		{name: "범위 밖 --limit", flag: []string{"--limit", "0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runCmd(t, runStats, append(append([]string{}, args...), tt.flag...)...)
			if res.code != 2 {
				t.Fatalf("code = %d, want 2", res.code)
			}
			if !strings.Contains(res.stderr, "오류:") {
				t.Errorf("stderr 에 오류 설명이 없다: %q", res.stderr)
			}
		})
	}
}

// 사람용 표와 --json 이 같은 구조체에서 나오는지 확인한다. 둘이 갈리면 사용자가 화면에서
// 본 숫자와 스크립트가 읽은 숫자가 달라지고, 그 어긋남은 아무 데서도 실패하지 않는다.
func TestStatsHumanMatchesJSON(t *testing.T) {
	dir, args := tempTarget(t)
	seed(t, dir, time.Now().Add(-30*time.Minute))

	jsonRes := runCmd(t, runStats, append(args, "--json")...)
	if jsonRes.code != 0 {
		t.Fatalf("--json code = %d (stderr: %s)", jsonRes.code, jsonRes.stderr)
	}
	var parsed statsResult
	if err := json.Unmarshal([]byte(jsonRes.stdout), &parsed); err != nil {
		t.Fatalf("JSON 파싱: %v\n%s", err, jsonRes.stdout)
	}
	if !parsed.Available {
		t.Fatal("available = false, want true")
	}
	if parsed.Group != "vendor" {
		t.Errorf("group = %q, want vendor", parsed.Group)
	}
	if len(parsed.Rows) != 2 {
		t.Fatalf("rows = %d개, want 2 (claude_code·codex): %s", len(parsed.Rows), jsonRes.stdout)
	}
	if parsed.Since >= parsed.Until {
		t.Errorf("since=%d until=%d, want since < until", parsed.Since, parsed.Until)
	}

	human := runCmd(t, runStats, args...)
	if human.code != 0 {
		t.Fatalf("code = %d (stderr: %s)", human.code, human.stderr)
	}
	for _, row := range parsed.Rows {
		assertTableRow(t, human.stdout, statsCells(row.Label, row.Totals))
	}
	// 합계 행도 JSON 의 total 에서 온다.
	assertTableRow(t, human.stdout, statsCells("합계", parsed.Total))

	// 축별 행의 합이 전체 합계와 맞는지 (잘린 행이 없는 구간이므로 같아야 한다).
	var cost float64
	for _, row := range parsed.Rows {
		cost += row.CostUSD
	}
	if diff := cost - parsed.Total.CostUSD; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("행 합계 %v 와 total %v 가 다르다", cost, parsed.Total.CostUSD)
	}
}

// --group day 는 dim 이 아니라 시간 축이라 경로가 다르다. 빈 날도 0 행으로 채워야
// 그래프에 구멍이 생기지 않는다 (dashboard.emptySkeleton).
func TestStatsGroupDay(t *testing.T) {
	dir, args := tempTarget(t)
	at := time.Now().Add(-30 * time.Minute)
	seed(t, dir, at)

	res := runCmd(t, runStats, append(args, "--group", "day", "--since", "3d", "--json")...)
	if res.code != 0 {
		t.Fatalf("code = %d (stderr: %s)", res.code, res.stderr)
	}
	var parsed statsResult
	if err := json.Unmarshal([]byte(res.stdout), &parsed); err != nil {
		t.Fatalf("JSON 파싱: %v", err)
	}
	if len(parsed.Rows) < 3 {
		t.Fatalf("rows = %d개, want 3일치 이상 (빈 날도 채운다): %s", len(parsed.Rows), res.stdout)
	}

	// 시각이 아니라 **날짜** 로 찾는다. 자정 직전에 도는 테스트가 마지막 행을 오늘로
	// 가정하면 하루 중 언제 도느냐에 따라 흔들린다.
	day := at.Format("2006-01-02")
	found := false
	for _, row := range parsed.Rows {
		if row.Key != day {
			continue
		}
		found = true
		if row.CostUSD == 0 {
			t.Errorf("%s 행의 비용이 0 이다: %+v", day, row)
		}
		if row.StartAt == 0 {
			t.Errorf("%s 행의 start_at 이 0 이다 (그래프가 x축을 못 잡는다)", day)
		}
	}
	if !found {
		t.Errorf("%s 날짜 행이 없다: %s", day, res.stdout)
	}
}

// dim=project 의 key 는 해시다. 표에는 사람이 읽을 이름이 나와야 한다.
func TestStatsGroupProjectShowsName(t *testing.T) {
	dir, args := tempTarget(t)
	at := time.Now().Add(-30 * time.Minute)
	seed(t, dir, at)
	seedProjectRollup(t, dir, at)

	res := runCmd(t, runStats, append(args, "--group", "project")...)
	if res.code != 0 {
		t.Fatalf("code = %d (stderr: %s)", res.code, res.stderr)
	}
	res.mustContain(t, "telemetryctl")
	if strings.Contains(res.stdout, "hash-a") {
		t.Errorf("프로젝트 해시가 그대로 표에 나왔다:\n%s", res.stdout)
	}
}

// assertTableRow 는 셀 값들이 한 줄에 그 순서대로 들어 있는지 본다. 열 폭 때문에 공백
// 개수는 달라지므로 필드로 쪼개 비교한다.
func assertTableRow(t *testing.T, out string, cells []string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != len(cells) || fields[0] != cells[0] {
			continue
		}
		for i := range cells {
			if fields[i] != cells[i] {
				t.Errorf("%q 행의 %d번째 값 = %q, want %q", cells[0], i, fields[i], cells[i])
			}
		}
		return
	}
	t.Errorf("표에 %v 행이 없다:\n%s", cells, out)
}
