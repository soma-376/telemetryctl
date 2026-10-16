package event

import "testing"

// event_content.kind 컬럼에 들어갈 문자열은 계획서 스키마가 못 박은 값이다
// (prompt | response | tool_input | tool_result). 상수 값을 바꾸면 이미 저장된 행과
// 어긋나 화면의 원문 검색이 조용히 빈다. 그 계약을 테스트로 고정한다.
func TestContentKindValuesMatchSchema(t *testing.T) {
	want := map[ContentKind]string{
		ContentPrompt:     "prompt",
		ContentResponse:   "response",
		ContentToolInput:  "tool_input",
		ContentToolResult: "tool_result",
	}
	for kind, s := range want {
		if string(kind) != s {
			t.Errorf("ContentKind = %q, want %q", string(kind), s)
		}
	}
	if len(want) != 4 {
		t.Fatalf("kind 어휘가 %d 개 — 스키마는 4 개다", len(want))
	}
}

// 제로값이 "원문 없음"이어야 한다. session.Input 은 원문이 없는 이벤트에 제로값을 담아
// 넘기고 조립기는 Kind 로만 분기한다 — 제로값이 유효한 kind 였다면 원문 없는 이벤트가
// 전부 prompt 로 취급된다.
func TestZeroContentIsAbsent(t *testing.T) {
	var c Content
	if c.Kind != "" {
		t.Fatalf("제로값 Content 의 Kind = %q, want 빈 값", c.Kind)
	}
	for _, kind := range []ContentKind{ContentPrompt, ContentResponse, ContentToolInput, ContentToolResult} {
		if kind == "" {
			t.Fatalf("%q 가 제로값과 같다", kind)
		}
	}
	if c.Body != "" || c.Truncated {
		t.Fatalf("제로값 Content 가 비어 있지 않다: %+v", c)
	}
}
