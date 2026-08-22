package dashboard

import (
	"context"
	"fmt"
	"time"
)

// Dim 은 rollup_hourly.dim 값이다. rollup.Dim 과 같은 어휘지만 타입을 여기 다시 두는 이유는
// GUI 표면이 집계기 내부 타입에 묶이지 않게 하려는 것이다 — 이 값은 TS 로 그대로 나간다.
type Dim string

const (
	DimTotal   Dim = "total"
	DimVendor  Dim = "vendor"
	DimModel   Dim = "model"
	DimTool    Dim = "tool"
	DimProject Dim = "project"
	DimType    Dim = "type"
)

var validDims = map[Dim]bool{
	DimTotal: true, DimVendor: true, DimModel: true,
	DimTool: true, DimProject: true, DimType: true,
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
	defaultBreakdownDays  = 7
	maxBreakdownDays      = 400 // 고정 보존 기간과 같다. 그보다 오래된 롤업은 없다
	defaultBreakdownLimit = 50
	maxBreakdownLimit     = 500
	hoursPerDay           = 24
)

// BreakdownQuery 는 롤업 집계 조회 조건이다.
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
	// Label 은 표시용 문자열이다. dim=project 면 project_name 을 채운다 — key 는 해시라
	// 그대로 보여 줄 수 없다. 이름을 못 찾으면 Key 와 같다.
	Label string `json:"label"`
	// StartAt 은 BucketDay 에서 그 날의 시작(UTC unix 초)이다. 다른 모드에서는 0.
	StartAt int64 `json:"start_at"`
	Totals
}

