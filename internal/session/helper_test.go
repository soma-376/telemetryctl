package session

import (
	"reflect"
	"strings"
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
		i.Content = event.Content{Kind: event.ContentPrompt, Body: body}
	}
}

func temporality(t event.Temporality) func(*Input) {
	return func(i *Input) { i.Event.Temporality = t }
}

// startedAt 은 누적 계열의 start_time 을 unix 초로 정한다.
// 조립기의 관측 기준점(첫 이벤트의 TS)보다 크거나 같으면 첫 관측이 통째로 세션 것이 된다.
func startedAt(ts int64) func(*Input) {
	return func(i *Input) { i.Event.StartTS = event.UnixSec(ts).Nano() }
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
//
// 파일 행의 모양도 같이 본다. 라인 배분은 Input.Target 이 실제로 도착해야만 일어나므로
// 이 단언은 "디코더 → 세션" 경로가 살아 있다는 것과 그 경로로 전체 경로가 새지 않는다는 것을
// 한 자리에서 지킨다 (ADR 0003).
func assertLineInvariant(t *testing.T, s Session) {
	t.Helper()
	var added, removed int64
	for _, f := range s.Files {
		if f.LinesAdded < 0 || f.LinesRemoved < 0 {
			t.Fatalf("파일 라인이 음수: %+v", f)
		}
		assertBasenameOnly(t, "session_files.file_name", f.Name)
		assertPathHash(t, "session_files.file_path_hash", f.PathHash)
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

// assertBasenameOnly 는 화면에 나가는 이름이 basename 인지 본다. 구분자가 하나라도 있으면
// 정규화를 거치지 않은 값이 들어왔다는 뜻이다.
func assertBasenameOnly(t *testing.T, label, name string) {
	t.Helper()
	if strings.ContainsAny(name, `/\`) {
		t.Fatalf("%s 에 경로 구분자가 있다: %q", label, name)
	}
}

func assertPathHash(t *testing.T, label, hash string) {
	t.Helper()
	if hash == "" {
		return
	}
	if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		t.Fatalf("%s 가 sha256 hex 가 아니다: %q", label, hash)
	}
}

// collectStrings 는 값 안의 모든 문자열을 재귀로 모은다. 비공개 필드도 Kind 만 보고 읽으므로
// event.Opt 의 내부까지 훑는다 — 프라이버시 단언이 "출력 어디에도" 를 문자 그대로 검사한다.
func collectStrings(v reflect.Value, out *[]string) {
	switch v.Kind() {
	case reflect.String:
		*out = append(*out, v.String())
	case reflect.Struct:
		for i := range v.NumField() {
			collectStrings(v.Field(i), out)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			collectStrings(v.Index(i), out)
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			collectStrings(v.Elem(), out)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			collectStrings(iter.Key(), out)
			collectStrings(iter.Value(), out)
		}
	}
}

func allStrings(v any) []string {
	var out []string
	collectStrings(reflect.ValueOf(v), &out)
	return out
}
