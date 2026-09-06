package localapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/vendor"
)

const (
	SessionStartPath = receiver.HookSessionStartPath
	SessionEndPath   = receiver.HookSessionEndPath
	hookTimeout      = 750 * time.Millisecond

	// paramVendor 는 훅을 보낸 벤더다. 본문이 아니라 쿼리로 받는 이유는 훅 페이로드가
	// 벤더 소유라 우리 필드를 더할 수 없기 때문이다 — URL 은 우리가 설정 파일에 쓴다.
	paramVendor = "vendor"
)

// HookPayload 는 벤더 lifecycle 훅 JSON 중 우리가 읽는 것이다. 두 벤더가 같은 필드
// 이름을 쓴다 — Codex 의 훅 엔진이 Claude Code 계약을 복제했다.
//
// 모르는 필드는 무시한다. 벤더가 필드를 늘리는 것은 정상이고, 여기서 400 을 주면
// 그날로 세션 마감이 멈춘다.
type HookPayload struct {
	SessionID string `json:"session_id"`
	// Source 는 SessionStart 에만 있다. startup·resume·clear·compact.
	Source string `json:"source"`
}

// LifecycleEvent 는 훅 하나가 말하는 세션 상태 변화다.
type LifecycleEvent struct {
	Vendor    string
	SessionID string
	Source    string
	// End 는 본문의 hook_event_name 이 아니라 경로가 정한다.
	End bool
}

func (e LifecycleEvent) Validate() error {
	if _, ok := vendor.Normalize(e.Vendor); !ok {
		return fmt.Errorf("hook vendor를 알 수 없음: %q", e.Vendor)
	}
	if e.SessionID == "" {
		return errors.New("hook session_id는 필수")
	}
	return nil
}

type HookSink interface {
	SubmitLifecycle(context.Context, LifecycleEvent) error
}

// SubmitLifecycle 은 command hook 브리지가 쓰는 클라이언트 쪽이다. http 훅을 지원하지
// 않는 벤더(Codex)만 이 경로를 탄다.
func (c *Client) SubmitLifecycle(ctx context.Context, event LifecycleEvent, body []byte) error {
	if err := event.Validate(); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()

	path := SessionStartPath
	if event.End {
		path = SessionEndPath
	}
	req, err := c.newRequest(callCtx, http.MethodPost, path+"?"+url.Values{
		paramVendor: {event.Vendor},
	}.Encode())
	if err != nil {
		return err
	}
	req.Body = http.NoBody
	if len(body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("localapi: hook 요청: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("localapi: hook 요청 실패 (%d)", resp.StatusCode)
	}
	return nil
}

// DecodeHook 은 훅 본문과 벤더를 LifecycleEvent 로 옮긴다. 서버와 브리지가 같은 규칙을
// 쓰도록 여기 둔다.
func DecodeHook(r io.Reader, vendorID string, end bool) (LifecycleEvent, error) {
	var p HookPayload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return LifecycleEvent{}, fmt.Errorf("hook 본문 파싱: %w", err)
	}
	// 별칭이 와도 정식 ID 로 옮겨 담는다. 원문을 그대로 두면 그 값이 sessions.vendor_id
	// 로 저장되어, OTLP 가 정규화해 만든 같은 세션과 다른 행으로 갈린다.
	canonical, ok := vendor.Normalize(vendorID)
	if ok {
		vendorID = string(canonical)
	}
	event := LifecycleEvent{
		Vendor: vendorID, SessionID: p.SessionID, Source: p.Source, End: end,
	}
	return event, event.Validate()
}
