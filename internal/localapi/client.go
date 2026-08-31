package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/dashboard/tray"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
)

const (
	// snapshotTimeout 은 로컬 SQLite 조회 왕복이다. 짧아야 한다 — 트레이는 메뉴를 열 때마다 부른다.
	snapshotTimeout = 3 * time.Second
	// refreshTimeout 은 벤더 API 를 두드리고 오는 시간까지 포함한다.
	refreshTimeout = 35 * time.Second
)

// ErrDaemonNotRunning 은 runtime.json 이 없다는 뜻이다 — 데몬을 켠 적이 없거나 꺼져 있다.
// 호출자가 오류와 구분해야 한다. Cache 는 이때 마지막 정상 스냅샷을 Stale 로 유지한다.
var ErrDaemonNotRunning = errors.New("localapi: 데몬이 실행 중이 아니다")

// Client 는 GUI 가 데몬을 부르는 쪽이다. dataDir 아래 runtime.json 으로 주소를 찾는다.
// tray.Source 를 만족한다.
type Client struct {
	dataDir string
	http    *http.Client
}

func NewClient(dataDir string) *Client {
	return &Client{dataDir: dataDir, http: &http.Client{Timeout: refreshTimeout}}
}

// Snapshot 은 데몬이 만든 트레이 스냅샷 한 장을 받는다.
func (c *Client) Snapshot(ctx context.Context, q tray.Query) (tray.Snapshot, error) {
	return c.requestSnapshot(ctx, snapshotTimeout, http.MethodGet, TrayPath, q)
}

// Refresh 는 데몬에 수동 한도 갱신을 명령하고 갱신된 트레이 스냅샷을 받는다.
func (c *Client) Refresh(ctx context.Context, q tray.Query) (tray.Snapshot, error) {
	return c.requestSnapshot(ctx, refreshTimeout, http.MethodPost, TrayRefreshPath, q)
}

func (c *Client) requestSnapshot(ctx context.Context, timeout time.Duration, method, path string, q tray.Query) (tray.Snapshot, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	params := url.Values{}
	params.Set(paramTZ, q.TZ)
	params.Set(paramRecentLimit, strconv.Itoa(q.RecentLimit))

	req, err := c.newRequest(callCtx, method, path+"?"+params.Encode())
	if err != nil {
		return tray.Snapshot{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return tray.Snapshot{}, fmt.Errorf("localapi: 트레이 요청: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return tray.Snapshot{}, fmt.Errorf("localapi: 트레이 요청 실패 (%d)", resp.StatusCode)
	}
	var snap tray.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return tray.Snapshot{}, fmt.Errorf("localapi: 트레이 스냅샷 디코드: %w", err)
	}
	return snap, nil
}

// newRequest 는 주소를 찾고 인증을 붙인다.
//
// endpoint 가 loopback HTTP 인지 반드시 확인한다. runtime.json 은 평범한 파일이라
// 누가 고쳐 두면 우리가 토큰을 바깥 호스트로 보내게 된다.
//
// TODO(보안): 지금은 ingest 토큰을 그대로 쓴다. 그 토큰은 벤더 설정 파일에 평문으로
// 들어가므로(AGENTS.md), 그 파일을 읽을 수 있으면 이 조회 API 도 부를 수 있다. 조회는
// 세션 이력과 제목을 돌려주므로 ingest(쓰기 전용)보다 노출이 넓다. 키링에 조회 전용
// 계정을 따로 두는 것이 맞다.
func (c *Client) newRequest(ctx context.Context, method, path string) (*http.Request, error) {
	info, found, err := runtimeinfo.Read(runtimeinfo.PathIn(c.dataDir))
	if err != nil || !found {
		return nil, ErrDaemonNotRunning
	}
	endpoint, err := url.Parse(info.Endpoint)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "localhost" {
		return nil, errors.New("localapi: 데몬 endpoint가 loopback HTTP가 아님")
	}
	token, found, err := credential.Get(credential.AccountLocalIngest)
	if err != nil {
		return nil, fmt.Errorf("localapi: 로컬 제어 토큰 조회: %w", err)
	}
	if !found || token == "" {
		return nil, errors.New("localapi: 로컬 제어 토큰이 없음")
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint.String()+path, nil)
	if err != nil {
		return nil, fmt.Errorf("localapi: 요청 생성: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(receiver.LocalHeader, receiver.LocalHeaderValue)
	return req, nil
}
