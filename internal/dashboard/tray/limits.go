package tray

// 벤더 한도 스냅샷 조회 (PROJ-96).
//
// 벤더 API 를 직접 두드리지 않는다. 데몬이 internal/vendorlimit 로 조회해 SQLite 에 넣어
// 둔 최신 스냅샷을 읽을 뿐이다. 화면이 직접 붙으면 트레이를 열 때마다 외부 호출이 나가고,
// 데몬과 화면이 서로 다른 시각의 값을 말하게 된다.

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

// VendorLimits 는 데몬이 저장해 둔 최신 벤더 한도 스냅샷을 읽는다.
//
// 아직 한 번도 조회되지 않은 벤더는 목록에서 빠지는 것이 아니라 unavailable 로 채운다.
// 빠지면 화면이 "아직 로딩 중" 과 구분하지 못한다.
//
// db 가 nil 이면 로컬 DB 가 아직 없다는 뜻이고 에러가 아니다 (ADR 0004). 호출자는 DB 가
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
