package daemon

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/your-org/pulsemetry/internal/codexapp"
)

const codexTitleTimeout = 10 * time.Second

type codexTitleStore interface {
	SetCodexTitle(context.Context, string, string) error
}

func newCodexTitleRefresher(parent context.Context, reader codexapp.ThreadReader, store codexTitleStore, logger *log.Logger) *titleRefresher {
	return newCodexTitleRefresherWithPolicy(parent, reader, store, logger, titlePolicy{})
}

// Codex는 진행 중 이름 변경을 폴링하고 종료 시 최종 조회한다 (ADR 0017).
func newCodexTitleRefresherWithPolicy(parent context.Context, reader codexapp.ThreadReader, store codexTitleStore, logger *log.Logger, policy titlePolicy) *titleRefresher {
	return newTitleRefresher(parent, func(ctx context.Context, key string) bool {
		ctx, cancel := context.WithTimeout(ctx, codexTitleTimeout)
		defer cancel()
		title, err := reader.ThreadName(ctx, key)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, codexapp.ErrThreadIDRequired) {
				logger.Printf("경고: Codex 세션 제목 조회 실패 (%s): %v", key, err)
			}
			return false
		}
		if title == "" {
			return false
		}
		if err := store.SetCodexTitle(ctx, key, title); err != nil {
			logger.Printf("경고: Codex 세션 제목 저장 실패 (%s): %v", key, err)
			return false
		}
		return true
	}, policy)
}
