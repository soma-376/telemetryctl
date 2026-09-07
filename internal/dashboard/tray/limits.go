package tray

// 벤더 한도 스냅샷 조회 (PROJ-96).
//
// 데몬 내부의 트레이 조립기가 호출한다. 벤더 API를 직접 조회하지 않고, 데몬의 한도
// 갱신 경로가 SQLite에 저장한 최신 스냅샷을 읽는다. GUI는 이 함수를 호출하거나 DB를
// 열지 않고 데몬의 로컬 API 응답을 받는다 (ADR 0013).

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// Querier 는 한도 조회가 필요로 하는 최소 인터페이스다. *sql.DB 를 그대로 받지 않는
// 이유는 이 조회가 쓰기를 할 수 없다는 것을 타입으로 못박기 위해서다.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

const vendorLimitsQuery = `SELECT vendor,state,reason,detail,plan,windows_json,extra_json,observed_at
FROM vendor_limit_snapshots ORDER BY vendor`

// VendorLimits 는 데몬의 트레이 조립기가 SQLite에서 최신 벤더 한도 스냅샷을 읽을 때 쓴다.
// GUI는 이 함수를 직접 호출하지 않는다 (ADR 0013).
//
// 아직 한 번도 조회되지 않은 벤더는 목록에서 빠지는 것이 아니라 unavailable 로 채운다.
// 빠지면 화면이 "아직 로딩 중" 과 구분하지 못한다.
//
// db 가 nil 이면 조회할 DB가 없다는 뜻이며 벤더별 미조회 상태를 반환한다. 호출자는 DB 가
// 없을 때 반드시 nil 리터럴을 넘겨야 한다 — 타입이 붙은 nil 포인터를 인터페이스에 담으면
// 여기서 db != nil 이 되어 그대로 패닉으로 간다.
func VendorLimits(ctx context.Context, db Querier, now time.Time) vendorlimit.Snapshot {
	snap := vendorlimit.Snapshot{Results: make([]vendorlimit.Result, 0, len(vendorlimit.SupportedVendors()))}
	if db == nil {
		return missingLimits(snap, now)
	}
	rows, err := db.QueryContext(ctx, vendorLimitsQuery)
	if err != nil {
		return missingLimits(snap, now)
	}
	defer rows.Close()
	found := make(map[vendorlimit.Vendor]vendorlimit.Result)
	for rows.Next() {
		var out vendorlimit.Result
		var windows, extra string
		if rows.Scan(&out.Vendor, &out.State, &out.Reason, &out.Detail, &out.Plan,
			&windows, &extra, &out.ObservedAt) != nil {
			continue
		}
		if json.Unmarshal([]byte(windows), &out.Windows) != nil {
			out.Windows = []vendorlimit.Window{}
		}
		if out.Windows == nil {
			out.Windows = []vendorlimit.Window{}
		}
		_ = json.Unmarshal([]byte(extra), &out.Extra)
		found[out.Vendor] = out
		if out.ObservedAt > snap.ObservedAt {
			snap.ObservedAt = out.ObservedAt
		}
	}
	for _, vendor := range vendorlimit.SupportedVendors() {
		if result, ok := found[vendor]; ok {
			snap.Results = append(snap.Results, result)
		} else {
			snap.Results = append(snap.Results, missingLimit(vendor, now))
		}
	}
	return snap
}

func missingLimits(snap vendorlimit.Snapshot, now time.Time) vendorlimit.Snapshot {
	for _, vendor := range vendorlimit.SupportedVendors() {
		snap.Results = append(snap.Results, missingLimit(vendor, now))
	}
	return snap
}

func missingLimit(vendor vendorlimit.Vendor, now time.Time) vendorlimit.Result {
	return vendorlimit.Result{Vendor: vendor, State: vendorlimit.StateUnavailable,
		Reason: vendorlimit.ReasonNotProbed, Detail: "아직 사용 한도를 조회하지 않았다",
		Windows: []vendorlimit.Window{}, ObservedAt: now.UTC().Format(time.RFC3339)}
}
