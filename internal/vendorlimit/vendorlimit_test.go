package vendorlimit

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// bothVendorsHome 은 두 벤더 모두 로그인된 홈을 만든다.
func bothVendorsHome(t *testing.T) string {
	t.Helper()
	home := newHome(t)
	writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, testNow.Add(time.Hour), "max"))
	writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
	return home
}

// collectOptions 는 모의 서버를 가리키는 Collect 구성이다.
func collectOptions(home, claudeBase, codexBase string) Options {
	return Options{
		HomeDir: home,
		Client:  newHTTPClient(),
		now:     fixedNow,
		baseURLs: map[Vendor]string{
			VendorClaudeCode: claudeBase,
			VendorCodex:      codexBase,
		},
	}
}

func TestCollect는두벤더를모두정규화한다(t *testing.T) {
	t.Parallel()
	claudeUp := jsonUpstream(t, claudeUsageBody(testNow.Add(time.Hour).Format(time.RFC3339)))
	codexUp := jsonUpstream(t, codexUsageBody)
	home := bothVendorsHome(t)

	snap := Collect(context.Background(), collectOptions(home, claudeUp.srv.URL, codexUp.srv.URL))

	if len(snap.Results) != 2 {
		t.Fatalf("결과 = %d개: %+v", len(snap.Results), snap.Results)
	}
	if snap.ObservedAt != "2026-08-28T03:04:05Z" {
		t.Errorf("observed_at = %q", snap.ObservedAt)
	}
	// 순서가 고정이어야 화면 카드가 조회마다 자리를 바꾸지 않는다.
	if snap.Results[0].Vendor != VendorClaudeCode || snap.Results[1].Vendor != VendorCodex {
		t.Fatalf("순서가 다르다: %q, %q", snap.Results[0].Vendor, snap.Results[1].Vendor)
	}
	for _, r := range snap.Results {
		if r.State != StateAvailable {
			t.Errorf("%s: state = %q, reason = %q, detail = %q", r.Vendor, r.State, r.Reason, r.Detail)
		}
		if len(r.Windows) == 0 {
			t.Errorf("%s: 창이 비었다", r.Vendor)
		}
	}

	got, ok := snap.Result(VendorCodex)
	if !ok || got.Plan != "plus" {
		t.Errorf("Result(codex) = (%+v, %v)", got, ok)
	}
	if _, ok := snap.Result(Vendor("gemini_cli")); ok {
		t.Error("지원하지 않는 벤더가 결과에 있다")
	}

	assertSerializable(t, snap, claudeCanary, codexCanary, accountCanary, home)
}

// 티켓의 핵심 인수조건: 한 벤더의 실패가 다른 벤더의 숫자를 지우지 않는다.
func TestCollect는실패한벤더만unavailable로만든다(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// broken 은 실패시킬 벤더를, 나머지는 정상으로 둔다.
		setup      func(t *testing.T, home string) (claudeBase, codexBase string)
		broken     Vendor
		healthy    Vendor
		wantReason Reason
	}{
		{
			name: "Claude 자격증명만 없다",
			setup: func(t *testing.T, home string) (string, string) {
				writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
				return jsonUpstream(t, claudeUsageBody(testNow.Format(time.RFC3339))).srv.URL,
					jsonUpstream(t, codexUsageBody).srv.URL
			},
			broken: VendorClaudeCode, healthy: VendorCodex, wantReason: ReasonCredentialMissing,
		},
		{
			name: "Codex 쪽만 네트워크가 끊겼다",
			setup: func(t *testing.T, home string) (string, string) {
				writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, testNow.Add(time.Hour), "max"))
				writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
				return jsonUpstream(t, claudeUsageBody(testNow.Format(time.RFC3339))).srv.URL, deadUpstream(t)
			},
			broken: VendorCodex, healthy: VendorClaudeCode, wantReason: ReasonNetwork,
		},
		{
			name: "Claude 토큰만 만료됐다",
			setup: func(t *testing.T, home string) (string, string) {
				writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, testNow.Add(-time.Hour), "max"))
				writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
				return statusUpstream(t, http.StatusUnauthorized).srv.URL, jsonUpstream(t, codexUsageBody).srv.URL
			},
			broken: VendorClaudeCode, healthy: VendorCodex, wantReason: ReasonTokenExpired,
		},
		{
			name: "Codex 응답 모양만 바뀌었다",
			setup: func(t *testing.T, home string) (string, string) {
				writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, testNow.Add(time.Hour), "max"))
				writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
				return jsonUpstream(t, claudeUsageBody(testNow.Format(time.RFC3339))).srv.URL,
					jsonUpstream(t, `{"limits":"바뀐 모양"}`).srv.URL
			},
			broken: VendorCodex, healthy: VendorClaudeCode, wantReason: ReasonResponseUnrecognized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := newHome(t)
			claudeBase, codexBase := tc.setup(t, home)

			snap := Collect(context.Background(), collectOptions(home, claudeBase, codexBase))

			broken, ok := snap.Result(tc.broken)
			if !ok {
				t.Fatalf("%s 결과가 통째로 빠졌다 — 화면이 로딩 중과 구분하지 못한다", tc.broken)
			}
			if broken.State != StateUnavailable || broken.Reason != tc.wantReason {
				t.Errorf("%s: state = %q, reason = %q, want unavailable/%q (detail=%q)",
					tc.broken, broken.State, broken.Reason, tc.wantReason, broken.Detail)
			}

			healthy, ok := snap.Result(tc.healthy)
			if !ok {
				t.Fatalf("%s 결과가 빠졌다", tc.healthy)
			}
			if healthy.State != StateAvailable {
				t.Fatalf("%s 까지 같이 죽었다: reason = %q, detail = %q",
					tc.healthy, healthy.Reason, healthy.Detail)
			}
			if len(healthy.Windows) == 0 {
				t.Errorf("%s 의 창이 비었다", tc.healthy)
			}

			assertSerializable(t, snap, claudeCanary, codexCanary, accountCanary, home)
		})
	}
}

