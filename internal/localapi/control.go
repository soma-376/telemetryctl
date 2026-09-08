package localapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
)

const ShutdownPath = "/v1/control/shutdown"

func EnsureControlToken() (string, error) {
	token, found, err := credential.Get(credential.AccountLocalControl)
	if err != nil {
		return "", err
	}
	if found && token != "" {
		return token, nil
	}
	token, err = receiver.NewToken()
	if err != nil {
		return "", err
	}
	return token, credential.Set(credential.AccountLocalControl, token)
}

// NewControlHandler는 ingest와 다른 토큰 및 대상 인스턴스 좌표를 요구한다.
func NewControlHandler(token, dataDir, startedAt string, pid int, stop func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ShutdownPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if token == "" || stop == nil || r.Header.Get(receiver.LocalHeader) != receiver.LocalHeaderValue ||
			r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.RawQuery != "" || r.Header.Get("X-Pulsemetry-PID") != strconv.Itoa(pid) ||
			r.Header.Get("X-Pulsemetry-Started-At") != startedAt ||
			r.Header.Get("X-Pulsemetry-Data-Dir") != filepath.Clean(dataDir) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		stop()
	})
}

// StopDaemon은 PID를 kill하지 않는다. 인증된 정상 종료 후 runtime 제거까지 기다린다.
func StopDaemon(ctx context.Context, dataDir string) error {
	path := runtimeinfo.PathIn(dataDir)
	st, err := runtimeinfo.Load(path)
	if err != nil {
		return err
	}
	if !st.Found || st.Stale {
		return nil
	}
	i := st.Info
	u, err := url.Parse(i.Endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "localhost" || u.Port() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		filepath.Clean(i.DataDir) != filepath.Clean(dataDir) {
		return errors.New("종료 대상의 runtime 좌표가 유효하지 않다")
	}
	token, found, err := credential.Get(credential.AccountLocalControl)
	if err != nil || !found {
		return errors.New("종료 제어 토큰이 없다. 데몬을 수동 종료한 뒤 다시 실행하라")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(i.Endpoint, "/")+ShutdownPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(receiver.LocalHeader, receiver.LocalHeaderValue)
	req.Header.Set("X-Pulsemetry-PID", strconv.Itoa(i.PID))
	req.Header.Set("X-Pulsemetry-Started-At", i.StartedAt)
	req.Header.Set("X-Pulsemetry-Data-Dir", filepath.Clean(dataDir))
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("데몬 종료 요청 실패. 수동 종료 후 재시도하라")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("데몬 종료 요청 거부 (%d): 수동 종료 후 재시도하라", resp.StatusCode)
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		next, found, err := runtimeinfo.Read(path)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if next.PID != i.PID || next.StartedAt != i.StartedAt {
			return errors.New("종료 중 다른 데몬이 기동했다")
		}
		select {
		case <-ctx.Done():
			return errors.New("데몬 종료 확인 시간 초과: 데이터는 삭제하지 않는다")
		case <-tick.C:
		}
	}
}
