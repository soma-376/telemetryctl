package main

// 로컬 조회 명령(stats·sessions·purge·status)이 공유하는 조각들이다.
//
// 세 가지를 여기에 모은다. (1) --since 문법, (2) --group 어휘, (3) "어느 DB 를 볼 것인가".
// 특히 (3)이 흩어지면 CLI 가 데몬이 쓰는 것과 다른 파일을 읽고도 "데이터 없음" 이라고만
// 말하게 된다 — 우선순위를 데몬(internal/daemon/runner.go 의 resolveDataDir)과 한 곳에서
// 맞춰 둔다.

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/hostenv"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/store"
)

const (
	// defaultSince 는 --since 기본값이다 (계획서 CLI 표의 `--since 7d`).
	defaultSince = 7 * 24 * time.Hour
	// maxSinceDays 는 --since 상한(일)이다. 세션 계층 보존 기간과 같다 — 그보다 오래된
	// 데이터는 어차피 없으므로, 더 긴 구간을 받아 봐야 빈 구간만 늘어난다.
	maxSinceDays = 400
	maxSince     = maxSinceDays * 24 * time.Hour
)

// parseSince 는 --since 값을 기간으로 옮긴다. 빈 값은 기본값(7일)이다.
//
// time.ParseDuration 은 d(일)·w(주) 를 모른다. "7d" 를 그대로 넘기면 사용자에게
// `time: unknown unit "d"` 가 그대로 나가는데, 그 문장은 우리 문법을 설명하지 못한다.
// 그래서 일·주 접미사만 여기서 처리하고 나머지(24h·90m·1h30m)는 표준 파서에 맡긴다.
func parseSince(v string) (time.Duration, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return defaultSince, nil
	}

	var d time.Duration
	switch last := s[len(s)-1]; last {
	case 'd', 'w':
		n, err := strconv.Atoi(strings.TrimSpace(s[:len(s)-1]))
		if err != nil {
			return 0, sinceSyntaxError(v)
		}
		// 상한을 곱셈 **전에** 본다. 큰 수를 먼저 곱하면 time.Duration(int64 나노초)이
		// 넘쳐 음수가 되고, 그러면 "0 보다 커야 합니다" 라는 엉뚱한 이유로 거부된다.
		if n > maxSinceDays {
			return 0, sinceRangeError(v)
		}
		unit := 24 * time.Hour
		if last == 'w' {
			unit = 7 * 24 * time.Hour
		}
		d = time.Duration(n) * unit
	default:
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, sinceSyntaxError(v)
		}
	}

	switch {
	case d <= 0:
		return 0, fmt.Errorf("--since 는 0 보다 커야 합니다: %q", v)
	case d > maxSince:
		return 0, sinceRangeError(v)
	}
	return d, nil
}

func sinceSyntaxError(v string) error {
	return fmt.Errorf("--since 값을 해석할 수 없습니다 %q: 숫자와 단위(s·m·h·d·w)로 쓰세요 (예: 7d, 24h, 90m)", v)
}

func sinceRangeError(v string) error {
	return fmt.Errorf("--since 는 %d일을 넘을 수 없습니다 (보존 상한): %q", maxSinceDays, v)
}

// statsGroup 은 --group 값 하나를 dashboard 조회 축으로 옮긴 것이다.
//
// 계획서의 `vendor|model|tool|project|day` 다섯 개가 전부다. day 만 성격이 다른데,
// 나머지가 "무엇으로 나눌까" 인 반면 day 는 "언제로 나눌까" 라서 dim 이 아니라 bucket 이다.
type statsGroup struct {
	Name   string
	Dim    dashboard.Dim
	Bucket dashboard.BucketBy
	// Header 는 표 첫 열의 제목이다.
	Header string
}

var statsGroups = []statsGroup{
	{Name: "vendor", Dim: dashboard.DimVendor, Bucket: dashboard.BucketKey, Header: "벤더"},
	{Name: "model", Dim: dashboard.DimModel, Bucket: dashboard.BucketKey, Header: "모델"},
	{Name: "tool", Dim: dashboard.DimTool, Bucket: dashboard.BucketKey, Header: "툴"},
	{Name: "project", Dim: dashboard.DimProject, Bucket: dashboard.BucketKey, Header: "프로젝트"},
	{Name: "day", Dim: dashboard.DimTotal, Bucket: dashboard.BucketDay, Header: "날짜"},
}