func TestCollect는요청한벤더만조회한다(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want []Vendor
		got  []Vendor
	}{
		{name: "비우면 전부", want: nil, got: SupportedVendors()},
		{name: "Codex 만", want: []Vendor{VendorCodex}, got: []Vendor{VendorCodex}},
		{name: "지원하지 않는 벤더는 건너뛴다", want: []Vendor{Vendor("gemini_cli"), Vendor("cursor")}, got: nil},
		{name: "지원 벤더만 걸러 낸다", want: []Vendor{Vendor("cursor"), VendorClaudeCode}, got: []Vendor{VendorClaudeCode}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := bothVendorsHome(t)
			claudeUp := jsonUpstream(t, claudeUsageBody(testNow.Format(time.RFC3339)))
			codexUp := jsonUpstream(t, codexUsageBody)

			opts := collectOptions(home, claudeUp.srv.URL, codexUp.srv.URL)
			opts.Vendors = tc.want
			snap := Collect(context.Background(), opts)

			if len(snap.Results) != len(tc.got) {
				t.Fatalf("결과 = %d개, want %d: %+v", len(snap.Results), len(tc.got), snap.Results)
			}
			for i, v := range tc.got {
				if snap.Results[i].Vendor != v {
					t.Errorf("results[%d] = %q, want %q", i, snap.Results[i].Vendor, v)
				}
			}
		})
	}
}

// 어댑터가 패닉해도 다른 벤더의 결과는 살아야 한다. 고루틴 안의 패닉은 프로세스를 죽인다.
func TestSafeProbe는패닉을가둔다(t *testing.T) {
	t.Parallel()
	env := probeEnv{home: t.TempDir(), client: newHTTPClient(), now: fixedNow}

	got := safeProbe(context.Background(), panicAdapter{}, env)

	if got.State != StateUnavailable || got.Reason != ReasonInternal {
		t.Fatalf("state = %q, reason = %q", got.State, got.Reason)
	}
	if got.Vendor != VendorCodex {
		t.Errorf("vendor = %q", got.Vendor)
	}
	// 패닉 값에 비밀이 들어 있어도 밖으로 나오면 안 된다.
	assertNoSecret(t, "패닉 결과", allStrings(got), codexCanary)
	assertSerializable(t, got, codexCanary)
}

// panicAdapter 는 남의 JSON 모양이 바뀌어 어댑터가 깨지는 상황을 흉내 낸다.
type panicAdapter struct{}

func (panicAdapter) vendor() Vendor { return VendorCodex }

func (panicAdapter) probe(context.Context, probeEnv) Result {
	panic("어댑터 내부에서 " + codexCanary + " 를 다루다 깨졌다")
}

func TestResolveHome(t *testing.T) {
	t.Parallel()
	if got, err := resolveHome("/tmp/fake-home"); err != nil || got != "/tmp/fake-home" {
		t.Fatalf("resolveHome = (%q, %v)", got, err)
	}
	// 지정하지 않으면 hostenv 가 판별한다. 값 자체는 환경에 달렸으므로 비어 있지 않은지만 본다.
	got, err := resolveHome("")
	if err != nil {
		t.Skipf("이 환경에서는 홈을 판별할 수 없다: %v", err)
	}
	if got == "" {
		t.Error("홈이 빈 문자열이다")
	}
}

// 자격증명이 하나도 없는 깨끗한 장비에서도 Collect 는 모양을 유지해야 한다.
// 미설치는 오류가 아니라 상태다 (ADR 0004 의 "미설치 상태는 error 가 아니다").
func TestCollect는미설치장비에서도모양을유지한다(t *testing.T) {
	t.Parallel()
	home := newHome(t)

	snap := Collect(context.Background(), collectOptions(home, deadUpstream(t), deadUpstream(t)))

	if len(snap.Results) != 2 {
		t.Fatalf("결과 = %d개", len(snap.Results))
	}
	for _, r := range snap.Results {
		if r.State != StateUnavailable || r.Reason != ReasonCredentialMissing {
			t.Errorf("%s: state = %q, reason = %q", r.Vendor, r.State, r.Reason)
		}
		if r.Windows == nil {
			t.Errorf("%s: Windows 가 nil", r.Vendor)
		}
	}
	assertSerializable(t, snap, home)
}
