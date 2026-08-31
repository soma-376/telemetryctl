package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Options 는 Client의 프로세스 구성을 정한다. 영값은 운영 기본값이다.
type Options struct {
	Command []string
}

// Client 는 App Server 프로세스 하나를 지연 시작해 재사용한다.
type Client struct {
	mu      sync.Mutex
	command []string
	process *process
	nextID  int64
	closed  bool
}

func New(opts Options) *Client { return &Client{command: append([]string(nil), opts.Command...)} }

func (c *Client) RateLimits(ctx context.Context) (RateLimitSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureStarted(ctx); err != nil {
		return RateLimitSnapshot{}, err
	}
	var result getRateLimitsResponse
	if err := c.call(ctx, "account/rateLimits/read", nil, &result); err != nil {
		c.process.kill()
		c.process = nil
		return RateLimitSnapshot{}, err
	}
	return result.RateLimits, nil
}

func (c *Client) ensureStarted(ctx context.Context) error {
	if c.closed {
		return ErrClosed
	}
	if c.process != nil {
		return nil
	}
	p, err := startProcess(c.command)
	if err != nil {
		return err
	}
	c.process = p
	params := initializeParams{ClientInfo: clientInfo{Name: "pulsemetry", Version: "1"}, Capabilities: map[string]any{}}
	if err := c.call(ctx, "initialize", params, nil); err != nil {
		_ = p.close()
		c.process = nil
		return err
	}
	if err := c.write(notification{Method: "initialized"}); err != nil {
		p.kill()
		c.process = nil
		return err
	}
	return nil
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	c.nextID++
	id := c.nextID
	if err := c.write(request{ID: id, Method: method, Params: params}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-c.process.lines:
			if !ok {
				return fmt.Errorf("%w: stdout가 닫힘", ErrUnavailable)
			}
			resp, err := decodeResponse(line)
			if err != nil {
				// 알림에는 id가 없다. 요청 응답을 기다리는 동안에는 조용히 건너뛴다.
				var note notification
				if json.Unmarshal(line, &note) == nil && note.Method != "" {
					continue
				}
				return err
			}
			if *resp.ID != id {
				continue
			}
			if out == nil || len(resp.Result) == 0 {
				return nil
			}
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("%w: 결과를 읽지 못함", ErrProtocol)
			}
			return nil
		}
	}
}

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%w: 요청을 만들지 못함", ErrProtocol)
	}
	b = append(b, '\n')
	if _, err := c.process.stdin.Write(b); err != nil {
		return errors.Join(ErrUnavailable, err)
	}
	return nil
}

// Close 는 App Server를 종료한다. 여러 번 불러도 안전하다.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.process == nil {
		return nil
	}
	err := c.process.close()
	c.process = nil
	return err
}
