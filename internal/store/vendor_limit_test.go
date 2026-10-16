package store

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

func TestVendorLimitFailurePreservesLastGoodPayload(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	good := vendorlimit.Result{Vendor: vendorlimit.VendorCodex, State: vendorlimit.StateAvailable,
		Plan: "pro", Windows: []vendorlimit.Window{{Label: "primary", UsedRatio: .5}},
		ObservedAt: "2026-08-30T00:00:00Z"}
	if err := db.UpsertVendorLimit(ctx, good, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	bad := vendorlimit.Result{Vendor: vendorlimit.VendorCodex, State: vendorlimit.StateUnavailable,
		Reason: vendorlimit.ReasonNetwork, Windows: []vendorlimit.Window{}, ObservedAt: "2026-08-30T00:01:00Z"}
	if err := db.UpsertVendorLimit(ctx, bad, time.Unix(20, 0)); err != nil {
		t.Fatal(err)
	}
	var state, plan, windows, observed string
	if err := db.SQL().QueryRowContext(ctx, `SELECT state,plan,windows_json,observed_at FROM vendor_limit_snapshots WHERE vendor=?`, "codex").Scan(&state, &plan, &windows, &observed); err != nil {
		t.Fatal(err)
	}
	if state != "unavailable" || plan != "pro" || observed != good.ObservedAt || windows == "[]" {
		t.Fatalf("마지막 정상값을 보존하지 못함: state=%s plan=%s windows=%s observed=%s", state, plan, windows, observed)
	}
}
