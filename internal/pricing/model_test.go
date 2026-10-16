package pricing

import "testing"

// alias 정규화는 "같은 모델의 다른 표기" 만 모은다. 표에 없는 이름은 그대로 남아야
// 조회가 실패하고 unavailable 로 떨어진다 — 여기서 억지로 아는 이름에 붙이면
// 화면에 틀린 비용이 뜬다.
func TestCanonical(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "별칭은 그대로", raw: "claude-opus-5", want: "claude-opus-5"},
		{name: "대문자와 공백을 눕힌다", raw: "  Claude-Opus-5 ", want: "claude-opus-5"},
		{name: "날짜 스냅샷을 뗀다", raw: "claude-haiku-4-5-20251001", want: "claude-haiku-4-5"},
		{name: "대시로 구분된 날짜도 뗀다", raw: "gpt-4.1-2025-04-14", want: "gpt-4.1"},
		{name: "-latest 를 뗀다", raw: "claude-3-5-sonnet-latest", want: "claude-3-5-sonnet"},
		{
			name: "Bedrock 의 리전·벤더 접두와 버전 꼬리를 뗀다",
			raw:  "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
			want: "claude-sonnet-4-5",
		},
		{name: "Bedrock global 접두", raw: "global.anthropic.claude-opus-5", want: "claude-opus-5"},
		{name: "Vertex 의 @스냅샷을 뗀다", raw: "claude-opus-4-5@20251101", want: "claude-opus-4-5"},
		{name: "경로형 이름은 마지막 조각만", raw: "publishers/anthropic/models/claude-opus-5", want: "claude-opus-5"},
		{name: "라우터 접두 openai/", raw: "openai/gpt-5-codex", want: "gpt-5-codex"},
		{name: "이름 안의 점은 접두가 아니다", raw: "gpt-4.1-mini", want: "gpt-4.1-mini"},
		{name: "세대 숫자는 날짜가 아니다", raw: "claude-opus-4-5", want: "claude-opus-4-5"},
		{name: "빈 이름은 빈 값", raw: "", want: ""},
		{name: "공백뿐인 이름도 빈 값", raw: "   ", want: ""},
		{name: "모르는 이름은 손대지 않는다", raw: "some-unreleased-model", want: "some-unreleased-model"},
		{name: "대시가 없는 이름", raw: "o3", want: "o3"},
		{name: "일부만 날짜처럼 생긴 꼬리는 남긴다", raw: "gpt-4.1-04-14", want: "gpt-4.1-04-14"},
		{name: "날짜 자릿수가 안 맞으면 남긴다", raw: "model-2025-04-1", want: "model-2025-04-1"},
		{name: "8자리가 아닌 숫자 꼬리는 남긴다", raw: "model-2025010", want: "model-2025010"},
		{name: "Claude Code 의 합성 모델 표기도 그대로 남는다", raw: "<synthetic>", want: "<synthetic>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Canonical(tt.raw); got != tt.want {
				t.Fatalf("Canonical(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
