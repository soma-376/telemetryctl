package daemon

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	claudeTitleQueueSize = 128
	claudeTitleTimeout   = 5 * time.Second
	// claudeTitleRetry 는 못 찾은 세션을 다시 보기까지의 최소 간격이다.
	//
	// 제목은 첫 프롬프트 직후에 만들어지므로, 세션이 열리자마자 넣은 요청은 대개 한 번은
	// 빈손으로 돌아온다. 재시도가 필요하되 매 스냅샷마다 파일을 다시 열 이유는 없다.
	claudeTitleRetry = time.Minute
)

type claudeTitleRequest struct {
	key   string
	ended bool
}

type claudeTitleStore interface {
	SetClaudeTitle(context.Context, string, string) error
}

// claudeTitleRefresher 는 트랜스크립트 읽기를 수집 파이프라인 밖에서 직렬로 처리한다.
// 파일 IO 가 수신 경로나 조회 경로에 들어가지 않게 하는 것이 이 타입의 목적이다.
//
// 구조는 codexTitleRefresher 와 같다. 두 벤더가 제목을 다른 곳에서 얻지만 데몬이 그것을
// 다루는 방식은 같아야, 나중에 읽는 사람이 한쪽만 보고도 다른 쪽을 안다.
type claudeTitleRefresher struct {
	root  string
	store claudeTitleStore
	log   *log.Logger
	now   func() time.Time
	// read 는 트랜스크립트 읽기다. 필드로 둔 것은 테스트가 파일 없이 돌기 위해서이고,
	// codexTitleRefresher 가 reader 를 주입받는 것과 같은 이유다.
	read func(root, sessionKey string) (string, bool)

	ctx    context.Context
	cancel context.CancelFunc
	queue  chan claudeTitleRequest
	done   chan struct{}
	once   sync.Once
}

// newClaudeTitleRefresher 는 root 가 비면 nil 을 돌려준다. 홈을 못 찾는 환경에서는
// 이 기능이 통째로 없는 것이고, 호출부는 nil 을 그대로 들고 다녀도 된다.
func newClaudeTitleRefresher(parent context.Context, root string, store claudeTitleStore, logger *log.Logger, now func() time.Time) *claudeTitleRefresher {
	if root == "" || store == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	ctx, cancel := context.WithCancel(parent)
	r := &claudeTitleRefresher{
		root: root, store: store, log: logger, now: now, read: readClaudeAITitle,
		ctx: ctx, cancel: cancel, queue: make(chan claudeTitleRequest, claudeTitleQueueSize), done: make(chan struct{}),
	}
	go r.run()
	return r
}

// Enqueue 는 큐가 찼을 때 수집을 막지 않는다. 다음 세션 스냅샷이 다시 기회를 준다.
//
// ended 는 이 스냅샷에서 세션이 마감됐다는 뜻이다. 마감된 세션은 다시 스냅샷에 실리지
// 않으므로 "다음 기회" 가 없다 — 쿨다운을 건너뛰고 마지막으로 한 번 더 본다.
func (r *claudeTitleRefresher) Enqueue(sessionKey string, ended bool) {
	if r == nil || sessionKey == "" {
		return
	}
	select {
	case r.queue <- claudeTitleRequest{key: sessionKey, ended: ended}:
	default:
	}
}

func (r *claudeTitleRefresher) run() {
	defer close(r.done)
	completed := make(map[string]struct{})
	retryAfter := make(map[string]time.Time)

	for {
		select {
		case <-r.ctx.Done():
			return
		case req := <-r.queue:
			key := req.key
			if _, ok := completed[key]; ok {
				continue
			}
			if until := retryAfter[key]; !req.ended && !until.IsZero() && r.now().Before(until) {
				continue
			}
			if r.refresh(key) {
				completed[key] = struct{}{}
				delete(retryAfter, key)
				continue
			}
			retryAfter[key] = r.now().Add(claudeTitleRetry)
		}
	}
}

func (r *claudeTitleRefresher) refresh(sessionKey string) bool {
	title, ok := r.read(r.root, sessionKey)
	if !ok {
		// 아직 제목이 안 만들어졌거나 파일이 없다. 흔한 정상 상태라 로그를 남기지 않는다.
		return false
	}
	ctx, cancel := context.WithTimeout(r.ctx, claudeTitleTimeout)
	defer cancel()
	if err := r.store.SetClaudeTitle(ctx, sessionKey, title); err != nil {
		r.log.Printf("경고: Claude 세션 제목 저장 실패 (%s): %v", sessionKey, err)
		return false
	}
	return true
}

// Close 는 DB 를 닫기 전에 워커를 멈춘다.
func (r *claudeTitleRefresher) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.cancel()
		<-r.done
	})
}
