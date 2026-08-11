package otlpdecode

import (
	"encoding/json"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/your-org/pulsemetry/internal/event"
)

// Target 은 이 이벤트가 건드린 파일이다 — tool_events.target_hash·target_name 과
// session_files 한 행의 원천이다.
//
// # 왜 Event 에 없는가
//
// events 테이블에는 파일 컬럼이 없다. 계획서 스키마에서 파일이 등장하는 곳은
// session_files(file_path_hash·file_name)와 tool_events(target_hash·target_name) 둘뿐이고
// 둘 다 세션에 매달린 테이블이다. events 에 없는 컬럼을 event.Event 에 만들면 store 가
// 쓸 자리가 없는 필드를 들고 다니게 되므로, 원문(Content)과 같은 방식으로 이벤트 옆에
// 따로 실어 보낸다.
//
// # 왜 event.Path 인가
//
// session 패키지는 "이미 정규화된 값만 받는다" 를 계약으로 갖는다. 여기서 event.NormalizePath
// 를 거쳐 해시+basename+확장자로 줄이므로 전체 경로 문자열은 이 구조체를 통해 세션·저장소로
// 흘러갈 수 없다 (ADR 0003). 전체 경로는 tool_input **원문**(event_content.body)에만 남는다 —
// 그쪽은 16KB 캡·30일 보존·상위 미전달 조건으로 허용된 자리다.
type Target struct {
	EventIndex int
	DedupKey   string
	Path       event.Path
}

// filePathKeys 는 tool_input 안에서 대상 파일 경로를 담고 오는 키다.
//
// 계획서가 지목한 것은 Edit/Write 계열의 file_path 하나다. 벤더가 kvlist 로 보낼 때의
// camelCase 표기와 NotebookEdit 의 notebook_path 까지만 더한다 — 여기를 넓히면
// (예: Glob 의 path) 편집하지 않은 디렉터리가 tool_events.target_name 에 뜬다.
var filePathKeys = []string{"file_path", "filePath", "notebook_path", "notebookPath"}

// toolInputTarget 은 tool_input 속성값에서 대상 파일 경로를 뽑아 정규화한다.
//
// 두 가지 형태를 받는다. Claude Code 는 tool_input 을 **JSON 문자열**로 보내고
// ({"file_path":"...","old_string":"..."}), 다른 벤더는 kvlist AnyValue 로 보낼 수 있다.
// 문자열 쪽은 최상위 객체만 얕게 훑는다 — 중첩 구조를 재귀로 뒤지면 tool_result 본문에
// 우연히 들어 있는 경로까지 대상으로 잡힌다.
//
// 경로를 못 찾으면 제로값 Path 다. 명령만 있는 tool_input({"command":"go test ..."})이
// 그 경우다 — tool_events.target_name 의 "명령 첫 토큰" 은 아직 채우지 않는다.
func toolInputTarget(v *commonpb.AnyValue) event.Path {
	raw := rawToolInputPath(v)
	if raw == "" {
		return event.Path{}
	}
	return event.NormalizePath(raw)
}

func rawToolInputPath(v *commonpb.AnyValue) string {
	if kv, ok := v.GetValue().(*commonpb.AnyValue_KvlistValue); ok {
		return pathFromKVList(kv.KvlistValue.GetValues())
	}
	s, ok := v.GetValue().(*commonpb.AnyValue_StringValue)
	if !ok {
		return ""
	}
	return pathFromJSONObject(s.StringValue)
}

func pathFromKVList(kvs []*commonpb.KeyValue) string {
	for _, want := range filePathKeys {
		for _, kv := range kvs {
			if kv.GetKey() != want {
				continue
			}
			if s, ok := kv.GetValue().GetValue().(*commonpb.AnyValue_StringValue); ok && s.StringValue != "" {
				return s.StringValue
			}
		}
	}
	return ""
}

// pathFromJSONObject 는 JSON 객체 문자열의 최상위에서 경로 키를 찾는다.
// JSON 이 아니거나 객체가 아니면 조용히 포기한다 — tool_input 형식은 벤더 자유이고,
// 파싱 실패는 "파일 정보가 없다" 일 뿐 이벤트를 버릴 이유가 아니다.
func pathFromJSONObject(s string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return ""
	}
	for _, want := range filePathKeys {
		raw, ok := obj[want]
		if !ok {
			continue
		}
		var p string
		if err := json.Unmarshal(raw, &p); err == nil && p != "" {
			return p
		}
	}
	return ""
}
