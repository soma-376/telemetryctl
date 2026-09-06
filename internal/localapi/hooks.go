package localapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	SessionStartPath = "/v1/hooks/session-start"
	SessionEndPath   = "/v1/hooks/session-end"
	hookTimeout      = 750 * time.Millisecond
)

type LifecycleEvent struct {
	Vendor    string `json:"vendor"`
	SessionID string `json:"session_id"`
	Source    string `json:"source,omitempty"`
	End       bool   `json:"-"`
}

func (e LifecycleEvent) Validate() error {
	if e.Vendor != "codex" {
		return errors.New("hook vendor는 codex여야 함")
	}
	if e.SessionID == "" {
		return errors.New("hook session_id는 필수")
	}
	return nil
}

type HookSink interface {
	SubmitLifecycle(context.Context, LifecycleEvent) error
}

func (c *Client) SubmitLifecycle(ctx context.Context, event LifecycleEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	path := SessionStartPath
	if event.End {
		path = SessionEndPath
	}
	req, err := c.newRequest(callCtx, http.MethodPost, path)
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
