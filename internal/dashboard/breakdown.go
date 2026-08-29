package dashboard

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/your-org/pulsemetry/internal/store"
)

// Dim 은 집계 축이다. 이 값은 TS 바인딩으로 그대로 나가므로 문자열이 곧 계약이다.
type Dim string

const (
	DimTotal  Dim = "total"
	DimVendor Dim = "vendor"
	DimModel  Dim = "model"
	DimTool   Dim = "tool"
	// DimProject 는 sessions.workspace_path 축이다. v1 의 project_hash 는 v3 에 없고,
	// 대신 ADR 0010 이 워크스페이스 원경로를 로컬에 저장하기로 했다. Key 는 그 경로이고
	// Label 은 basename 이다 — 경로를 그대로 표에 넣으면 한 줄이 화면을 넘긴다.
	DimProject Dim = "project"
)

// validDims 는 받아들이는 축이다.
//
// v1 의 `type` 축(events.type)은 여기 없다. v3 events 에는 벤더 속성을 담는 컬럼이
// 아예 없어 그 축을 만들 입력 자체가 사라졌다 (ADR 0009). 조용히 빈 결과를 주는 대신
// 알 수 없는 축으로 거절한다 — 항상 비어 있는 축을 받아 주면 화면이 "데이터 없음" 으로
// 오해한다.
var validDims = map[Dim]bool{
	DimTotal: true, DimVendor: true, DimModel: true,
	DimTool: true, DimProject: true,
}

// BucketBy 는 Breakdown 이 무엇을 축으로 묶을지 정한다.
type BucketBy string

const (
	// BucketKey 는 dim 의 key 별 합계다. 「Agent 사용 비율」(dim=vendor)이 이것이다.
	BucketKey BucketBy = ""
	// BucketHourOfDay 는 현지 0~23시별 합계다. 「시간대별 집중도」가 이것이다.
	// 항상 24행을 돌려준다 — 빈 시간대가 빠지면 화면의 막대 그래프에 구멍이 생긴다.
	BucketHourOfDay BucketBy = "hour_of_day"
	// BucketDay 는 현지 날짜별 합계다. 추세 그래프용이고, 데이터가 없는 날도 0 행으로 채운다.
	BucketDay BucketBy = "day"
)

const (
	defaultBreakdownDays = 7
	// maxBreakdownDays 는 고정 보존 기간과 같다. 그보다 오래된 데이터는 없다.
	maxBreakdownDays      = store.DefaultRetentionDays
	defaultBreakdownLimit = 50
	maxBreakdownLimit     = 500
	hoursPerDay           = 24
)

// BreakdownQuery 는 집계 조회 조건이다.
//
// 구간은 두 방식 중 하나로 준다.
//   - From·To 를 직접 준다 (UTC unix 초, To 배타). To 가 0 이면 지금까지.
//   - 둘 다 0 이면 TZ 기준 최근 Days 일(오늘 포함, 기본 7일)이다.
type BreakdownQuery struct {
	Dim Dim `json:"dim"`
	// Key 는 Bucket 모드에서 특정 key 로 좁힐 때 쓴다 (예: dim=vendor, key=claude_code 의
	// 시간대별 집중도). BucketKey 모드에서는 무시한다.
	Key string `json:"key"`

	TZ   string `json:"tz"`
	Days int    `json:"days"`
	From int64  `json:"from"`
	To   int64  `json:"to"`

	Bucket BucketBy `json:"bucket"`
	// Limit 은 BucketKey 모드에만 적용된다. 시간·날짜 축은 구간이 곧 개수다.
	Limit int `json:"limit"`
}

// Row 는 Breakdown 한 행이다. Totals 를 임베드해 JSON 에서 평평하게 펼쳐진다.
type Row struct {
	// Key 는 dim 의 key(BucketKey), "00"~"23"(BucketHourOfDay), "2026-08-10"(BucketDay) 다.
	Key string `json:"key"`
	// Label 은 표시용 문자열이다. dim=project 면 워크스페이스 경로의 basename 을 채운다 —
	// 전체 경로는 표 한 줄을 넘긴다. 다른 축에서는 Key 와 같다.
	Label string `json:"label"`
	// StartAt 은 BucketDay 에서 그 날의 시작(UTC unix 초)이다. 다른 모드에서는 0.
	StartAt int64 `json:"start_at"`
	Totals
}

