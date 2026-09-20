package localapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/dashboard/home"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/zalando/go-keyring"
)

func TestHomeClientAndRoute(t *testing.T) {
	keyring.MockInit()
	if err := credential.Set(credential.AccountLocalIngest, "home-test"); err != nil {
		t.Fatal(err)
	}
	svc := dashboard.NewService(filepath.Join(t.TempDir(), "missing.db"))
	t.Cleanup(func() { _ = svc.Stop() })
	h := WithHome(http.NotFoundHandler(), svc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != HomePath || r.Header.Get("Authorization") != "Bearer home-test" || r.Header.Get(receiver.LocalHeader) != receiver.LocalHeaderValue {
			t.Error("인증 또는 경로 오류")
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	client := updatesTestClient(t, srv.URL)
	q := home.Query{TZ: "Asia/Seoul", Start: "2026-09-19", End: "2026-09-20"}
	out, err := client.Home(context.Background(), q)
	if err != nil || out.Usage.TZ != q.TZ || out.Usage.Date != q.Start || out.EndDate != q.End || len(out.Usage.Windows) != 8 {
		t.Fatalf("왕복 실패: %+v %v", out, err)
	}
	for _, raw := range []string{`{`, `{}`, `{"start":"2026-02-30","end":"2026-03-01"}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", HomePath+"?"+url.Values{"q": {raw}}.Encode(), nil))
		if w.Code != 400 {
			t.Fatalf("잘못된 조건: %s %d", raw, w.Code)
		}
	}
	w := httptest.NewRecorder()
	data, _ := json.Marshal(q)
	h.ServeHTTP(w, httptest.NewRequest("POST", HomePath+"?"+url.Values{"q": {string(data)}}.Encode(), nil))
	if w.Code != 405 {
		t.Fatalf("POST 허용: %d", w.Code)
	}
}

func TestHomeClientRejectsRedirect(t *testing.T) {
	keyring.MockInit()
	if err := credential.Set(credential.AccountLocalIngest, "home-test"); err != nil {
		t.Fatal(err)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, err := updatesTestClient(t, srv.URL).Home(context.Background(), home.Query{})
	if err == nil || hits != 1 {
		t.Fatalf("리다이렉트: %d %v", hits, err)
	}
}
