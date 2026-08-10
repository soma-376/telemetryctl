package event

// Opt 는 "미설정" 과 "0" 이 다른 스칼라를 표현한다. SQLite 스키마에서 NULL 을 허용하는
// 수치 컬럼이 Go 쪽에서도 미설정을 표현할 수 있어야 하므로 그 컬럼들에 일관되게 쓴다.
//
// 포인터(*int64) 대신 값 타입을 고른 이유는 세 가지다.
//   - nil 역참조가 구조적으로 없다. 이벤트는 다섯 패키지를 지나가고 그중 넷은 값만 읽는다.
//   - T 가 comparable 이면 Opt[T] 도 comparable 이라 == 와 테이블 주도 테스트가 그대로 동작한다.
//   - 복사본이 원본과 저장소를 공유하지 않는다. 조립기가 이벤트를 복사해 들고 있어도
//     나중에 도착한 이벤트가 앞선 값을 바꾸지 못한다.
//
// 필드를 비공개로 둬 제로값(미설정)과 Some 이외의 생성 경로를 막는다.
type Opt[T comparable] struct {
	v     T
	valid bool
}

// Some 은 값이 설정된 Opt 를 만든다. 미설정은 제로값 Opt[T]{} 다.
func Some[T comparable](v T) Opt[T] { return Opt[T]{v: v, valid: true} }

// Get 은 값과 설정 여부를 함께 돌려준다. store 는 ok=false 를 NULL 로 쓴다.
func (o Opt[T]) Get() (T, bool) { return o.v, o.valid }

func (o Opt[T]) Valid() bool { return o.valid }

// Or 는 미설정일 때 fallback 을 돌려준다. 집계처럼 0 을 항등원으로 써도 되는 곳에서만 쓴다.
func (o Opt[T]) Or(fallback T) T {
	if !o.valid {
		return fallback
	}
	return o.v
}