// Breakdown 은 조회 시점 집계다 — 「Agent 사용 비율」과 「시간대별 집중도」가 여기서 나온다.
func (r *Reader) Breakdown(ctx context.Context, q BreakdownQuery) ([]Row, error) {
	dim := q.Dim
	if dim == "" {
		dim = DimTotal
	}
	if !validDims[dim] {
		// 조용히 빈 결과를 주면 화면이 오타를 "데이터 없음" 으로 오해한다.
		return nil, fmt.Errorf("dashboard: 알 수 없는 집계 축 %q", string(q.Dim))
	}
	switch q.Bucket {
	case BucketKey, BucketHourOfDay, BucketDay:
	default:
		return nil, fmt.Errorf("dashboard: 알 수 없는 묶음 단위 %q", string(q.Bucket))
	}
	loc, err := loadLocation(q.TZ)
	if err != nil {
		return nil, err
	}
	tr := q.resolveRange(r.now(), loc)

	db, ok := r.db()
	if !ok {
		// 미설치라도 시간·날짜 축은 골격을 돌려준다. 빈 그래프와 없는 그래프는 다르다.
		return emptySkeleton(q.Bucket, tr, loc), nil
	}

	key := q.Key
	if dim == DimTotal || q.Bucket == BucketKey {
		// 키별 집계는 모든 키를 돌려주는 것이 목적이고, total 에는 키가 하나뿐이다.
		key = ""
	}
	rows, err := aggregate(ctx, db, dim, key, tr)
	if err != nil {
		return nil, err
	}

	if q.Bucket == BucketKey {
		return byKey(dim, rows, clampLimit(q.Limit, defaultBreakdownLimit, maxBreakdownLimit)), nil
	}
	return byTime(rows, tr, loc, q.Bucket), nil
}

// resolveRange 는 From·To 또는 Days 에서 실제 구간을 만든다.
func (q BreakdownQuery) resolveRange(now time.Time, loc *time.Location) timeRange {
	if q.From > 0 || q.To > 0 {
		start := time.Unix(q.From, 0)
		end := now
		if q.To > 0 {
			end = time.Unix(q.To, 0)
		}
		if end.Before(start) {
			end = start
		}
		return timeRange{Start: start, End: end}
	}
	days := q.Days
	if days <= 0 {
		days = defaultBreakdownDays
	}
	if days > maxBreakdownDays {
		days = maxBreakdownDays
	}
	return lastDays(now, loc, days)
}

// byKey 는 시간 버킷을 접어 키별 합계를 만든다.
//
// dim='total' 의 키는 항상 빈 문자열이라 한 행이 된다. 그대로 두면 화면이 "전체" 한 줄을
// 받는다 — 특수 취급하지 않는 편이 분기를 줄인다.
func byKey(dim Dim, rows []aggRow, limit int) []Row {
	out := []Row{}
	index := map[string]int{}
	for _, r := range rows {
		i, ok := index[r.Key]
		if !ok {
			i = len(out)
			index[r.Key] = i
			out = append(out, Row{Key: r.Key, Label: labelFor(dim, r.Key)})
		}
		out[i].Totals.add(r.Totals)
	}
	// 비용 내림차순이 화면의 기본 순서다. 비용이 없는 축(도구·프롬프트만 있는 구간)에서도
	// 순서가 흔들리지 않도록 프롬프트 수와 키를 차례로 얹는다.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		if out[i].Prompts != out[j].Prompts {
			return out[i].Prompts > out[j].Prompts
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// labelFor 는 화면에 보일 문자열이다.
func labelFor(dim Dim, key string) string {
	if dim != DimProject || key == "" {
		return key
	}
	// 워크스페이스 경로의 basename 이 사람이 부르는 프로젝트 이름이다. 전체 경로는 JSON 의
	// Key 에 그대로 남아 「작업 폴더 열기」 가 쓸 수 있다.
	return filepath.Base(filepath.Clean(key))
}

// byTime 은 시간·날짜 축 집계다. UTC 시간 버킷을 현지 시각으로 귀속시킨다 (timezone.go).
func byTime(rows []aggRow, tr timeRange, loc *time.Location, bucket BucketBy) []Row {
	skeleton := emptySkeleton(bucket, tr, loc)
	index := make(map[string]int, len(skeleton))
	for i, row := range skeleton {
		index[row.Key] = i
	}
	for _, r := range rows {
		i, ok := index[bucketKeyOf(bucket, r.Hour, loc)]
		if !ok {
			// 골격 밖의 버킷이다. From·To 를 직접 준 구간에서 경계가 정시가 아닐 때 생긴다.
			continue
		}
		skeleton[i].Totals.add(r.Totals)
	}
	return skeleton
}

func bucketKeyOf(bucket BucketBy, hour int64, loc *time.Location) string {
	if bucket == BucketHourOfDay {
		return fmt.Sprintf("%02d", localHour(hour, loc))
	}
	return localDayStart(hour, loc).Format(dateKey)
}

// emptySkeleton 은 값이 0 인 축 골격이다. 데이터가 없는 시간·날짜가 목록에서 빠지면
// 화면의 막대가 옆으로 밀려 다른 시간대의 값처럼 보인다.
func emptySkeleton(bucket BucketBy, tr timeRange, loc *time.Location) []Row {
	switch bucket {
	case BucketHourOfDay:
		out := make([]Row, hoursPerDay)
		for h := range out {
			k := fmt.Sprintf("%02d", h)
			out[h] = Row{Key: k, Label: k}
		}
		return out
	case BucketDay:
		out := []Row{}
		day := dayOf(tr.Start, loc)
		for !day.Start.After(tr.End.Add(-time.Nanosecond)) {
			k := day.Start.Format(dateKey)
			out = append(out, Row{Key: k, Label: k, StartAt: day.StartSec()})
			day = timeRange{Start: day.End, End: day.End.AddDate(0, 0, 1)}
			if len(out) > maxBreakdownDays {
				break
			}
		}
		return out
	default:
		return []Row{}
	}
}
