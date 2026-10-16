package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// LineCount 는 "미관측" 과 "0" 이 서로 다른 줄 수 하나다.
//
// # 왜 int64 가 아닌가
//
// v3 의 `file_changes.additions` · `deletions` 는 선택 컬럼이고 스키마 문서가 **미관측은
// NULL** 이라고 못 박았다 (`docs/sqlite-schema/file-changes.md`). 실제로 세 상황이 다르다.
//
//   - 값이 있고 0 이다 — 편집은 했는데 추가된 줄이 없다 (삭제만 한 편집).
//   - 값이 NULL 이다 — 줄 수를 **한 번도 관측하지 못했다.** `session.FileChangeOf` 가
//     줄 수를 채우지 않는 것이 지금의 기본 경로다. lines_of_code 메트릭에 파일명이 없어
//     파일별 값을 알 수 없기 때문이다.
//   - 파일 변경 행 자체가 없다 — 그 파일을 건드리지 않았다.
//
// NULL 을 0 으로 눕히면 두 번째가 첫 번째와 같아진다. 그러면 화면이 "이 편집은 0줄을
// 바꿨다" 고 **단정**하게 되는데, 실제로 아는 것은 "모른다" 뿐이다. 합계에서는 그 차이가
// 더 커진다 — 미관측 100건이 0줄로 눕으면 총합이 조용히 과소 보고된다.
//
// # 왜 포인터가 아닌가
//
// `internal/event` 의 `Opt[T]` 와 같은 이유다. 필드를 비공개로 두면 제로값(미관측)과
// [ObservedLines] 이외의 생성 경로가 없고, 값을 꺼내려면 반드시 [LineCount.Get] 을 거쳐
// 관측 여부를 함께 받게 된다. 호출자가 실수로 0 과 미관측을 섞을 자리가 타입에 없다.
// nil 역참조도 구조적으로 없고, 비교 가능한 값 타입이라 테이블 주도 테스트에서 `==` 가
// 그대로 동작한다.
//
// # JSON 계약
//
// 관측된 값은 정수로, 미관측은 **`null`** 로 나간다. 이 패키지가 이미 쓰는 규약과 같다 —
// `SessionRow.EndedAt` · `ToolRow.DurationMS` · `ToolRow.Success` 가 전부 "모르는 값은
// null" 이다 (dashboard.go 의 `nullInt64` 주석). TypeScript 쪽 타입은 `number | null` 이고,
// 화면은 null 을 0 이 아니라 "—" 로 그린다.
type LineCount struct {
	n        int64
	observed bool
}

// ObservedLines 는 실제로 관측한 줄 수를 담는다. 미관측은 제로값 `LineCount{}` 다.
func ObservedLines(n int64) LineCount { return LineCount{n: n, observed: true} }

// Get 은 값과 관측 여부를 함께 돌려준다. ok=false 면 n 에는 의미가 없다.
func (c LineCount) Get() (int64, bool) { return c.n, c.observed }

// Observed 는 이 줄 수를 한 번이라도 관측했는지 알려준다.
func (c LineCount) Observed() bool { return c.observed }

// Or 는 미관측일 때 fallback 을 돌려준다.
//
// **정렬 키처럼 0 을 항등원으로 써도 되는 곳에서만 쓴다.** 화면에 숫자를 찍는 자리에서
// `Or(0)` 을 부르면 이 타입이 막으려던 혼동이 그대로 돌아온다.
func (c LineCount) Or(fallback int64) int64 {
	if !c.observed {
		return fallback
	}
	return c.n
}

// plus 는 관측된 값 하나를 더한다. 미관측 합계에 관측값이 더해지면 그 결과는 관측된 값이다 —
// "일부만 관측한 합계" 는 관측된 몫만큼은 알고 있는 값이기 때문이다. 몇 건을 못 봤는지는
// 합계와 별개로 세어 함께 보고한다 (FileChangeSummary.UnobservedAdditions).
func (c LineCount) plus(n int64) LineCount { return ObservedLines(c.n + n) }

// MarshalJSON 은 미관측을 null 로, 관측값을 정수로 낸다.
func (c LineCount) MarshalJSON() ([]byte, error) {
	if !c.observed {
		return []byte("null"), nil
	}
	return strconv.AppendInt(nil, c.n, 10), nil
}

// UnmarshalJSON 은 null 을 미관측으로 되돌린다. 왕복이 성립해야 GUI 가 돌려보낸 값을
// 다시 읽을 수 있고, 무엇보다 이 타입의 계약을 테스트가 양방향으로 단언할 수 있다.
func (c *LineCount) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		*c = LineCount{}
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		// 원문을 그대로 싣지 않는다. 값이 길면 그 자체가 화면을 덮는다.
		return fmt.Errorf("dashboard: 줄 수는 정수이거나 null 이어야 함")
	}
	*c = ObservedLines(n)
	return nil
}
