package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/your-org/pulsemetry/internal/policy"
)

const PolicyPath = "/v1/policy"

// PolicySource는 외부 조회 없이 데몬이 보관한 결과만 돌려준다.
type PolicySource interface {
	Snapshot() policy.Snapshot
}

// WithPolicy는 기존 로컬 핸들러에 정책 상태 조회를 더한다.
func WithPolicy(next http.Handler, source PolicySource) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", next)
	mux.HandleFunc(PolicyPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, source.Snapshot())
	})
	return mux
}

// Policy는 데몬의 마지막 확인 결과를 읽는다. 외부 확인은 데몬 주기 작업의 책임이다.
func (c *Client) Policy(ctx context.Context) (policy.Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, PolicyPath)
	if err != nil {
		return policy.Snapshot{}, err
	}
	// 로컬 인증을 붙인 요청을 리다이렉트 대상에 보내지 않는다.
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return policy.Snapshot{}, fmt.Errorf("localapi: 정책 상태 조회: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return policy.Snapshot{}, fmt.Errorf("localapi: 정책 상태 조회 실패 (%d)", resp.StatusCode)
	}
	const maxSnapshotBytes = 64 << 10
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSnapshotBytes+1))
	if err != nil || len(body) > maxSnapshotBytes {
		return policy.Snapshot{}, errors.New("localapi: 정책 상태 응답 읽기 실패")
	}
	var snapshot policy.Snapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return policy.Snapshot{}, errors.New("localapi: 정책 상태 응답 형식 오류")
	}
	if snapshot.AppliedRevision < 1 || snapshot.Status == "" {
		return policy.Snapshot{}, errors.New("localapi: 적용 정책 정보 누락")
	}
	return snapshot, nil
}
