package session

import (
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
)

// 테스트 입력 빌더. 이벤트 필드가 많아 케이스마다 구조체를 다 쓰면 무엇을 검증하는지가 묻힌다.

const testInstallation = "inst-test"

func logEv(sessionID, name string, ts int64, opts ...func(*Input)) Input {
	in := Input{Event: event.Event{
		Vendor:         "claude_code",
		InstallationID: testInstallation,
		Signal:         event.SignalLog,
		Name:           name,
		TS:             event.UnixSec(ts).Nano(),
		SessionID:      sessionID,
	}}
	for _, o := range opts {
		o(&in)
	}
	return in
}

func metricEv(sessionID, name string, ts int64, value float64, opts ...func(*Input)) Input {
	in := logEv(sessionID, name, ts)
	in.Event.Signal = event.SignalMetric
	in.Event.Temporality = event.TemporalityDelta
	in.Event.Measure.Value = event.Some(value)
	// opts 를 마지막에 적용해야 temporality(...) 같은 옵션이 기본값에 덮이지 않는다.
	for _, o := range opts {
		o(&in)
	}
	return in
}

func vendor(v string) func(*Input) { return func(i *Input) { i.Event.Vendor = v } }
func typ(v string) func(*Input)    { return func(i *Input) { i.Event.Attr.Type = v } }
func tool(v string) func(*Input)   { return func(i *Input) { i.Event.Attr.ToolName = v } }
func mcp(v string) func(*Input)    { return func(i *Input) { i.Event.Attr.MCPServer = v } }
func decide(v string) func(*Input) { return func(i *Input) { i.Event.Attr.Decision = v } }
func success(v bool) func(*Input)  { return func(i *Input) { i.Event.Measure.Success = event.Some(v) } }
func attempt(v int64) func(*Input) { return func(i *Input) { i.Event.Measure.Attempt = event.Some(v) } }
func response(v int64) func(*Input) {
	return func(i *Input) { i.Event.Measure.ResponseLength = event.Some(v) }
}

func project(p string) func(*Input) {
	return func(i *Input) { i.Event.Attr = i.Event.Attr.WithProject(event.NormalizePath(p)) }
}

func target(p string) func(*Input) {
	return func(i *Input) { i.Target = event.NormalizePath(p) }
}

func prompt(body string) func(*Input) {
	return func(i *Input) {
		i.Kind = ContentPrompt
		i.Body = body
	}
}

func temporality(t event.Temporality) func(*Input) {
	return func(i *Input) { i.Event.Temporality = t }
}

// only 는 세션 하나만 있는 결과에서 그 세션을 꺼낸다.
func only(t *testing.T, ss []Session) Session {
	t.Helper()
	if len(ss) != 1 {
		t.Fatalf("세션이 1개가 아님: %d", len(ss))
	}
	return ss[0]
}

// assertLineInvariant 는 계획서 「테스트 전략」이 요구한 불변식을 확인한다 —
// 파일별 배분 합계가 세션 합계를 넘지 않는다. 등식(배분 + 미배분 = 합계)까지 본다.
func assertLineInvariant(t *testing.T, s Session) {
	t.Helper()
	var added, removed int64
	for _, f := range s.Files {
		if f.LinesAdded < 0 || f.LinesRemoved < 0 {
			t.Fatalf("파일 라인이 음수: %+v", f)
		}
		added += f.LinesAdded
		removed += f.LinesRemoved
	}
	if added > s.LinesAdded {
		t.Fatalf("파일별 added 합 %d 가 세션 합계 %d 를 초과", added, s.LinesAdded)
	}
	if removed > s.LinesRemoved {
		t.Fatalf("파일별 removed 합 %d 가 세션 합계 %d 를 초과", removed, s.LinesRemoved)
	}
	if got := added + s.Diag.UnattributedLinesAdded; got != s.LinesAdded {
		t.Fatalf("added 배분+미배분 = %d, 세션 합계 = %d", got, s.LinesAdded)
	}
	if got := removed + s.Diag.UnattributedLinesRemoved; got != s.LinesRemoved {
		t.Fatalf("removed 배분+미배분 = %d, 세션 합계 = %d", got, s.LinesRemoved)
	}
}
