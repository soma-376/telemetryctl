package event

import (
	"testing"
	"time"
)

const (
	nsPerSec  = int64(time.Second)
	nsPerHour = int64(time.Hour)
)

func TestHourOf(t *testing.T) {
	tests := []struct {
		name string
		ts   UnixNano
		want Hour
	}{
		{"에폭", 0, 0},
		{"에폭 직후 1ns", 1, 0},
		{"정각 직전 1ns", UnixNano(nsPerHour - 1), 0},
		{"정각", UnixNano(nsPerHour), 3600},
		{"정각 직후 1ns", UnixNano(nsPerHour + 1), 3600},
		{"버킷 중간", UnixNano(nsPerHour + 1800*nsPerSec), 3600},
		{"다음 정각 직전 1ns", UnixNano(2*nsPerHour - 1), 3600},
		{"에폭 직전 1ns", -1, -3600},
		{"에폭 1시간 전 정각", UnixNano(-nsPerHour), -3600},
		{"에폭 1시간 전 정각 직전 1ns", UnixNano(-nsPerHour - 1), -7200},
		{"실제 타임스탬프", NanoFromTime(time.Date(2026, 8, 10, 17, 34, 21, 500, time.UTC)),
			Hour(time.Date(2026, 8, 10, 17, 0, 0, 0, time.UTC).Unix())},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HourOf(tt.ts); got != tt.want {
				t.Errorf("HourOf(%d) = %d (%s), want %d (%s)",
					int64(tt.ts), int64(got), got.Time(), int64(tt.want), tt.want.Time())
			}
		})
	}
}

// 버킷은 UTC 기준이다. 로컬 시간대가 무엇이든 같은 순간은 같은 버킷에 들어가야 한다.
func TestHourOfIsUTC(t *testing.T) {
	seoul := time.FixedZone("KST", 9*60*60)
	instant := time.Date(2026, 8, 10, 17, 30, 0, 0, seoul) // = 08:30 UTC
	got := HourOf(NanoFromTime(instant))
	want := Hour(time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC).Unix())
	if got != want {
		t.Fatalf("HourOf = %d (%s), want %d (%s)", int64(got), got.Time(), int64(want), want.Time())
	}
	if h := got.Time().Hour(); h != 8 {
		t.Fatalf("버킷 시각이 UTC 가 아님: %d 시", h)
	}
}

// 한 버킷 안의 모든 순간은 같은 값으로, 경계를 넘으면 반드시 다른 값으로 떨어져야 한다.
func TestHourOfBucketIsHalfOpen(t *testing.T) {
	start := UnixNano(1_754_800_000 * nsPerSec)
	start = HourOf(start).Sec().Nano() // 정각으로 맞춘다
	bucket := HourOf(start)

	for _, offset := range []int64{0, 1, nsPerSec, nsPerHour / 2, nsPerHour - 1} {
		if got := HourOf(start + UnixNano(offset)); got != bucket {
			t.Errorf("offset %d ns 가 버킷을 벗어남: %d != %d", offset, int64(got), int64(bucket))
		}
	}
	if got := HourOf(start + UnixNano(nsPerHour)); got != bucket+3600 {
		t.Errorf("다음 정각 = %d, want %d", int64(got), int64(bucket)+3600)
	}
	if got := HourOf(start - 1); got != bucket-3600 {
		t.Errorf("정각 직전 1ns = %d, want %d", int64(got), int64(bucket)-3600)
	}
}

func TestUnixNanoSec(t *testing.T) {
	tests := []struct {
		name string
		ts   UnixNano
		want UnixSec
	}{
		{"정확히 초", UnixNano(5 * nsPerSec), 5},
		{"초 미만 잔여는 버림", UnixNano(5*nsPerSec + 999_999_999), 5},
		{"에폭", 0, 0},
		{"음수 1ns 는 내림", -1, -1},
		{"음수 정확히 초", UnixNano(-5 * nsPerSec), -5},
		{"음수 초 미만 잔여", UnixNano(-5*nsPerSec - 1), -6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ts.Sec(); got != tt.want {
				t.Errorf("UnixNano(%d).Sec() = %d, want %d", int64(tt.ts), int64(got), int64(tt.want))
			}
		})
	}
}

func TestUnixSecNanoRoundTrip(t *testing.T) {
	for _, sec := range []UnixSec{0, 1, -1, 1_754_800_000, -1_754_800_000} {
		if got := sec.Nano().Sec(); got != sec {
			t.Errorf("UnixSec(%d).Nano().Sec() = %d", int64(sec), int64(got))
		}
	}
}

func TestTimeConversionsAreUTC(t *testing.T) {
	instant := time.Date(2026, 8, 10, 17, 34, 21, 123_456_789, time.UTC)
	if got := NanoFromTime(instant).Time(); !got.Equal(instant) {
		t.Errorf("UnixNano 왕복 실패: %s != %s", got, instant)
	}
	if got := SecFromTime(instant).Time(); !got.Equal(instant.Truncate(time.Second)) {
		t.Errorf("UnixSec 왕복 실패: %s", got)
	}
	for name, loc := range map[string]*time.Location{
		"nano": NanoFromTime(instant).Time().Location(),
		"sec":  SecFromTime(instant).Time().Location(),
		"hour": HourOf(NanoFromTime(instant)).Time().Location(),
	} {
		if loc != time.UTC {
			t.Errorf("%s 변환의 Location 이 UTC 가 아님: %s", name, loc)
		}
	}
}

func TestEventHourMatchesHourOf(t *testing.T) {
	e := baseEvent()
	if got, want := e.Hour(), HourOf(e.TS); got != want {
		t.Fatalf("Event.Hour() = %d, want %d", int64(got), int64(want))
	}
}
