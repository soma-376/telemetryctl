package dashboard

import (
	"fmt"
	"time"
)

// 시간대가 이 패키지에서 가장 조용히 틀리기 쉬운 부분이다.
//
// rollup_hourly.hour 는 **UTC** 시간 버킷이고 화면은 **사용자 시간대**의 "오늘" 을 묻는다.
// Asia/Seoul(UTC+9)의 오늘은 UTC 로 어제 15:00 에 시작한다. UTC 자정으로 잘라 답하면
// 매일 9시간이 어긋나고, 그 오차는 아침에 크고 저녁에 사라져 "가끔 숫자가 이상하다" 로만
// 보고된다. 그래서 경계 계산은 전부 time.Location 위에서 한다.
//
// # DST
//
// Asia/Seoul 에는 DST 가 없지만 API 는 임의 tz 를 받는다. 하루가 23시간이거나 25시간인
// 날이 있으므로 "24시간을 빼면 어제" 가 아니다. 여기서는 자정을 달력 기준(AddDate)으로만
// 옮긴다 — AddDate 는 날짜를 하나 바꾼 뒤 그 시간대에서 다시 해석하므로 23/25시간 하루가
// 저절로 맞는다. 자정 자체가 존재하지 않는 날(일부 시간대의 봄 전환)은 time.Date 가
// 유효한 인스턴트로 정규화한다.
//
// # 시간 버킷의 귀속
//
// UTC+5:30 같은 30·45분 오프셋에서는 현지 자정이 시간 버킷 한가운데에 떨어진다. 이때
// 버킷은 **자기 시작 시각이 속한 현지 날짜** 에 통째로 귀속된다. 겹치지도 비지도 않는
// 규칙이라 합계가 두 번 세지지 않는 대신, 그런 시간대에서는 하루 경계에 최대 한 버킷만큼의
// 근사가 남는다. 정시 오프셋(대부분의 시간대)에서는 정확하다.

// loadLocation 은 시간대 문자열을 해석한다.
//
// 빈 문자열은 UTC 다 (time.LoadLocation 의 규약). 잘못된 문자열은 명확한 에러로 거부한다 —
// 조용히 UTC 로 떨어지면 사용자는 자기 시간대가 무시된 줄 모른 채 9시간 어긋난 숫자를 본다.
func loadLocation(tz string) (*time.Location, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		// 원인 에러를 감싸지 않는다. tzdata 경로 같은 내부 사정이 화면에 뜰 이유가 없다.
		return nil, fmt.Errorf("dashboard: 알 수 없는 시간대 %q", tz)
	}
	return loc, nil
}

// timeRange 는 [Start, End) 반열린 구간이다. 끝을 배타로 두어야 자정이 두 날에 동시에
// 속하지 않는다.
type timeRange struct {
	Start time.Time
	End   time.Time
}

// StartSec·EndSec 는 rollup_hourly.hour·sessions.started_at 와 비교할 UTC unix 초다.
func (r timeRange) StartSec() int64 { return r.Start.Unix() }
func (r timeRange) EndSec() int64   { return r.End.Unix() }

// dayOf 는 t 가 속한 현지 하루다.
func dayOf(t time.Time, loc *time.Location) timeRange {
	local := t.In(loc)
	y, m, d := local.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	return timeRange{Start: start, End: start.AddDate(0, 0, 1)}
}

// previousDay 는 하루 앞의 현지 하루다. Today 의 "어제 대비" 가 쓴다.
func previousDay(day timeRange, loc *time.Location) timeRange {
	start := day.Start.In(loc).AddDate(0, 0, -1)
	return timeRange{Start: start, End: day.Start}
}

// lastDays 는 오늘을 포함한 최근 days 일 구간이다. days<=0 이면 하루로 본다.
func lastDays(now time.Time, loc *time.Location, days int) timeRange {
	today := dayOf(now, loc)
	if days <= 1 {
		return today
	}
	return timeRange{Start: today.Start.AddDate(0, 0, -(days - 1)), End: today.End}
}

// localHour 는 UTC unix 초를 현지 시각의 0~23 시로 옮긴다. 시간대별 집중도가 쓴다.
func localHour(sec int64, loc *time.Location) int {
	return time.Unix(sec, 0).In(loc).Hour()
}

// localDayStart 는 UTC unix 초가 속한 현지 하루의 시작이다. 일별 집계가 쓴다.
func localDayStart(sec int64, loc *time.Location) time.Time {
	return dayOf(time.Unix(sec, 0), loc).Start
}

// dateKey 는 화면과 TS 가 그대로 쓰는 날짜 문자열이다.
const dateKey = "2006-01-02"
