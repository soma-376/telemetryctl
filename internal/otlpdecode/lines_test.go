package otlpdecode

import (
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

func stringValue(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}

func TestToolInputLineCounts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    string
		add, del int64
		// Write 의 삭제 줄 수는 미관측이다 — 새 파일인지 덮어쓴 것인지 tool_input 으로는 모른다.
		delKnown bool
	}{
		{"Write 는 전량 추가", `{"file_path":"a.go","content":"one\ntwo\nthree\n"}`, 3, 0, false},
		{"빈 Write", `{"file_path":"a.go","content":""}`, 0, 0, false},
		{"추가만", `{"file_path":"a.go","old_string":"a\nb","new_string":"a\nx\nb"}`, 1, 0, true},
		{"삭제만", `{"file_path":"a.go","old_string":"a\nx\nb","new_string":"a\nb"}`, 0, 1, true},
		{"같은 줄 수 교체도 0 이 아니다", `{"file_path":"a.go","old_string":"a\nb","new_string":"c\nd"}`, 2, 2, true},
		{"변경 없음", `{"file_path":"a.go","old_string":"a\nb","new_string":"a\nb"}`, 0, 0, true},
		{"CRLF 도 한 줄로 센다", `{"file_path":"a.go","old_string":"a\r\nb","new_string":"a\r\nb\r\nc"}`, 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			add, del := toolInputLines(stringValue(tc.input))
			gotAdd, addSet := add.Get()
			gotDel, delSet := del.Get()
			if !addSet || delSet != tc.delKnown {
				t.Fatalf("관측 여부 = (%v, %v), 삭제 기대 = %v", addSet, delSet, tc.delKnown)
			}
			if gotAdd != tc.add || (tc.delKnown && gotDel != tc.del) {
				t.Fatalf("추가·삭제 = (%d, %d), 기대 = (%d, %d)", gotAdd, gotDel, tc.add, tc.del)
			}
		})
	}
}

// 셀 수 없는 편집은 0 이 아니라 미관측이어야 한다. 0 은 "바뀐 게 없다" 는 뜻이라
// dashboard 의 합계를 조용히 낮춘다.
func TestToolInputLinesLeavesUncountableUnobserved(t *testing.T) {
	big := strings.Repeat("line\n", 2_100)
	for _, tc := range []struct{ name, input string }{
		{"replace_all 은 치환 횟수를 알 수 없다", `{"file_path":"a.go","old_string":"a","new_string":"b","replace_all":true}`},
		{"old_string 만 있는 형태", `{"file_path":"a.go","old_string":"a"}`},
		{"파일 경로만 (Read 계열)", `{"file_path":"a.go"}`},
		{"명령만 실은 tool_input (Bash)", `{"command":"go test ./..."}`},
		{"JSON 이 아닌 값", `not json at all`},
		{"예산을 넘는 편집", `{"file_path":"a.go","old_string":"` + strings.ReplaceAll(big, "\n", `\n`) + `","new_string":"` + strings.ReplaceAll(big+"x", "\n", `\n`) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			add, del := toolInputLines(stringValue(tc.input))
			if add.Valid() || del.Valid() {
				t.Fatalf("미관측이어야 하는데 값이 있다: add=%v del=%v", add, del)
			}
		})
	}
}

// kvlist 로 오는 tool_input 은 경로만 쓴다 — 줄 수의 출처인 원문 형식이 벤더마다 달라
// 추측하지 않는다.
func TestToolInputLinesIgnoresNonString(t *testing.T) {
	v := &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{
		KvlistValue: &commonpb.KeyValueList{Values: []*commonpb.KeyValue{
			{Key: "file_path", Value: stringValue("a.go")},
			{Key: "content", Value: stringValue("one\ntwo\n")},
		}},
	}}
	add, del := toolInputLines(v)
	if add.Valid() || del.Valid() {
		t.Fatalf("kvlist 에서 줄 수를 만들었다: add=%v del=%v", add, del)
	}
}