// Breakdown 은 rollup_hourly 집계다 — 「Agent 사용 비율」과 「시간대별 집중도」가 여기서 나온다.
func (r *Reader) Breakdown(ctx context.Context, q BreakdownQuery) ([]Row, error) {
	dim := q.Dim
	if dim == "" {
		dim = DimTotal
	}
	if !validDims[dim] {
		// 조용히 빈 결과를 주면 화면이 오타를 "데이터 없음" 으로 오해한다.
		return nil, fmt.Errorf("dashboard: 알 수 없는 집계 축 %q", string(q.Dim))
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

	switch q.Bucket {
	case BucketKey:
		return breakdownByKey(ctx, db, dim, tr, clampLimit(q.Limit, defaultBreakdownLimit, maxBreakdownLimit))
	case BucketHourOfDay, BucketDay:
		return breakdownByTime(ctx, db, dim, q.Key, tr, loc, q.Bucket)
	default:
		return nil, fmt.Errorf("dashboard: 알 수 없는 묶음 단위 %q", string(q.Bucket))
	}
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

// dim='total' 의 key 는 항상 빈 문자열이라 GROUP BY 해도 한 행이다. 그대로 두면 화면이
// "전체" 한 줄을 받는다 — 특수 취급하지 않는 편이 분기를 줄인다.
const breakdownByKeySQL = `SELECT "key", ` + rollupSumColumns + `
FROM rollup_hourly
WHERE dim = ? AND hour >= ? AND hour < ?
GROUP BY "key"
ORDER BY SUM(cost_usd) DESC, SUM(prompts) DESC, "key" ASC
LIMIT ?`

func breakdownByKey(ctx context.Context, db sqlQuerier, dim Dim, tr timeRange, limit int) (out []Row, err error) {
	const op = "집계 조회"
	rows, err := db.QueryContext(ctx, breakdownByKeySQL, string(dim), tr.StartSec(), tr.EndSec(), limit)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	out = []Row{}
	for rows.Next() {
		var row Row
		dest := append([]any{&row.Key}, totalsDest(&row.Totals)...)
		if serr := rows.Scan(dest...); serr != nil {
			return nil, queryErr(op, serr)
		}
		row.Label = row.Key
		out = append(out, row)
	}
	if dim == DimProject {
		if err := labelProjects(ctx, db, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// breakdownByTime 은 시간·날짜 축 집계다.
//
// SQL 로 묶지 않고 행을 받아 Go 에서 묶는다. rollup_hourly.hour 는 UTC 이고 축은 현지
// 시각이라, SQL 로 하려면 고정 오프셋을 박아야 하는데 그러면 DST 가 있는 시간대에서 전환일
// 전후가 한 시간씩 밀린다. time.Location 위에서 계산하면 그 문제가 없다.
// 구간이 최대 400일이라 행 수는 유계다.
const breakdownRawSQL = `SELECT hour, ` + rollupRawColumns + `
FROM rollup_hourly
WHERE dim = ? AND hour >= ? AND hour < ?`

func breakdownByTime(ctx context.Context, db sqlQuerier, dim Dim, key string, tr timeRange, loc *time.Location, bucket BucketBy) (out []Row, err error) {
	const op = "시간대별 집계 조회"

	query := breakdownRawSQL
	args := []any{string(dim), tr.StartSec(), tr.EndSec()}
	if dim == DimTotal {
		// dim='total' 의 key 는 '' 하나뿐이라 조건이 필요 없다.
		key = ""
	}
	if key != "" {
		query += ` AND "key" = ?`
		args = append(args, key)
	}

	skeleton := emptySkeleton(bucket, tr, loc)
	index := make(map[string]int, len(skeleton))
	for i, row := range skeleton {
		index[row.Key] = i
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			hour int64
			t    Totals
		)
		dest := append([]any{&hour}, totalsDest(&t)...)
		if serr := rows.Scan(dest...); serr != nil {
			return nil, queryErr(op, serr)
		}
		k := bucketKeyOf(bucket, hour, loc)
		i, ok := index[k]
		if !ok {
			// 골격 밖의 버킷이다. From·To 를 직접 준 구간에서 경계가 정시가 아닐 때 생긴다.
			continue
		}
		skeleton[i].Totals.add(t)
	}
	return skeleton, nil
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

// labelProjects 는 project_hash 행에 사람이 읽을 이름을 붙인다.
//
// rollup_hourly 의 dim='project' key 는 해시다 (basename 은 서로 다른 프로젝트끼리 충돌해
// 집계가 합쳐지므로 집계기가 해시를 쓴다). 화면에는 이름이 필요하니 sessions 에서 가져온다.
func labelProjects(ctx context.Context, db sqlQuerier, rows []Row) (err error) {
	const op = "프로젝트 이름 조회"
	hashes := make([]any, 0, len(rows))
	for _, r := range rows {
		if r.Key != "" {
			hashes = append(hashes, r.Key)
		}
	}
	if len(hashes) == 0 {
		return nil
	}
	// 가장 최근 세션의 이름을 고른다. 프로젝트 디렉터리 이름이 바뀌면 같은 해시에 여러 이름이
	// 붙는데, 고르는 규칙이 없으면 새로고침마다 화면의 이름이 바뀐다.
	// MAX() 와 함께 쓴 벌거벗은 컬럼이 그 최댓값 행에서 온다는 것은 SQLite 가 문서화한 동작이다.
	query := `SELECT project_hash, project_name, MAX(started_at) FROM sessions
WHERE project_hash IN (` + placeholders(len(hashes)) + `) AND project_name IS NOT NULL
GROUP BY project_hash`
	sqlRows, err := db.QueryContext(ctx, query, hashes...)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(sqlRows, op, &err)

	names := make(map[string]string, len(hashes))
	for sqlRows.Next() {
		var (
			hash, name string
			latest     int64
		)
		if serr := sqlRows.Scan(&hash, &name, &latest); serr != nil {
			return queryErr(op, serr)
		}
		names[hash] = name
	}
	for i := range rows {
		if name, ok := names[rows[i].Key]; ok && name != "" {
			rows[i].Label = name
		}
	}
	return nil
}
