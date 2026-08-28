package vendorlimit

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 이 패키지의 가장 중요한 불변식이다.
//
// 토큰은 메모리에만 살고 SQLite·로그·오류·Wails 응답 어디에도 남지 않는다. 성공 경로만
// 보면 아무것도 증명하지 못한다 — 토큰이 새는 곳은 실패 경로다. 오류 메시지에 응답 본문이
// 실리고, *url.Error 가 URL 을 통째로 담아 오고, 파싱 실패 메시지가 원문 조각을 물고 온다.
// 그래서 모든 경로의 결과와 오류 문자열을 한 자루에 모아 놓고 한 번에 단언한다
// (internal/session/privacy_test.go 와 같은 방식).
//
// 홈 경로도 같이 금지한다. Result.Detail 은 GUI 로 그대로 나가고, 전체 경로는 로컬에만
// 두는 값이다 (ADR 0003).
func TestTokenNeverEscapes(t *testing.T) {
	t.Parallel()

	var bag []string
	add := func(label string, v any) {
		t.Helper()
		bag = append(bag, allStrings(v)...)
		// 직렬화 경로도 따로 태운다. Wails 바인딩이 지나가는 길이다 (ADR 0004).
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: json.Marshal 실패: %v", label, err)
		}
		bag = append(bag, string(b))
	}
	addErr := func(err error) {
		if err != nil {
			bag = append(bag, err.Error())
		}
	}

	resetsAt := testNow.Add(time.Hour).Format(time.RFC3339)

	// 각 경로마다 홈을 새로 만들고, 그 홈 경로 자체도 금지 문자열에 넣는다.
	type path struct {
		name       string
		home       string
		claudeBase string
		codexBase  string
	}
	var paths []path
	newPath := func(name string, setup func(home string) (string, string)) {
		home := t.TempDir()
		cb, xb := setup(home)
		paths = append(paths, path{name: name, home: home, claudeBase: cb, codexBase: xb})
	}

	writeBoth := func(home string, claudeExpiry time.Time) {
		writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, claudeExpiry, "max"))
		writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
	}
	okClaude := func() string { return jsonUpstream(t, claudeUsageBody(resetsAt)).srv.URL }
	okCodex := func() string { return jsonUpstream(t, codexUsageBody).srv.URL }

	newPath("성공", func(home string) (string, string) {
		writeBoth(home, testNow.Add(time.Hour))
		return okClaude(), okCodex()
	})
	newPath("자격증명 없음", func(string) (string, string) {
		return okClaude(), okCodex()
	})
	newPath("자격증명 형식 변경", func(home string) (string, string) {
		writeClaudeCredential(t, home, `{"claudeAiOauth":{"token":"`+claudeCanary+`"}}`)
		writeCodexAuth(t, home, `{"tokens":{"bearer":"`+codexCanary+`"}}`)
		return okClaude(), okCodex()
	})
	newPath("파일이 통째로 토큰 문자열", func(home string) (string, string) {
		// JSON 파싱 실패 메시지가 원문 조각을 물고 오는 경로다.
		writeClaudeCredential(t, home, claudeCanary)
		writeCodexAuth(t, home, codexCanary)
		return okClaude(), okCodex()
	})
	newPath("토큰 만료", func(home string) (string, string) {
		writeBoth(home, testNow.Add(-time.Hour))
		return statusUpstream(t, http.StatusUnauthorized).srv.URL, statusUpstream(t, http.StatusForbidden).srv.URL
	})
	newPath("상위 5xx", func(home string) (string, string) {
		writeBoth(home, testNow.Add(time.Hour))
		return statusUpstream(t, http.StatusInternalServerError).srv.URL, statusUpstream(t, http.StatusBadGateway).srv.URL
	})
	newPath("본문이 JSON 이 아님", func(home string) (string, string) {
		writeBoth(home, testNow.Add(time.Hour))
		return jsonUpstream(t, `<html>maintenance</html>`).srv.URL, jsonUpstream(t, `nope`).srv.URL
	})
	newPath("API 모양 변경", func(home string) (string, string) {
		writeBoth(home, testNow.Add(time.Hour))
		return jsonUpstream(t, `{"limits":[]}`).srv.URL, jsonUpstream(t, `{"limits":[]}`).srv.URL
	})
	newPath("네트워크 장애", func(home string) (string, string) {
		writeBoth(home, testNow.Add(time.Hour))
		return deadUpstream(t), deadUpstream(t)
	})
	newPath("요청을 되비추는 상위", func(home string) (string, string) {
		// Authorization 헤더를 200 본문에 그대로 실어 돌려주는 상위. 응답 본문을 오류나
		// 결과에 싣는 순간 토큰이 새는 가장 현실적인 경로다.
		echo := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"echo":"` + r.Header.Get("Authorization") + `"}`))
		})
		writeBoth(home, testNow.Add(time.Hour))
		return echo.srv.URL, echo.srv.URL
	})

	homes := make([]string, 0, len(paths))
	for _, p := range paths {
		homes = append(homes, p.home)
		snap := Collect(context.Background(), collectOptions(p.home, p.claudeBase, p.codexBase))
		add(p.name, snap)

		// 어댑터를 거치지 않은 날것의 오류도 자루에 넣는다. Result 로 옮기는 과정에서
		// 걸러지는 것이 아니라 애초에 오류에 토큰이 없어야 한다.
		_, err := loadClaudeCredential(p.home)
		addErr(err)
		_, err = loadCodexCredential(p.home)
		addErr(err)
		addErr(getJSON(context.Background(), newHTTPClient(), p.claudeBase+claudeUsagePath,
			newToken(claudeCanary), map[string]string{"anthropic-beta": claudeOAuthBeta}, new(map[string]any)))
		addErr(getJSON(context.Background(), newHTTPClient(), p.codexBase+codexUsagePath,
			newToken(codexCanary), map[string]string{codexAccountHeader: accountCanary}, new(map[string]any)))
	}

	// 컨텍스트 취소 경로.
	blocked := make(chan struct{})
	hang := newUpstream(t, func(http.ResponseWriter, *http.Request) { <-blocked })
	t.Cleanup(func() { close(blocked) })
	hangHome := t.TempDir()
	homes = append(homes, hangHome)
	writeBoth(hangHome, testNow.Add(time.Hour))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	add("취소", Collect(ctx, collectOptions(hangHome, hang.srv.URL, hang.srv.URL)))

	// 패닉 경로.
	add("패닉", safeProbe(context.Background(), panicAdapter{},
		probeEnv{home: hangHome, client: newHTTPClient(), now: fixedNow}))

	// --- 전제 확인: 이 단언이 공허하지 않은가 -------------------------------

	if len(bag) < len(paths) {
		t.Fatalf("자루가 너무 작다(%d) — 경로를 타지 못했다", len(bag))
	}
	// 스캐너가 실제로 값을 훑고 있는지. 실패 사유 문구가 하나도 없으면 위 루프가 조용히
	// 무의미해진 것이다.
	joined := strings.Join(bag, "\n")
	for _, marker := range []string{
		"credential_missing", "credential_malformed", "token_expired",
		"upstream_status", "network_error", "response_unrecognized", "internal_error",
		"available", "five_hour", "primary",
	} {
		if !strings.Contains(joined, marker) {
			t.Errorf("기대한 흔적 %q 가 없다 — 그 경로를 타지 못했을 수 있다", marker)
		}
	}
	// 리플렉션 스캐너가 비공개 필드까지 들여다보는지 확인한다. 이 단언이 없으면
	// "Result 에 토큰이 없다" 가 "스캐너가 못 본다" 와 구분되지 않는다.
	raw := claudeCredential{token: newToken(claudeCanary), plan: "max"}
	found := false
	for _, s := range allStrings(raw) {
		if s == claudeCanary {
			found = true
		}
	}
	if !found {
		t.Fatal("스캐너가 Token 의 비공개 필드를 못 본다 — 이 테스트는 아무것도 증명하지 못한다")
	}

	// --- 본 단언 ----------------------------------------------------------

	forbidden := append([]string{claudeCanary, codexCanary, accountCanary,
		"Bearer ", "refresh-token-value", "id-token-value"}, homes...)
	assertNoSecret(t, "모든 경로의 결과·직렬화·오류", bag, forbidden...)
}
