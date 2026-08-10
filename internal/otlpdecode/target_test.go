package otlpdecode

import (
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/your-org/pulsemetry/internal/event"
)

func kvlist(pairs ...string) *commonpb.AnyValue {
	kvs := make([]*commonpb.KeyValue, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		kvs = append(kvs, &commonpb.KeyValue{Key: pairs[i], Value: anyStr(pairs[i+1])})
	}
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{
		KvlistValue: &commonpb.KeyValueList{Values: kvs},
	}}
}

// tool_input 의 형태가 벤더마다 다르다. 형태 하나를 놓치면 그 벤더의 세션에서
// 「파일 변경」 화면이 통째로 빈다.
func TestToolInputTargetExtraction(t *testing.T) {
	const path = "/Users/jy/dev/projects/soma-376/telemetryctl/internal/session/state.go"

	tests := []struct {
		name     string
		value    *commonpb.AnyValue
		wantName string
		wantExt  string
	}{
		{
			name:     "Edit 의 JSON 문자열",
			value:    anyStr(`{"file_path":"` + path + `","old_string":"a","new_string":"b"}`),
			wantName: "state.go", wantExt: "go",
		},
		{
			name:     "kvlist 로 온 file_path",
			value:    kvlist("file_path", path, "old_string", "a"),
			wantName: "state.go", wantExt: "go",
		},
		{
			name:     "camelCase 표기",
			value:    anyStr(`{"filePath":"` + path + `"}`),
			wantName: "state.go", wantExt: "go",
		},
		{
			name:     "NotebookEdit 의 notebook_path",
			value:    anyStr(`{"notebook_path":"/Users/jy/nb/analysis.ipynb","cell_id":"c1"}`),
			wantName: "analysis.ipynb", wantExt: "ipynb",
		},
		{
			name:  "명령만 있는 tool_input 은 대상이 없다",
			value: anyStr(`{"command":"go test -race ./internal/otlpdecode/"}`),
		},
		{
			name:  "JSON 이 아니면 포기한다",
			value: anyStr("Edit(file_path=/Users/jy/a.go)"),
		},
		{
			name:  "JSON 객체가 아니면 포기한다",
			value: anyStr(`["/Users/jy/a.go"]`),
		},
		{
			name:  "경로 키의 값이 문자열이 아니면 포기한다",
			value: anyStr(`{"file_path":{"nested":"/Users/jy/a.go"}}`),
		},
		{
			name:  "빈 경로는 대상이 아니다",
			value: anyStr(`{"file_path":""}`),
		},
		{
			name:  "kvlist 에 경로 키가 없으면 대상이 없다",
			value: kvlist("command", "go build ./..."),
		},
		{
			name:  "받을 수 없는 타입",
			value: anyInt64(42),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolInputTarget(tt.value)
			if tt.wantName == "" {
				if got != (event.Path{}) {
					t.Fatalf("대상이 없어야 하는데 %+v 가 나왔다", got)
				}
				return
			}
			if got.Name != tt.wantName || got.Ext != tt.wantExt {
				t.Fatalf("Path = %+v, want name=%q ext=%q", got, tt.wantName, tt.wantExt)
			}
			if len(got.Hash) != 64 {
				t.Fatalf("해시 길이 = %d, want 64", len(got.Hash))
			}
			// 전체 경로는 어느 필드에도 없다 — session 이 요구한 계약이다.
			for _, s := range []string{got.Hash, got.Name, got.Ext} {
				if strings.Contains(s, "/") || strings.Contains(s, "Users") {
					t.Fatalf("정규화 결과에 경로가 남았다: %q", s)
				}
			}
		})
	}
}

// 디코더가 파일 경로를 세션까지 실어 보내는 새 경로다. 이것이 없으면 session_files 가
// 영원히 비고 「파일 변경」 화면이 통째로 빈다.
func TestTargetsReachSessionAsNormalizedPath(t *testing.T) {
	res := decodeBoth(t, PayloadLogs, "logs_session_walkthrough.json", testOptions())

	if len(res.Targets) != 1 {
		t.Fatalf("Targets = %d건, want 1 (Edit 의 tool_input 하나)", len(res.Targets))
	}
	got := res.Targets[0]

	// 연결 고리가 실제 이벤트를 가리킨다.
	if got.EventIndex < 0 || got.EventIndex >= len(res.Events) {
		t.Fatalf("EventIndex = %d 가 범위를 벗어남", got.EventIndex)
	}
	ev := res.Events[got.EventIndex]
	if ev.EventID != "evt_tool_0002" {
		t.Fatalf("대상이 붙은 이벤트 = %q, want evt_tool_0002", ev.EventID)
	}
	if got.DedupKey != ev.DedupKey() {
		t.Fatalf("DedupKey 가 이벤트와 어긋남: %q vs %q", got.DedupKey, ev.DedupKey())
	}

	want := event.NormalizePath(fixtureFilePath)
	if got.Path != want {
		t.Fatalf("Path = %+v, want %+v", got.Path, want)
	}
	if got.Path.Name != "decode.go" || got.Path.Ext != "go" {
		t.Fatalf("basename·확장자가 어긋남: %+v", got.Path)
	}
}

// 명령만 있는 tool_input(Bash)과 tool_input 이 아예 없는 이벤트는 대상이 없다.
// 여기서 대상을 만들면 tool_events.target_name 에 파일이 아닌 값이 섞인다.
func TestEventsWithoutFileTargetProduceNone(t *testing.T) {
	res := decodeBoth(t, PayloadLogs, "logs_session_walkthrough.json", testOptions())

	withTarget := map[int]bool{}
	for _, tg := range res.Targets {
		withTarget[tg.EventIndex] = true
	}
	for i, e := range res.Events {
		if e.EventID == "evt_tool_0002" {
			continue
		}
		if withTarget[i] {
			t.Errorf("%s 에 대상이 붙었다 — 파일을 건드리지 않은 이벤트다", e.EventID)
		}
	}
}
