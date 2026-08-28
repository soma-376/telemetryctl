package pricing

import (
	"slices"
	"testing"
)

func TestTableLookup(t *testing.T) {
	table := Default()

	tests := []struct {
		name    string
		model   string
		wantKey string
		wantOK  bool
	}{
		{name: "표에 그대로 있는 이름", model: "claude-opus-5", wantKey: "claude-opus-5", wantOK: true},
		{name: "날짜 붙은 아이디는 같은 줄로", model: "claude-haiku-4-5-20251001", wantKey: "claude-haiku-4-5", wantOK: true},
		{
			name:    "Bedrock 표기도 같은 줄로",
			model:   "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
			wantKey: "claude-sonnet-4-5", wantOK: true,
		},
		{name: "별칭은 한 단계 따라간다", model: "claude-sonnet-4-0", wantKey: "claude-sonnet-4", wantOK: true},
		{name: "Codex 모델", model: "gpt-5-codex", wantKey: "gpt-5-codex", wantOK: true},
		{name: "모르는 모델은 못 찾는다", model: "claude-opus-9", wantOK: false},
		{name: "합성 모델 표기도 못 찾는다", model: "<synthetic>", wantOK: false},
		{name: "빈 이름은 못 찾는다", model: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, rate, ok := table.Lookup(tt.model)
			if ok != tt.wantOK {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.model, ok, tt.wantOK)
			}
			if !ok {
				// 못 찾았을 때 추측한 단가가 새어 나오지 않아야 한다.
				if rate != (Rate{}) {
					t.Fatalf("찾지 못했는데 단가가 들어 있다: %+v", rate)
				}
				if key != "" {
					t.Fatalf("찾지 못했는데 키가 들어 있다: %q", key)
				}
				return
			}
			if key != tt.wantKey {
				t.Fatalf("Lookup(%q) key = %q, want %q", tt.model, key, tt.wantKey)
			}
			if rate.InputPerMTokUSD <= 0 {
				t.Fatalf("찾은 줄에 입력 단가가 없다: %+v", rate)
			}
		})
	}
}

// 표는 만든 뒤에 밖에서 바뀌면 안 된다. 바뀌면 같은 판 번호가 다른 단가를 가리킨다.
func TestNewTableCopiesInput(t *testing.T) {
	rates := map[string]Rate{"m": {InputPerMTokUSD: 1, OutputPerMTokUSD: 2}}
	aliases := map[string]string{"m-latest-x": "m"}
	table := NewTable("v9", "2026-01-01", rates, aliases)

	rates["m"] = Rate{InputPerMTokUSD: 999}
	delete(aliases, "m-latest-x")

	_, rate, ok := table.Lookup("m")
	if !ok || rate.InputPerMTokUSD != 1 {
		t.Fatalf("입력 맵 변경이 표를 바꿨다: %+v ok=%v", rate, ok)
	}
	if got := table.Aliases(); len(got) != 1 {
		t.Fatalf("별칭 맵이 밖에서 지워졌다: %v", got)
	}
	table.Aliases()["m-latest-x"] = "다른-것"
	if key, _, _ := table.Lookup("m-latest-x"); key != "m" {
		t.Fatalf("Aliases() 결과 변경이 표를 바꿨다: key=%q", key)
	}
}

func TestTableModelsIsSorted(t *testing.T) {
	models := Default().Models()
	if len(models) == 0 {
		t.Fatal("기본 표가 비어 있다")
	}
	if !slices.IsSorted(models) {
		t.Fatalf("Models() 가 정렬되지 않았다: %v", models)
	}
	if slices.Contains(models, "claude-sonnet-4-0") {
		t.Fatal("Models() 에 별칭이 섞였다")
	}
}
