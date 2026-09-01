package tray

// 스냅샷을 받아 갱신 주기 동안 들고 있는다. **GUI 쪽 코드다.**
//
// 갱신 실패는 화면이 사라질 이유가 아니라 상태다. 트레이에서 숫자가 사라지는 것은
// "값을 못 읽었다" 가 아니라 "활동이 없다" 로 읽히고, 그 오해는 사용자가 도구를 끄게 만든다.
// 그래서 실패하면 직전 정상 스냅샷을 그대로 두고 Stale 만 세운다.

import (
	"context"
	"sync"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
)

// Source 는 스냅샷의 출처다.
//
// 운영 구현은 데몬의 로컬 API를 호출하는 localapi.Client다.
type Source interface {
	Snapshot(ctx context.Context, q Query) (Snapshot, error)
	// RefreshAuto 는 사람이 누르지 않은 갱신이다. 데몬이 자동 등급 쿨다운으로 억제한다 (ADR 0014).
	RefreshAuto(ctx context.Context, q Query) (Snapshot, error)
	// RefreshManual 은 사용자가 새로고침을 누른 것이다. 수동 등급이라 사실상 항상 다시 조회한다.
	RefreshManual(ctx context.Context, q Query) (Snapshot, error)
}

// Cache 는 스냅샷을 받아 마지막 정상값을 들고 있는다. 동시 사용에 안전하다.
//
// 앱당 하나여야 한다 — 호출마다 새로 만들면 마지막 정상 스냅샷이 매번 사라져
// 실패가 곧 빈 화면이 된다.
type Cache struct {
	mu sync.Mutex

	src      Source
	interval time.Duration

	last    Snapshot
	hasGood bool
	// lastQuery 가 바뀌면 주기와 무관하게 다시 만든다. 시간대가 다른 스냅샷을 캐시라고
	// 돌려주면 화면이 남의 날짜를 그린다.
	lastQuery Query
	// checkedAt 은 마지막 **시도** 시각이다. 실패해도 갱신되므로 실패가 매 호출마다
	// 재시도로 번지지 않는다.
	checkedAt time.Time
	hasTried  bool

	now func() time.Time
}

func New(src Source) *Cache {
	return &Cache{src: src, interval: DefaultInterval, now: time.Now}
}

// Current 는 갱신 주기를 지키는 조회다. 트레이는 메뉴를 열 때마다 불리는 화면이라,
// 주기 안이고 조건이 같으면 직전 스냅샷을 그대로 준다.
func (c *Cache) Current(ctx context.Context, q Query) (Snapshot, error) {
	if _, err := dashboard.LoadLocation(q.TZ); err != nil {
		// 시간대 오타는 갱신 실패가 아니라 호출자 버그다. 마지막 정상값으로 덮지 않는다.
		return Snapshot{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasTried && c.lastQuery == q && c.now().Sub(c.checkedAt) < c.interval {
		return c.last, nil
	}
	return c.rebuild(ctx, q), nil
}

// RefreshAuto 는 트레이 창이 열렸을 때 부른다. 갱신 주기를 보지 않고 데몬까지 간다 —
// 창을 여는 것은 명시적인 사용자 동작이라 60초 캐시로 막을 이유가 없고, 벤더를 두드릴지는
// 데몬이 자동 등급 쿨다운으로 판단한다 (ADR 0014).
func (c *Cache) RefreshAuto(ctx context.Context, q Query) (Snapshot, error) {
	return c.command(ctx, q, c.src.RefreshAuto)
}

// RefreshManual 은 벤더 한도를 다시 조회하게 한 뒤 갱신된 스냅샷을 받는다.
// 트레이의 "새로고침" 버튼이 부른다.
func (c *Cache) RefreshManual(ctx context.Context, q Query) (Snapshot, error) {
	return c.command(ctx, q, c.src.RefreshManual)
}

// command 는 두 갱신의 공통 몸통이다. 둘은 데몬에 어느 등급으로 명령하는지만 다르고,
// 캐시를 대하는 방식은 같다.
//
// 갱신 명령이 실패해도 조회는 그대로 한다. 데몬이 꺼져 있다고 과거 스냅샷까지 못 보게 할
// 이유가 없다 — 그 사실은 Stale 과 monitoring.state 가 말한다.
func (c *Cache) command(ctx context.Context, q Query, call func(context.Context, Query) (Snapshot, error)) (Snapshot, error) {
	if _, err := dashboard.LoadLocation(q.TZ); err != nil {
		return Snapshot{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out, err := call(ctx, q)
	return c.remember(q, out, err), nil
}

// rebuild 는 실제 갱신이다. error 를 돌려주지 않는다 — 갱신 실패는 상태이지 에러가 아니다.
// 한 번도 성공한 적이 없으면 빈 모양을 Stale 로 준다. 호출자가 mu 를 쥐고 있어야 한다.
func (c *Cache) rebuild(ctx context.Context, q Query) Snapshot {
	out, err := c.src.Snapshot(ctx, q)
	return c.remember(q, out, err)
}

// remember 는 한 번의 데몬 응답을 캐시에 반영한다. 호출자가 mu 를 쥐고 있어야 한다.
func (c *Cache) remember(q Query, out Snapshot, err error) Snapshot {
	now := c.now()
	c.checkedAt = now
	c.hasTried = true
	c.lastQuery = q

	if err != nil {
		out = c.last
		if !c.hasGood {
			out = emptySnapshot(q.TZ)
		}
		out.CheckedAt = now.Unix()
		out.Stale = true
		out.StaleReason = StaleLocalQuery
		c.last = out
		return out
	}

	out.RefreshedAt = now.Unix()
	out.CheckedAt = now.Unix()

	c.last = out
	c.hasGood = true
	return out
}
