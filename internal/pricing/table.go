package pricing

import (
	"maps"
	"slices"
)

// 가격표 자료구조.
//
// # 왜 버전이 있는가
//
// 공시 단가는 바뀐다. 바뀐 뒤에 계산한 값과 그 전에 계산한 값이 섞이면, 화면의 비용이
// 왜 그 숫자인지 아무도 설명하지 못한다. 그래서 모든 결과가 **어느 판의 표로 계산했는지**를
// 달고 나간다(Applied). 표를 고치는 사람은 Version 을 올리고 EffectiveDate 를 바꾼다.
//
// # 왜 코드에 있는가
//
// 단가를 파일이나 서버에서 읽어오면 변경이 조용히 일어난다. 코드에 두면 단가 변경이
// 리뷰 가능한 diff 가 되고, 표를 고칠 때 테스트가 함께 깨져 근거를 남기게 된다.
// 그래서 이 패키지는 IO 를 하지 않는다 — 파일도, 네트워크도, 시계도 없다.

// Rate 는 모델 하나의 100만 토큰당 USD 단가다.
//
// **0 이하는 "공시 단가 없음"** 이다. 0 을 "무료" 로 쓰지 않는다 — 단가를 모르는 것과
// 값이 0 인 것을 같게 두면 표의 빈칸이 조용히 비용 0 으로 새어 나간다.
type Rate struct {
	InputPerMTokUSD      float64 `json:"input_per_mtok_usd"`
	OutputPerMTokUSD     float64 `json:"output_per_mtok_usd"`
	CacheReadPerMTokUSD  float64 `json:"cache_read_per_mtok_usd"`
	CacheWritePerMTokUSD float64 `json:"cache_write_per_mtok_usd"`
}

// Table 은 한 판의 가격표다. 값 타입이지만 내부 맵은 NewTable 에서 복사하므로,
// 만든 뒤에는 밖에서 바꿀 수 없다.
type Table struct {
	// Version 은 표의 판 번호다. 단가를 한 줄이라도 고치면 올린다.
	Version string `json:"version"`
	// EffectiveDate 는 이 단가를 공시 기준으로 확인한 날짜(YYYY-MM-DD)다.
	EffectiveDate string `json:"effective_date"`

	rates   map[string]Rate
	aliases map[string]string
}

// NewTable 은 표를 만든다. rates 의 키와 aliases 의 값은 **정규화된 이름**이어야 한다.
//
// aliases 는 한 단계만 따라간다. 별칭이 별칭을 가리키는 사슬은 만들지 않는다 —
// 사슬이 생기면 어떤 단가가 적용됐는지 표만 봐서는 알 수 없다.
func NewTable(version, effectiveDate string, rates map[string]Rate, aliases map[string]string) Table {
	t := Table{
		Version:       version,
		EffectiveDate: effectiveDate,
		rates:         make(map[string]Rate, len(rates)),
		aliases:       make(map[string]string, len(aliases)),
	}
	maps.Copy(t.rates, rates)
	maps.Copy(t.aliases, aliases)
	return t
}

// Lookup 은 모델 이름을 정규화해 단가를 찾는다.
//
// key 는 **실제로 적용한 표의 키**다. 별칭으로 들어온 이름이 어느 줄에 걸렸는지
// 결과에 남기기 위해 돌려준다. 못 찾으면 ok=false 이고, 이때 추측한 단가를 돌려주지 않는다.
func (t Table) Lookup(model string) (key string, rate Rate, ok bool) {
	c := Canonical(model)
	if c == "" {
		return "", Rate{}, false
	}
	if target, aliased := t.aliases[c]; aliased {
		c = target
	}
	r, found := t.rates[c]
	if !found {
		return "", Rate{}, false
	}
	return c, r, true
}

// Models 는 표에 단가가 있는 모델 키를 정렬해 돌려준다. 별칭은 포함하지 않는다.
func (t Table) Models() []string { return slices.Sorted(maps.Keys(t.rates)) }

// Aliases 는 별칭 → 표 키 대응의 복사본이다.
func (t Table) Aliases() map[string]string { return maps.Clone(t.aliases) }

// Applied 는 이 결과를 만든 가격표의 추적 정보다. 결과마다 붙어 나가므로, 화면에 뜬 비용이
// 어느 판의 어느 줄에서 나왔는지 되짚을 수 있다.
type Applied struct {
	TableVersion  string `json:"table_version"`
	EffectiveDate string `json:"effective_date"`
	// RateKey 는 이 결과에 적용한 표의 키다. 표에서 모델을 찾지 못했으면 빈 값이고,
	// 그때는 계산에 어떤 단가도 쓰이지 않았다는 뜻이다.
	RateKey string `json:"rate_key"`
}