// parseGroup 은 --group 값을 고른다. 오타를 조용히 기본값으로 떨어뜨리지 않는다 —
// 그러면 사용자는 자기가 요청한 것과 다른 표를 보면서 그 사실을 모른다.
func parseGroup(v string) (statsGroup, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return statsGroups[0], nil
	}
	for _, g := range statsGroups {
		if g.Name == s {
			return g, nil
		}
	}
	names := make([]string, 0, len(statsGroups))
	for _, g := range statsGroups {
		names = append(names, g.Name)
	}
	return statsGroup{}, fmt.Errorf("--group 값이 올바르지 않습니다 %q: %s 중 하나여야 합니다",
		v, strings.Join(names, "|"))
}

// sessionStatuses 는 --status 가 받는 값이다. 스키마의 sessions.status 어휘와 같다
// (internal/session/session.go). abandoned·handoff 는 휴리스틱 판정이라 계획서가 필터
// 용도로만 쓰라고 했고, 여기서도 필터일 뿐이다.
var sessionStatuses = []string{"running", "completed", "abandoned", "handoff"}

// parseStatuses 는 --status 값을 나눈다. 쉼표로 여러 개를 받을 수 있다.
func parseStatuses(v string) ([]string, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return nil, nil
	}
	out := make([]string, 0, 2)
	for _, part := range strings.Split(s, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if !containsString(sessionStatuses, p) {
			return nil, fmt.Errorf("--status 값이 올바르지 않습니다 %q: %s 중 하나여야 합니다",
				p, strings.Join(sessionStatuses, "|"))
		}
		if !containsString(out, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// localTarget 은 "어느 로컬 데이터를 볼 것인가" 와 그것을 정한 근거다.
type localTarget struct {
	StatePath string
	// State 는 설치 상태다. nil 이면 미설치이고, 그것은 에러가 아니다.
	State *installer.State
	// StateErr 는 상태 파일을 읽지 못한 이유다. status 는 이것을 그대로 보고하고,
	// 조회 명령은 경고만 하고 기본 경로로 진행한다.
	StateErr error

	DataDir string
	DBPath  string
}

// resolveLocalTarget 은 데이터 디렉터리를 --data-dir > state.Local.DataDir > 기본값
// (~/.pulsemetry) 순으로 정한다. 데몬의 resolveDataDir 와 같은 우선순위여야 한다 —
// 어긋나면 CLI 가 데몬이 쓰는 것과 다른 파일을 읽고 "데이터 없음" 만 반복한다.
func resolveLocalTarget(dataDirFlag, statePathFlag string) (localTarget, error) {
	t := localTarget{StatePath: strings.TrimSpace(statePathFlag)}
	if t.StatePath == "" {
		p, err := installer.DefaultStatePath()
		if err != nil {
			return localTarget{}, err
		}
		t.StatePath = p
	}
	t.State, t.StateErr = installer.LoadState(t.StatePath)

	switch {
	case strings.TrimSpace(dataDirFlag) != "":
		t.DataDir = strings.TrimSpace(dataDirFlag)
	case t.State != nil && t.State.Local.DataDir != "":
		t.DataDir = t.State.Local.DataDir
	default:
		env, err := hostenv.Detect()
		if err != nil {
			return localTarget{}, fmt.Errorf("데이터 디렉터리 판별 실패: %w", err)
		}
		t.DataDir = store.DefaultDataDir(env)
	}
	t.DBPath = store.PathIn(t.DataDir)
	return t, nil
}

// warnStateErr 는 상태 파일을 읽지 못했다는 사실을 조회 명령이 알리는 방법이다.
// 조회를 중단하지는 않는다 — state.json 이 깨졌어도 DB 는 멀쩡할 수 있고, 그때 stats 가
// 안 도는 것은 사용자에게 아무 도움이 되지 않는다.
func warnStateErr(stderr io.Writer, t localTarget) {
	if t.StateErr != nil {
		fmt.Fprintf(stderr, "경고: 상태 파일을 읽지 못해 기본 데이터 디렉터리를 씁니다 (%v)\n", t.StateErr)
	}
}

// openReader 는 로컬 데이터 조회 핸들을 연다. DB 파일이 없어도 에러가 아니다 —
// Reader.Available() 이 false 인 채로 열리고 모든 조회가 빈 결과를 준다.
func openReader(t localTarget) (*dashboard.Reader, error) {
	return dashboard.Open(t.DBPath)
}
