package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/dashboard/home"
)

const HomePath = "/v1/home"

// WithHome은 수신기의 기존 로컬 인증 안에 읽기 전용 홈 경로를 등록한다.
func WithHome(next http.Handler, source *dashboard.Service) http.Handler {
	builder := home.NewBuilder(source)
	mux := http.NewServeMux()
	mux.Handle("/", next)
	mux.HandleFunc(HomePath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		var q home.Query
		if err := json.Unmarshal([]byte(r.URL.Query().Get("q")), &q); err != nil || home.ValidateQuery(q) != nil {
			http.Error(w, "invalid home query", http.StatusBadRequest)
			return
		}
		out, err := builder.Snapshot(r.Context(), q)
		if err != nil {
			http.Error(w, "home unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, out)
	})
	return mux
}

func (c *Client) Home(ctx context.Context, q home.Query) (home.Snapshot, error) {
	data, err := json.Marshal(q)
	if err != nil {
		return home.Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, HomePath+"?"+url.Values{"q": {string(data)}}.Encode())
	if err != nil {
		return home.Snapshot{}, err
	}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return home.Snapshot{}, fmt.Errorf("localapi: 홈 조회: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return home.Snapshot{}, fmt.Errorf("localapi: 홈 조회 실패 (%d)", resp.StatusCode)
	}
	var out home.Snapshot
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}
