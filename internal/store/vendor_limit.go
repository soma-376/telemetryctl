package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

const upsertVendorLimitSQL = `INSERT INTO vendor_limit_snapshots
  (vendor,state,reason,detail,plan,windows_json,extra_json,observed_at,checked_at)
VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT(vendor) DO UPDATE SET
  state=excluded.state, reason=excluded.reason, detail=excluded.detail,
  plan=CASE WHEN excluded.state='available' THEN excluded.plan ELSE vendor_limit_snapshots.plan END,
  windows_json=CASE WHEN excluded.state='available' THEN excluded.windows_json ELSE vendor_limit_snapshots.windows_json END,
  extra_json=CASE WHEN excluded.state='available' THEN excluded.extra_json ELSE vendor_limit_snapshots.extra_json END,
  observed_at=CASE WHEN excluded.state='available' THEN excluded.observed_at ELSE vendor_limit_snapshots.observed_at END,
  checked_at=excluded.checked_at`

// UpsertVendorLimit 는 벤더 하나의 최신 한도 결과를 원자적으로 교체한다.
func (d *DB) UpsertVendorLimit(ctx context.Context, result vendorlimit.Result, checkedAt time.Time) error {
	windows, err := json.Marshal(result.Windows)
	if err != nil {
		return fmt.Errorf("store: 한도 창 인코딩: %w", err)
	}
	extra, err := json.Marshal(result.Extra)
	if err != nil {
		return fmt.Errorf("store: 추가 한도 인코딩: %w", err)
	}
	_, err = d.db.ExecContext(ctx, upsertVendorLimitSQL, result.Vendor, result.State,
		result.Reason, result.Detail, result.Plan, string(windows), string(extra),
		result.ObservedAt, checkedAt.UTC().Unix())
	if err != nil {
		return fmt.Errorf("store: 벤더 한도 upsert: %w", err)
	}
	return nil
}
