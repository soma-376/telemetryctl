package daemon

import (
	"context"
	"errors"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/forward"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/rollup"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// pipeline 은 이 단계의 **직렬화 지점**이다.
//
// # 왜 소유자 고루틴 하나인가
//
// session.Assembler 와 rollup.Aggregator 는 둘 다 동시 사용에 안전하지 않다고 각
// 패키지가 명시했고, 수신기 워커는 2개다. 게다가 두 집계기는 같은 이벤트를 **같은
// 순서로** 봐야 총량이 일치한다 — 한쪽만 재시작하거나 한쪽에만 필터를 걸면 cumulative
// 기준점이 어긋나 sessions 합계와 rollup_hourly 합계가 갈린다
// (internal/session/agreement_test.go 가 그 회귀를 지킨다).
//
// 뮤텍스로도 같은 결과를 낼 수 있지만 소유자를 고루틴 하나로 못박으면 "락을 안 잡고
// 집계기를 만지는 코드" 가 나중에 끼어들 자리 자체가 없어진다. 두 집계기와 미저장
// 배치는 전부 run 고루틴만 만지고, 바깥에서 들어오는 것은 cmds 채널뿐이다.
//
// # 상위 전달은 이 지점 밖이다
//
// Consume 은 받은 원본 바이트를 먼저 포워더 큐에 넣고 나서 집계를 요청한다.
// forward.Enqueue 는 절대 블로킹하지 않고 동시 호출에 안전하므로, 로컬 집계나 SQLite
// 쓰기가 느려도 회사 Collector 로 가는 길이 막히지 않는다 (§5.4).
// Scrub 은 포워더가 한다 — 여기서는 받은 바이트를 그대로 넘긴다 (ADR 0003).
type pipeline struct {
	db  *store.DB
	fwd *forward.Forwarder
	log *log.Logger
	now func() time.Time

	// batchEvents 는 크기 기준 flush 임계값이다.
	batchEvents int
	// writeTimeout·pruneTimeout 은 SQLite 호출 하나의 상한이다. 여기에 상한이 없으면
	// 잠긴 DB 하나가 종료 전체를 멈춰 세운다.
	writeTimeout time.Duration
	pruneTimeout time.Duration
	// sessionTTL 은 마감된 세션을 조립기 메모리에 남겨 두는 기간이다.
	sessionTTL time.Duration

	cmds chan command
	done chan struct{}
	once sync.Once

	// 아래는 전부 run 고루틴 전용이다. 바깥에서 읽지 않는다.
	asm          *session.Assembler
	agg          *rollup.Aggregator
	dedup        *dedupWindow
	pending      []store.EventRecord
	carryRollups []rollup.Row
	dirty        bool
	// lastRollupMeta 는 meta.last_rollup_at 에 마지막으로 쓴 값이다.
	lastRollupMeta event.UnixSec

	counters counters
}

type cmdKind uint8

const (
	cmdBatch cmdKind = iota
	// cmdFlush 는 시간 기준 배치 flush 다.
	cmdFlush
	// cmdSessions 는 세션 마감 + 스냅샷 저장 + 조립기 prune 이다.
	cmdSessions
	// cmdPrune 은 store 보존 정책 적용이다.
	cmdPrune
	// cmdFinal 은 종료 신호다. 채널을 close 하지 않고 센티널을 쓰는 이유는
	// 닫힌 채널에 보내면 select 안이라도 panic 이기 때문이다 — 종료와 Consume 이
	// 겹칠 여지를 아예 없앤다.
	cmdFinal
)

type command struct {
	kind  cmdKind
	batch receiver.Batch
}

// counters 는 바깥(종료 요약·후속 status 명령)에서 읽는 집계다. 값은 담지 않고 개수만이다.
type counters struct {
	batches            atomic.Int64
	events             atomic.Int64
	eventsStored       atomic.Int64
	eventsDuplicate    atomic.Int64
	duplicates         atomic.Int64
	contentsStored     atomic.Int64
	contentsDropped    atomic.Int64
	sessionsWritten    atomic.Int64
	sessionsClosed     atomic.Int64
	rollupRows         atomic.Int64
	writeErrors        atomic.Int64
	pruneErrors        atomic.Int64
	forwardDropped     atomic.Int64
	droppedTemporality atomic.Int64
}

// Stats 는 파이프라인 카운터 스냅샷이다.
type Stats struct {
	Batches      int64
	Events       int64
	EventsStored int64
	// EventsDuplicate 는 store 의 UNIQUE 제약이 접은 수다 (창 밖으로 밀려난 중복).
	EventsDuplicate int64
	// Duplicates 는 배선 단계 중복 제거 창이 걸러낸 수다.
	Duplicates      int64
	ContentsStored  int64
	ContentsDropped int64
	SessionsWritten int64
	SessionsClosed  int64
	RollupRows      int64
	WriteErrors     int64
	PruneErrors     int64
	// ForwardDropped 는 포워더 큐가 가득 차 버린 페이로드 수다.
	ForwardDropped int64

	// DroppedTemporality 는 aggregation_temporality 가 UNSPECIFIED 라 폐기한
	// 데이터포인트 수다 (계획서 「반드시 알아야 할 제약」 4).
	//
	// # 왜 세 후보 중 이것인가
	//
	// 같은 사실을 세는 카운터가 셋 있다.
	//
	//	otlpdecode.Rejected.UnspecifiedTemporality — 페이로드 한 건 단위
	//	session.Diag.DiscardedPoints              — 세션 한 건 단위
	//	rollup.Stats.DroppedTemporality           — 집계기 수명 전체 누계
	//
	// 첫째는 이미 갈 곳이 정해져 있다. 수신기가 OTLP PartialSuccess 응답에 그대로
	// 실어 벤더에게 돌려주므로, 여기서 또 누적하면 receiver.Stats.Rejected 와
	// 이중으로 세는 셈이 된다.
	//
	// 둘째는 session.id 를 가진 이벤트만 조립기에 도달하므로 구조적으로 과소 계수다.
	// session.id 없는 메트릭의 UNSPECIFIED 폐기는 여기에 절대 잡히지 않는다.
	//
	// 셋째만이 (a) 모든 이벤트를 보고 (b) Flush 로 초기화되지 않는 프로세스 수명
	// 누계이며 (c) rollup 패키지가 "status 명령과 로그가 얼마나 버렸는지 보려면
	// 버킷보다 오래 살아야 한다"고 그 용도를 명시해 두었다. 게다가 agreement_test 가
	// session 쪽 수와 일치함을 이미 고정하고 있어, 이것을 노출하면 둘째가 세는 것을
	// 잃지 않으면서 더 넓은 범위를 덮는다.
	DroppedTemporality int64
}

func (p *pipeline) Stats() Stats {
	return Stats{
		Batches:            p.counters.batches.Load(),
		Events:             p.counters.events.Load(),
		EventsStored:       p.counters.eventsStored.Load(),
		EventsDuplicate:    p.counters.eventsDuplicate.Load(),
		Duplicates:         p.counters.duplicates.Load(),
		ContentsStored:     p.counters.contentsStored.Load(),
		ContentsDropped:    p.counters.contentsDropped.Load(),
		SessionsWritten:    p.counters.sessionsWritten.Load(),
		SessionsClosed:     p.counters.sessionsClosed.Load(),
		RollupRows:         p.counters.rollupRows.Load(),
		WriteErrors:        p.counters.writeErrors.Load(),
		PruneErrors:        p.counters.pruneErrors.Load(),
		ForwardDropped:     p.counters.forwardDropped.Load(),
		DroppedTemporality: p.counters.droppedTemporality.Load(),
	}
}

func newPipeline(cfg pipelineConfig) *pipeline {
	p := &pipeline{
		db:           cfg.DB,
		fwd:          cfg.Forwarder,
		log:          cfg.Logger,
		now:          cfg.Now,
		batchEvents:  cfg.BatchEvents,
		writeTimeout: cfg.WriteTimeout,
		pruneTimeout: cfg.PruneTimeout,
		sessionTTL:   cfg.SessionTTL,
		cmds:         make(chan command, cfg.QueueSize),
		done:         make(chan struct{}),
		asm:          session.New(),
		agg:          rollup.New(),
		dedup:        newDedupWindow(cfg.DedupCapacity),
	}
	go p.run()
	return p
}

type pipelineConfig struct {
	DB           *store.DB
	Forwarder    *forward.Forwarder
	Logger       *log.Logger
	Now          func() time.Time
	BatchEvents  int
	QueueSize    int
	WriteTimeout time.Duration
	PruneTimeout time.Duration
	SessionTTL   time.Duration
	// DedupCapacity 는 배선 단계 중복 제거 창의 크기다. 0 이면 기본값.
	DedupCapacity int
}

// Consume 은 receiver.Sink 구현이다. 수신기 워커(2개)가 동시에 부른다.
//
// 블로킹해도 되는 자리다 — 여기서 밀리면 수신기 큐가 차고, 큐가 차면 핸들러가
// 200 + PartialSuccess 로 흘려보낸다. 역압이 개발 도구까지 번지지 않고 이 경계에서
// 멈추는 것이 설계된 동작이다 (§5.4).
func (p *pipeline) Consume(ctx context.Context, b receiver.Batch) error {
	if p.fwd != nil && len(b.Body) > 0 {
		if !p.fwd.Enqueue(b.Kind, b.Encoding, b.Body) {
			p.counters.forwardDropped.Add(1)
		}
	}
	if len(b.Result.Events) == 0 {
		return nil
	}
	// 종료 여부를 먼저 본다. select 는 여러 case 가 동시에 준비되면 무작위로 고르므로,
	// done 과 버퍼가 남은 cmds 를 한 select 에 두면 종료된 뒤에도 배치가 채널에 들어가
	// 아무도 꺼내지 않는 채로 사라진다.
	select {
	case <-p.done:
		return errors.New("daemon: 파이프라인이 이미 종료됨")
	default:
	}
	select {
	case p.cmds <- command{kind: cmdBatch, batch: b}:
		return nil
	case <-p.done:
		return errors.New("daemon: 파이프라인이 이미 종료됨")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// submit 은 주기 작업을 소유자 고루틴에 맡긴다. 파이프라인이 이미 끝났으면 조용히 버린다.
func (p *pipeline) submit(kind cmdKind) {
	select {
	case <-p.done:
		return
	default:
	}
	select {
	case p.cmds <- command{kind: kind}:
	case <-p.done:
	}
}

// close 는 종료 센티널을 보내고 최종 flush 가 끝나기를 deadline 까지 기다린다.
// 제한 시간 안에 끝났으면 true 다. 여러 번 불러도 안전하다.
func (p *pipeline) close(deadline time.Time) bool {
	p.once.Do(func() {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case p.cmds <- command{kind: cmdFinal}:
		case <-p.done:
		case <-timer.C:
		}
	})
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-p.done:
		return true
	case <-timer.C:
		return false
	}
}

func (p *pipeline) run() {
	for c := range p.cmds {
		switch c.kind {
		case cmdBatch:
			p.ingest(c.batch)
		case cmdFlush:
			if p.dirty {
				p.flush(nil)
			}
		case cmdSessions:
			p.closeSessions()
		case cmdPrune:
			p.prune()
		case cmdFinal:
			p.finalFlush()
			close(p.done)
			return
		}
	}
}

// ingest 는 배치 하나를 두 집계기와 미저장 이벤트 목록에 반영한다.
func (p *pipeline) ingest(b receiver.Batch) {
	res := b.Result
	if len(res.Events) == 0 {
		return
	}
	p.counters.batches.Add(1)
	p.counters.events.Add(int64(len(res.Events)))

	// Contents·Targets 는 EventIndex 로 이벤트를 가리키는 희소 슬라이스다. 이벤트마다
	// 두 슬라이스를 훑으면 큰 배치에서 O(n²) 가 되므로 인덱스 맵을 배치당 한 번만 만든다.
	contents := make(map[int][]event.Content, len(res.Contents))
	for _, c := range res.Contents {
		if c.EventIndex < 0 || c.EventIndex >= len(res.Events) {
			continue
		}
		contents[c.EventIndex] = append(contents[c.EventIndex], c.Content)
	}
	targets := make(map[int]event.Path, len(res.Targets))
	for _, t := range res.Targets {
		if t.EventIndex < 0 || t.EventIndex >= len(res.Events) {
			continue
		}
		targets[t.EventIndex] = t.Path
	}

	for i := range res.Events {
		e := res.Events[i]
		cs := contents[i]

		// 중복 제거는 세 소비자에게 들어가기 **전에** 한 번만 한다 (dedup.go).
		// 여기서 거르지 않으면 재전송 한 번에 session 은 두 번 세고 rollup 은
		// 자체 창으로 무시해, 두 합계가 갈린다.
		if !p.dedup.add(e.DedupKey()) {
			p.counters.duplicates.Add(1)
			continue
		}

		// 두 집계기는 같은 스트림을 같은 순서로 본다. 한쪽만 걸러내면 cumulative
		// 기준점이 어긋나 sessions 합계와 rollup_hourly 합계가 갈린다.
		// Add 의 반환값으로 분기해서도 안 된다 — session.id 가 없어 조립기가 무시한
		// 이벤트도 롤업과 events 테이블에는 그대로 들어가야 한다.
		p.asm.Add(session.Input{Event: e, Content: pickContent(cs), Target: targets[i]})
		p.agg.Add(e)

		p.pending = append(p.pending, store.EventRecord{Event: e, Contents: cs})
		p.dirty = true
	}
	// agg 는 이 고루틴 전용이라 여기서 읽는 것이 안전하다. 바깥은 원자 카운터만 본다.
	p.counters.droppedTemporality.Store(p.agg.Stats().DroppedTemporality)

	// 크기 기준 flush. 시간 기준(cmdFlush)과 두 축으로 거는 이유는 §5.4 의 뒤집힌
	// 요구 때문이다 — 이벤트마다 트랜잭션을 열면 느려서 수신기 큐가 차고, 반대로
	// 한없이 모으면 크래시 한 번에 잃는 양이 커진다.
	if len(p.pending) >= p.batchEvents {
		p.flush(nil)
	}
}

// contentPriority 는 한 이벤트에 여러 원문이 붙었을 때 조립기에 넘길 한 건의 우선순위다.
//
// session.Input.Content 는 한 건뿐인데 tool_result 로그는 tool_input 과 tool_result 를
// 함께 싣는다. 조립기가 원문을 쓰는 곳은 제목·요약 휴리스틱(첫 사용자 프롬프트)뿐이므로
// prompt 를 최우선으로 둔다. 여기서 고르지 않은 원문도 store.EventRecord.Contents 에는
// 전부 실려 event_content 에 저장되므로 잃는 것은 없다.
var contentPriority = []event.ContentKind{
	event.ContentPrompt,
	event.ContentResponse,
	event.ContentToolInput,
	event.ContentToolResult,
}

func pickContent(cs []event.Content) event.Content {
	if len(cs) == 0 {
		return event.Content{}
	}
	for _, want := range contentPriority {
		for _, c := range cs {
			if c.Kind == want {
				return c
			}
		}
	}
	return cs[0]
}

// closeSessions 는 유휴 세션을 마감하고 스냅샷을 저장한 뒤 조립기 메모리를 정리한다.
func (p *pipeline) closeSessions() {
	now := event.SecFromTime(p.now())

	closed := p.asm.Advance(now)
	if len(closed) > 0 {
		p.counters.sessionsClosed.Add(int64(len(closed)))
		for _, s := range closed {
			// ADR 0005 가 abandoned 판정 근거를 남기라고 요구했다. 오판 가능한
			// 휴리스틱이라 사후에 근거 없이는 아무도 검증할 수 없다.
			p.log.Printf("세션 마감: id=%s vendor=%s status=%s 근거=%q 도구=%d 재개=%d",
				s.SessionID, s.Vendor, s.Status, s.Diag.StatusReason, s.ToolCalls, s.Diag.Reopens)
		}
	}

	// Snapshot 은 진행 중·마감 세션을 모두 담은 **전체** 값이다. store 의 종속 테이블
	// 쓰기가 스냅샷을 정본으로 보고 대체하므로 부분 세션을 넣으면 누락이 삭제로
	// 해석된다 (store.Batch 주석). Snapshot()/Advance() 결과 외의 세션은 넘기지 않는다.
	p.flush(p.asm.Snapshot())

	// 조립기 메모리 정리. 이 호출이 없으면 세션 맵이 무한히 자라고, 더 나쁘게는
	// store.Prune 이 보존 정책으로 지운 타임라인을 다음 스냅샷이 통째로 되살린다.
	//
	// **반드시 flush 뒤에 온다.** 앞에 두면, 마감되는 순간 이미 TTL 을 넘긴 세션
	// (데몬이 몇 시간 잠들었다 깨는 경우)이 스냅샷에 들어가기 전에 사라져 store 에
	// 한 번도 쓰이지 못한다. 정리보다 저장이 먼저다.
	//
	// 보존 기간(400일)이 아니라 몇 시간짜리 TTL 을 쓰는 이유는 되살림을 구조적으로
	// 불가능하게 만들기 위해서다. 대신 TTL 을 지난 session.id 가 다시 등장하면 조립기가
	// 새 세션으로 시작하고 sessions UPSERT 가 기존 행을 덮는다 — 그래서 유휴 임계값
	// (10분)보다 한참 큰 값을 쓴다.
	if p.sessionTTL > 0 {
		before := now - event.UnixSec(p.sessionTTL/time.Second)
		if n := p.asm.Prune(before); n > 0 {
			p.log.Printf("조립기 정리: 세션 %d개 (마감 %s 이전)", n, p.sessionTTL)
		}
	}

}

// flush 는 미저장 이벤트·롤업·세션 스냅샷을 한 트랜잭션으로 쓴다.
//
// 세 종류를 한 트랜잭션에 묶는 것은 store 의 계약이다 — 부분 적용이 곧 화면의 모순이다.
func (p *pipeline) flush(sessions []session.Session) {
	rows := append(p.carryRollups, p.agg.Flush()...)
	p.carryRollups = nil
	if len(p.pending) == 0 && len(rows) == 0 && len(sessions) == 0 {
		p.dirty = false
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.writeTimeout)
	defer cancel()
	res, err := p.db.Write(ctx, store.Batch{Events: p.pending, Sessions: sessions, Rollups: rows})
	if err != nil {
		p.counters.writeErrors.Add(1)
		p.log.Printf("경고: 저장 실패 (이벤트 %d · 세션 %d · 롤업 %d, 다음 틱에 재시도): %v",
			len(p.pending), len(sessions), len(rows), err)
		// 실패한 배치는 다음 틱으로 넘긴다. 롤업 버킷은 Flush 가 이미 비웠으므로
		// 여기서 들고 있지 않으면 그대로 사라지고, rollup_hourly 는 UPSERT 누적이라
		// 같은 (hour,dim,key) 가 여러 번 들어와도 결과가 맞는다.
		//
		// 다만 무한히 들고 있으면 디스크 장애 하나가 메모리 누수가 된다. 상한을 넘으면
		// 오래된 것부터 버리고 로그를 남긴다 — 텔레메트리 손실은 허용, 데몬 정지는 불허.
		//
		// 세션 스냅샷은 되들고 있지 않는다. 조립기가 여전히 정본을 쥐고 있어 다음
		// 세션 틱이 더 최신인 전체 스냅샷을 다시 만들어 주기 때문이다.
		p.carryRollups = capTail(rows, maxCarryRollups, "롤업", p.log)
		p.pending = capTail(p.pending, maxCarryEvents, "이벤트", p.log)
		p.dirty = len(p.pending) > 0 || len(p.carryRollups) > 0
		return
	}

	p.counters.eventsStored.Add(int64(res.EventsInserted))
	p.counters.eventsDuplicate.Add(int64(res.EventsDuplicate))
	p.counters.contentsStored.Add(int64(res.ContentsInserted))
	p.counters.contentsDropped.Add(int64(res.ContentsDropped))
	p.counters.sessionsWritten.Add(int64(res.SessionsUpserted))
	p.counters.rollupRows.Add(int64(res.RollupRows))
	p.pending = nil
	p.dirty = false
	if res.RollupRows > 0 {
		p.recordRollupTime()
	}
}

// recordRollupTime 은 meta.last_rollup_at 을 갱신한다.
//
// 배치 트랜잭션 밖이라 별도 쓰기가 되므로 같은 초에 두 번 쓰지 않는다. 이 값은
// "마지막으로 롤업이 반영된 시각" 이라 초 단위면 충분하고, GUI 가 데이터 신선도를
// 표시하는 데만 쓴다.
func (p *pipeline) recordRollupTime() {
	now := event.SecFromTime(p.now())
	if now == p.lastRollupMeta {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.writeTimeout)
	defer cancel()
	if err := p.db.SetMeta(ctx, store.MetaLastRollupAt, strconv.FormatInt(int64(now), 10)); err != nil {
		p.log.Printf("경고: last_rollup_at 기록 실패: %v", err)
		return
	}
	p.lastRollupMeta = now
}

// prune 은 보존 정책을 적용한다.
func (p *pipeline) prune() {
	ctx, cancel := context.WithTimeout(context.Background(), p.pruneTimeout)
	defer cancel()
	res, err := p.db.Prune(ctx, p.now())
	if err != nil {
		// 치명적으로 다루지 않는다. Windows 에서 GUI 가 파일을 연 채면 실패할 수 있고
		// (계획서 「리스크」), store.Prune 은 트랜잭션 하나라 실패해도 부분 삭제가
		// 남지 않는다. 로깅하고 다음 틱에 그대로 다시 시도한다.
		p.counters.pruneErrors.Add(1)
		p.log.Printf("경고: 보존 정책 적용 실패 (다음 틱에 재시도): %v", err)
		return
	}
	if res.Total() > 0 {
		p.log.Printf("보존 정책 적용: 이벤트=%d 원문=%d 툴=%d 세션=%d 파일=%d MCP=%d 롤업=%d 벤더=%d",
			res.Events, res.EventContent, res.ToolEvents,
			res.Sessions, res.SessionFiles, res.MCPUsage, res.RollupHourly, res.Vendors)
	}
}

// finalFlush 는 종료 직전 마지막 저장이다.
//
// 마감 판정을 한 번 더 돌리는 이유는, 마지막 이벤트 이후 유휴 임계값을 넘긴 세션이
// running 상태로 남으면 화면 상단의 "N agents active" 가 죽은 세션을 계속 세기 때문이다.
func (p *pipeline) finalFlush() {
	now := event.SecFromTime(p.now())
	if closed := p.asm.Advance(now); len(closed) > 0 {
		p.counters.sessionsClosed.Add(int64(len(closed)))
	}
	p.flush(p.asm.Snapshot())
}

const (
	// maxCarryEvents·maxCarryRollups 는 저장 실패가 이어질 때 붙들 상한이다.
	maxCarryEvents  = 20_000
	maxCarryRollups = 5_000
)

// capTail 은 상한을 넘는 앞부분(오래된 쪽)을 버린다.
func capTail[T any](s []T, max int, what string, logger *log.Logger) []T {
	if len(s) <= max {
		return s
	}
	drop := len(s) - max
	logger.Printf("경고: 미저장 %s가 상한 %d 을 넘어 오래된 %d건을 버린다", what, max, drop)
	return append(s[:0], s[drop:]...)
}
