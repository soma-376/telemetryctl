package tray

import (
	"context"
	"testing"

	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// DB 가 아직 없는 것은 에러가 아니라 "미설치" 다 (ADR 0004). 그때도 벤더가 목록에서
// 빠지지 않고 unavailable 로 자리를 지켜야 화면이 "로딩 중" 과 구분한다.
func TestVendorLimitsWithoutDatabaseKeepsEveryVendor(t *testing.T) {
	snap := VendorLimits(context.Background(), nil, testNow)

	if len(snap.Results) != len(vendorlimit.SupportedVendors()) {
		t.Fatalf("results=%d, want %d", len(snap.Results), len(vendorlimit.SupportedVendors()))
	}
	for _, res := range snap.Results {
		if res.State != vendorlimit.StateUnavailable {
			t.Errorf("%s state=%q, want unavailable", res.Vendor, res.State)
		}
		// nil 슬라이스는 JSON 에서 null 이 되어 프런트엔드가 분기해야 한다.
		if res.Windows == nil {
			t.Errorf("%s windows 가 nil 이다", res.Vendor)
		}
		if res.ObservedAt != "2026-08-10T02:00:00Z" {
			t.Errorf("%s observed_at=%q", res.Vendor, res.ObservedAt)
		}
	}
}
