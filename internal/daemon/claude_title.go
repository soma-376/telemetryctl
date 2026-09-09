package daemon

import (
	"context"
	"log"
	"time"

	"github.com/your-org/pulsemetry/internal/claudecode"
)

const claudeTitleTimeout = 5 * time.Second

type claudeTitlePolicy struct {
	finalRetryDelays []time.Duration
	read             func(string, string) (string, bool)
}

type claudeTitleStore interface {
	SetClaudeTitle(context.Context, string, string) error
}

func newClaudeTitleRefresher(parent context.Context, root string, store claudeTitleStore, logger *log.Logger, now func() time.Time) *titleRefresher {
	return newClaudeTitleRefresherWithPolicy(parent, root, store, logger, now, claudeTitlePolicy{})
}

// Claude의 ai-title은 저장에 성공하면 재조회하지 않는다 (ADR 0018).
func newClaudeTitleRefresherWithPolicy(parent context.Context, root string, store claudeTitleStore, logger *log.Logger, now func() time.Time, policy claudeTitlePolicy) *titleRefresher {
	if root == "" || store == nil {
		return nil
	}
	if policy.read == nil {
		policy.read = claudecode.ReadAITitle
	}
	return newTitleRefresher(parent, func(ctx context.Context, key string) bool {
		title, ok := policy.read(root, key)
		if !ok {
			return false
		}
		ctx, cancel := context.WithTimeout(ctx, claudeTitleTimeout)
		defer cancel()
		if err := store.SetClaudeTitle(ctx, key, title); err != nil {
			logger.Printf("경고: Claude 세션 제목 저장 실패 (%s): %v", key, err)
			return false
		}
		return true
	}, titlePolicy{now: now, finalRetryDelays: policy.finalRetryDelays, stopAfterSuccess: true})
}
