package pricing

// 캐시 절감액.
//
// # 절감액은 비용이 아니다
//
// Savings 는 "캐시를 쓰지 않았다면 얼마였을까" 와 "실제로 얼마였나" 의 차이다.
// 어떤 합계에도 더하지 않는다 — Cost 와 더하면 비용이 절감액만큼 부풀거나 깎인다.
// 그래서 Cost 와 다른 타입에 담고, 이름도 금액이 아니라 절감액이다.
//
// # 왜 보고 비용이 있어도 계산하는가
//
// 절감액은 반사실(counterfactual)이라 표의 단가에서만 나온다. 벤더가 cost_usd 를
// 보고했든 아니든 "같은 토큰을 입력 단가로 냈다면" 은 표로만 답할 수 있다.
// 그래서 Cost.Source 와 무관하게 표가 있으면 계산한다. 반대로 표에 없는 모델은
// 보고 비용이 있어도 절감액을 알 수 없다.
//
// # read 와 write 를 왜 나누는가
//
// 둘은 방향이 다르다. 캐시 읽기는 입력 단가보다 싸서 절감이지만, 캐시 쓰기는 벤더에 따라
// 입력 단가보다 **비싸다**(Anthropic 은 1.25배). 쓰기 절감액이 음수인 것은 오류가 아니라
// 다음 호출의 읽기 절감을 사려고 지금 더 낸 선투자다. 합쳐 버리면 "캐시를 더 쓰면 되는지"
// 를 화면에서 판단할 수 없다.

// Savings 는 캐시로 아낀(또는 더 쓴) 금액이다. **비용이 아니다** — 위 머리말 참조.
type Savings struct {
	// Available 이 false 면 Read·Write·Total 은 모두 0 이고 화면은 "-" 로 그려야 한다.
	Available bool   `json:"available"`
	Reason    Reason `json:"reason"`

	// Read 는 캐시 읽기 토큰을 입력 단가로 냈을 때와의 차이다. 양수가 절감이다.
	Read Money `json:"read"`
	// Write 는 캐시 쓰기의 차이다. 쓰기 단가가 입력보다 비싼 벤더에서는 음수다.
	Write Money `json:"write"`
	// Total 은 Read + Write 다.
	Total Money `json:"total"`
}

// cacheSavings 는 캐시 read·write 절감액을 각각 계산한다.
func cacheSavings(u Usage, rate Rate, known bool, canonical string) Savings {
	if !known {
		return Savings{Reason: unknownReason(canonical)}
	}
	read := tokens(u.CacheReadTokens)
	write := tokens(u.CacheWriteTokens)

	// 기준이 되는 입력 단가가 없으면 "안 썼다면 얼마였을까" 를 물을 수 없다.
	if (read > 0 || write > 0) && rate.InputPerMTokUSD <= 0 {
		return Savings{Reason: ReasonMissingRate}
	}
	if rateMissing(read, rate.CacheReadPerMTokUSD) || rateMissing(write, rate.CacheWritePerMTokUSD) {
		return Savings{Reason: ReasonMissingRate}
	}

	// 캐시를 쓰지 않은 호출의 절감액은 0 이다 — unavailable 이 아니다.
	readSaved := tokenCost(read, rate.InputPerMTokUSD) - tokenCost(read, rate.CacheReadPerMTokUSD)
	writeSaved := tokenCost(write, rate.InputPerMTokUSD) - tokenCost(write, rate.CacheWritePerMTokUSD)

	return Savings{
		Available: true,
		Read:      money(readSaved),
		Write:     money(writeSaved),
		Total:     money(readSaved + writeSaved),
	}
}
