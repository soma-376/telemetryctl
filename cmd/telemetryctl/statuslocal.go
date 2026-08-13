package main

// status 의 로컬 파이프라인 블록 (계획서: "기존 printCredentialStatus 의 '존재 여부만,
// 값은 절대 출력 안 함' 스타일을 따른다").
//
// # 여기 출력되지 않는 것
//
// loopback ingest 토큰의 **값** 은 어떤 경우에도 출력하지 않는다. 존재 여부만 말한다.
// dashboard.Status·runtimeinfo.Info 는 애초에 비밀을 담지 않도록 설계돼 있어(각 패키지
// 주석) 여기서는 키링 토큰 하나만 조심하면 된다. 이 블록의 출력은 사용자가 이슈에
// 스크린샷으로 붙이는 것이 정상 사용법이라는 전제로 읽어야 한다.
//
// # 어떤 상태에서도 동작해야 한다
//
// status 는 진단 명령이다. 미설치·DB 없음·데몬 꺼짐은 전부 정상 상태이고 "설정 안 됨"
// 으로 표시할 뿐 에러가 아니다. 조회 실패도 블록 전체를 죽이지 않는다.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/autostart"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/receiver"
)

// healthTimeout 은 /healthz 확인 한 번의 상한이다. status 는 즉답해야 하는 명령이라
// 데몬이 응답하지 않을 때 오래 매달리면 안 된다.
const healthTimeout = 2 * time.Second

// autostartTimeout 은 서비스 관리자 조회 한 번의 상한이다 (PROJ-55).
//
// healthTimeout 과 같은 이유로 짧다. launchctl·systemctl 은 보통 즉답하지만 관리자가
// 바쁘거나 D-Bus 가 응답하지 않으면 매달릴 수 있고, status 가 그것 때문에 멈추면 안 된다.
// 시간 초과는 오류가 아니라 "확인 실패" 한 줄로 보고한다.
const autostartTimeout = 2 * time.Second

// healthResponse 는 /healthz 응답 중 status 가 보여 주는 부분이다.
// receiver 의 응답 타입은 비공개라 필요한 필드만 여기서 다시 선언한다.
type healthResponse struct {
	Status        string         `json:"status"`
	QueueDepth    int            `json:"queue_depth"`
	QueueCapacity int            `json:"queue_capacity"`
	Stats         receiver.Stats `json:"stats"`
}

// printLocalStatus 는 status 의 로컬 파이프라인 블록을 출력한다.
func printLocalStatus(w io.Writer, t localTarget) {
	fmt.Fprintln(w, "  로컬 파이프라인:")
	printLocalSettings(w, t)
	fmt.Fprintf(w, "    데이터 디렉터리: %s\n", t.DataDir)

	reader, err := openReader(t)
	if err != nil {
		fmt.Fprintf(w, "    DB: 열기 실패 (%v)\n", err)
		printIngestTokenStatus(w)
		return
	}
	defer reader.Close() //nolint:errcheck // 조회 전용 핸들이다

	ctx, cancel := context.WithTimeout(context.Background(), healthTimeout)
	defer cancel()

	st, err := reader.Status(ctx)
	if err != nil {
		fmt.Fprintf(w, "    DB: 상태 조회 실패 (%v)\n", err)
		printIngestTokenStatus(w)
		return
	}

	printDatabaseStatus(w, st)
	printDaemonStatus(w, st)
	printAutostartStatus(w)
	printIngestTokenStatus(w)
	printDataStatus(w, st)
}

