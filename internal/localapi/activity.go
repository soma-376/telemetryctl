package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/dashboard/activity"
)

const ActivityPath = "/v1/activity"

// WithActivity는 기존 트레이·훅 핸들러에 읽기 전용 Activity 경로를 더한다.
func WithActivity(next http.Handler, source *dashboard.Service) http.Handler {
	builder := activity.NewBuilder(source)
	mux := http.NewServeMux()
	mux.Handle("/", next)
	mux.HandleFunc("GET "+ActivityPath, func(w http.ResponseWriter, r *http.Request) {
		var q activity.Query
		if err := json.Unmarshal([]byte(r.URL.Query().Get("q")), &q); err != nil || q.Since < 0 || q.Until < 0 || (q.Until > 0 && q.Since >= q.Until) {
			http.Error(w, "invalid activity query", http.StatusBadRequest)
			return
		}
		page, err := builder.List(r.Context(), q)
		if err != nil {
			http.Error(w, "activity unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, page)
	})
	mux.HandleFunc("GET "+ActivityPath+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid session id", http.StatusBadRequest)
			return
		}
		out, err := builder.Session(r.Context(), id)
		if err != nil {
			http.Error(w, "session unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, out)
	})
	return mux
}

func (c *Client) Activity(ctx context.Context, q activity.Query) (activity.Page, error) {
	data, err := json.Marshal(q)
	if err != nil {
		return activity.Page{}, err
	}
	var out activity.Page
	err = c.readActivity(ctx, ActivityPath+"?"+url.Values{"q": {string(data)}}.Encode(), &out)
	return out, err
}

func (c *Client) ActivitySession(ctx context.Context, id int64) (activity.Detail, error) {
	var out activity.Detail
	err := c.readActivity(ctx, ActivityPath+"/"+strconv.FormatInt(id, 10), &out)
	return out, err
}

func (c *Client) readActivity(ctx context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, path)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("localapi: 활동 조회: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("localapi: 활동 조회 실패 (%d)", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
