package event

import "time"

// 시간 단위가 테이블마다 다르다. 계획서 스키마가 events.ts 를 unix **나노초**,
// sessions.started_at·last_event_at·ended_at 을 unix **초** 로 정의했다.
// 섞으면 세션이 1970 년에 시작하거나 400 일 보존이 즉시 만료되는 식으로 조용히 틀리므로
// 두 단위를 별도 타입으로 나눠 컴파일러가 대입을 막게 한다. 전부 UTC 기준이다.
//
// 세션 계열 테이블(sessions, session_files.last_ts, tool_events.ts)은 UnixSec 로 통일한다.
type (
	// UnixNano 는 events.ts 의 단위다 — UTC unix 나노초.
	UnixNano int64
	// UnixSec 는 sessions 계열 컬럼의 단위다 — UTC unix 초.
	UnixSec int64
	// Hour 는 events.hour·rollup_hourly.hour 의 값이다 — 시간 버킷이 시작하는 UTC unix 초.
	Hour int64
)

// NanoFromTime 은 time.Time 을 events.ts 단위로 옮긴다.
func NanoFromTime(t time.Time) UnixNano { return UnixNano(t.UnixNano()) }

// SecFromTime 은 time.Time 을 sessions 계열 단위로 옮긴다.
func SecFromTime(t time.Time) UnixSec { return UnixSec(t.Unix()) }

func (n UnixNano) Time() time.Time { return time.Unix(0, int64(n)).UTC() }
func (s UnixSec) Time() time.Time  { return time.Unix(int64(s), 0).UTC() }
func (h Hour) Time() time.Time     { return time.Unix(int64(h), 0).UTC() }

// Sec 은 나노초를 초로 내린다. 1970 이전(음수)에서도 버림이 아니라 내림이라
// 초 경계 왼쪽의 값이 오른쪽 구간으로 새지 않는다.
func (n UnixNano) Sec() UnixSec { return UnixSec(floorDiv(int64(n), int64(time.Second))) }

func (s UnixSec) Nano() UnixNano { return UnixNano(int64(s) * int64(time.Second)) }

// Sec 은 Hour 를 그대로 초로 본다 — 버킷 값 자체가 UTC unix 초다.
func (h Hour) Sec() UnixSec { return UnixSec(h) }

// HourOf 는 events.ts 를 시간 버킷으로 내린다. 정각은 자기 버킷에 들어가고
// 정각 직전 1ns 는 앞 버킷에 남는다.
func HourOf(n UnixNano) Hour {
	return Hour(floorDiv(int64(n), int64(time.Hour)) * int64(time.Hour/time.Second))
}

// floorDiv 는 음수에서도 내림으로 나눈다. Go 의 / 는 0 쪽으로 버려서 -1ns 가 0 시 버킷에
// 들어가 버린다 — 1970 이전 타임스탬프는 벤더가 시계를 못 읽었을 때 실제로 들어온다.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