// printAutostartStatus 는 자동 실행 등록 상태를 출력한다 (PROJ-55).
//
// printDaemonStatus 바로 뒤에 두는 이유는 **그 지점에서 독자의 질문이 바뀌기 때문**이다.
// 방금 "데몬: 실행 안 됨" 을 읽은 사람이 다음으로 묻는 것은 "왜, 그리고 돌아오긴 하나?"
// 이고, 제자리에서 답하는 편이 화면 아래쪽의 별도 블록보다 낫다.
//
// **절대 멈추면 안 된다.** status 는 진단 명령이라 어떤 상태에서도 동작해야 한다
// (이 파일 머리말). 미지원 환경·조회 실패·시간 초과는 전부 한 줄로 보고하고 넘어간다.
func printAutostartStatus(w io.Writer) {
	st, err := currentAutostartStatus()
	if err != nil {
		if errors.Is(err, autostart.ErrUnsupportedPlatform) {
			// 실패한 것이 없다. Windows 사용자에게 경고 어조를 쓰지 않는다 (PROJ-56).
			fmt.Fprintln(w, "    자동 실행: 지원하지 않는 환경 (Windows 는 후속 티켓입니다)")
			return
		}
		fmt.Fprintf(w, "    자동 실행: 확인 실패 (%v)\n", err)
		return
	}

	fmt.Fprintf(w, "    %s\n", autostartSummary(st))
	if !st.Registered {
		return
	}
	fmt.Fprintf(w, "    자동 실행 파일: %s\n", st.UnitPath)
	switch {
	case st.LogPath != "":
		fmt.Fprintf(w, "    자동 실행 로그: %s\n", st.LogPath)
	case st.Kind == autostart.KindSystemdUser:
		fmt.Fprintf(w, "    자동 실행 로그: %s\n", autostart.JournalCommand)
	}
}

// printLocalSettings 는 state.Local 이 담은 **의도** 를 보여 준다. 실제로 도는 값은
// 데몬 줄에서 따로 보여 준다 — 둘이 갈리는 것이 포트 폴백이 일어났다는 신호다.
func printLocalSettings(w io.Writer, t localTarget) {
	if t.State == nil {
		fmt.Fprintln(w, "    설정: 없음 (설치 상태 파일이 없어 기본 경로로 조회합니다)")
		return
	}
	l := t.State.Local
	rewire := "꺼짐 (기본값 — 벤더 설정은 회사 Collector 로 직결)"
	if l.Enabled {
		rewire = "켜짐 (벤더 설정이 로컬 수신기를 가리킴)"
	}
	port := l.ListenPort
	portNote := ""
	if port == 0 {
		port = receiver.DefaultPort
		portNote = " (기본값)"
	}
	content := "끔"
	if l.StoreContent {
		content = "켬"
	}
	fmt.Fprintf(w, "    재배선: %s\n", rewire)
	fmt.Fprintf(w, "    수신 포트(설정): %d%s · 보존: %d일 · 원문 보관: %s\n",
		port, portNote, l.RetentionDays, content)
}

func printDatabaseStatus(w io.Writer, st dashboard.Status) {
	if !st.Available {
		fmt.Fprintf(w, "    DB: 설정 안 됨 (파일 없음: %s)\n", st.DatabasePath)
		return
	}
	schema := fmt.Sprintf("스키마 v%d", st.SchemaVersion)
	if st.SchemaVersion != st.LatestSchemaVersion {
		// 조용히 넘어가면 "왜 새 컬럼이 비어 있나" 를 아무도 설명하지 못한다.
		schema += fmt.Sprintf(" (이 바이너리가 아는 최신은 v%d)", st.LatestSchemaVersion)
	}
	fmt.Fprintf(w, "    DB: %s (%s · %s)\n", st.DatabasePath, formatBytes(st.DatabaseBytes), schema)
	if st.RetentionDays > 0 {
		fmt.Fprintf(w, "    보존(적용값): 이벤트 %d일\n", st.RetentionDays)
	}
}

