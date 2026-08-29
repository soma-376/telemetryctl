package store

import "github.com/your-org/pulsemetry/internal/event"

// SQL 인자로 넘기기 전에 "값 없음" 을 NULL 로 옮기는 변환들이다.
//
// v3 의 수치 컬럼은 거의 전부 NULL 을 허용한다. 0 으로 눕히면 "비용 0" 과 "비용 개념 없음",
// "실패" 와 "성공 여부 미상" 이 같아진다 — 그 구분을 잃으면 조회 시점 GROUP BY 의 분모가
// 조용히 부푼다. 문서가 "미관측은 NULL" 이라고 못 박은 컬럼이 여럿이다.

// nullStr 은 빈 문자열을 NULL 로 옮긴다. 문자열 컬럼에서는 ""와 NULL 을 구분할 이유가 없고,
// NULL 로 통일해야 조회 쪽에서 IS NULL 하나만 보면 된다.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// optInt·optFloat·optBool 은 event.Opt 의 미설정을 NULL 로 옮긴다.
func optInt(o event.Opt[int64]) any {
	if v, ok := o.Get(); ok {
		return v
	}
	return nil
}

func optFloat(o event.Opt[float64]) any {
	if v, ok := o.Get(); ok {
		return v
	}
	return nil
}

func optBool(o event.Opt[bool]) any {
	if v, ok := o.Get(); ok {
		return boolInt(v)
	}
	return nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// nullSec 은 0 이하의 시각을 NULL 로 옮긴다. v3 의 시각 컬럼은 전부 선택이고 단위는 초다.
// 0 을 그대로 넣으면 1970 년 행이 되어 보존 정책이 즉시 지운다.
func nullSec(s event.UnixSec) any {
	if s <= 0 {
		return nil
	}
	return int64(s)
}

// optSec 은 event.Opt[event.UnixSec] 의 미설정을 NULL 로 옮긴다.
func optSec(o event.Opt[event.UnixSec]) any {
	if v, ok := o.Get(); ok {
		return nullSec(v)
	}
	return nil
}