func printDaemonStatus(w io.Writer, st dashboard.Status) {
	d := st.Daemon
	switch {
	case !d.Found:
		fmt.Fprintln(w, "    데몬: 실행 안 됨 (runtime.json 없음)")
		return
	case d.Stale:
		fmt.Fprintf(w, "    데몬: 실행 안 됨 (낡은 runtime.json — pid %d 가 살아 있지 않음)\n", d.PID)
	default:
		// pid 는 재사용되므로 "아마 실행 중" 이다. 확정은 아래 /healthz 가 한다.
		fmt.Fprintf(w, "    데몬: 실행 중으로 보임 (pid %d · 기동 %s)\n", d.PID, orDash(d.StartedAt))
	}
	fmt.Fprintf(w, "    수신 endpoint: %s · 포트 %d\n", orDash(d.Endpoint), d.ListenPort)
	if len(d.ListenAddrs) > 0 {
		fmt.Fprintf(w, "    리스너: %s\n", strings.Join(d.ListenAddrs, ", "))
	}
	// 낡은 파일이면 두드리지 않는다. pid 가 죽은 것은 이미 확정이라 얻을 정보가 없고,
	// 그 사이 다른 프로그램이 그 포트를 잡았다면 남의 서버에 요청을 보내는 셈이 된다.
	if d.Endpoint == "" || d.Stale {
		return
	}
	if h, err := probeHealth(d.Endpoint); err != nil {
		fmt.Fprintf(w, "    헬스체크: 응답 없음 (%v)\n", err)
	} else {
		fmt.Fprintf(w, "    헬스체크: %s · 큐 %d/%d\n", orDash(h.Status), h.QueueDepth, h.QueueCapacity)
		fmt.Fprintf(w, "    수신 카운터: 접수 %s · 폐기 %s · 디코드 %s(실패 %s) · 인증실패 %s · 크기초과 %s\n",
			formatInt(h.Stats.Accepted), formatInt(h.Stats.Dropped),
			formatInt(h.Stats.Decoded), formatInt(h.Stats.DecodeErrors),
			formatInt(h.Stats.Unauthorized), formatInt(h.Stats.TooLarge))
	}
}

// printIngestTokenStatus 는 loopback ingest 토큰의 **존재 여부만** 말한다.
// printCredentialStatus 와 같은 규칙이다 (§4.5) — 값은 어떤 경우에도 출력하지 않는다.
func printIngestTokenStatus(w io.Writer) {
	_, found, err := credential.Get(credential.AccountLocalIngest)
	switch {
	case err != nil:
		fmt.Fprintf(w, "    ingest 토큰: 읽기 실패 (%v)\n", err)
	case !found:
		fmt.Fprintln(w, "    ingest 토큰: 없음 (데몬이 처음 뜰 때 만듭니다)")
	default:
		fmt.Fprintln(w, "    ingest 토큰: 있음 (OS 키링)")
	}
}

func printDataStatus(w io.Writer, st dashboard.Status) {
	if !st.Available {
		return
	}
	c := st.Counts
	fmt.Fprintf(w, "    데이터: 세션 %s개(진행 중 %s) · 이벤트 %s · 원문 %s · 툴 %s · 파일 %s · 롤업 %s · 벤더 %s\n",
		formatInt(c.Sessions), formatInt(st.RunningSessions), formatInt(c.Events),
		formatInt(c.EventContent), formatInt(c.ToolEvents), formatInt(c.SessionFiles),
		formatInt(c.RollupHourly), formatInt(c.Vendors))
	fmt.Fprintf(w, "    이벤트 구간: %s ~ %s (%s)\n",
		formatUnixLocal(st.OldestEventAt), formatUnixLocal(st.NewestEventAt), zoneLabel(time.Now()))
	fmt.Fprintf(w, "    마지막 롤업: %s\n", formatUnixLocal(st.LastRollupAt))
	if len(st.ActiveVendors) > 0 {
		fmt.Fprintf(w, "    활성 벤더: %s\n", strings.Join(st.ActiveVendors, ", "))
	}
}

// probeHealth 는 /healthz 를 한 번 두드린다. runtime.json 의 pid 판정이 pid 재사용
// 때문에 확정적이지 않아서, "정말 우리 데몬인가" 는 여기서만 확인할 수 있다
// (runtimeinfo.Load 주석). 응답 본문에는 좌표와 카운터만 있고 비밀은 없다.
func probeHealth(endpoint string) (healthResponse, error) {
	client := &http.Client{Timeout: healthTimeout}
	resp, err := client.Get(endpoint + receiver.HealthPath)
	if err != nil {
		return healthResponse{}, err
	}
	defer resp.Body.Close() //nolint:errcheck // 읽기 끝난 응답이다
	if resp.StatusCode != http.StatusOK {
		return healthResponse{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var h healthResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&h); err != nil {
		return healthResponse{}, fmt.Errorf("응답 해석 실패: %w", err)
	}
	return h, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
