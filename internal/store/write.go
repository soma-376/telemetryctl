package store

import (
	"context"
	"fmt"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
)

// EventRecord 는 events 한 행과, 그 이벤트에서 승격할 값들을 함께 묶은 것이다.
//
// 디코더(otlpdecode.Result)는 Events·Contents·Targets 를 EventIndex 로 연결된 별도
// 슬라이스로 주고, 턴 경계와 도구 호출 식별자는 internal/session 이 정한다. 배선 단계
// (internal/daemon)가 그것들을 조인해 이 타입으로 묶어 넘긴다.
//
// 묶어서 받는 이유는 고아 방지다. 이벤트와 승격 대상이 따로 들어오면 "이벤트는 중복이라
// 무시했는데 승격은 저장" 하는 경로가 생기고, llm_calls.source_event_id 와
// tool_calls.decision_event_id 는 각각 UNIQUE 라 그 순간 배치 전체가 실패한다.
type EventRecord struct {
	Event event.Event

	// Contents 는 이 이벤트에서 뽑힌 원문이다. v3 에는 원문 테이블이 없고
	// 사용자 프롬프트만 turns.prompt_text 로 살아남는다. 나머지는 저장되지 않는다.
	Contents []event.Content

	// TurnKey 는 이 이벤트가 붙을 턴이다. **빈 값이면 세션 수준 가상 턴**이다.
	// 값을 정하는 것은 session.TurnTracker 이고 store 는 추측하지 않는다.
	TurnKey string

	// CallKey 는 tool_calls.call_key 다. 비어 있으면 도구 호출 승격 대상이 아니다.
	// 전역 UNIQUE 라 벤더 접두가 이미 붙어 있어야 한다 (session.Turn.CallKey).
	CallKey string

	// TargetPath 는 이 이벤트가 건드린 파일의 원경로다 (ADR 0010, 로컬 저장 전용).
	// tool_calls.target 으로 간다.
	TargetPath string

	// File 은 이 이벤트가 만든 파일 변경이다. Operation 이 빈 값이면 없다.
	File session.FileChange
}

// Batch 는 한 트랜잭션으로 적용되는 쓰기 단위다.
//
// 두 종류를 한 트랜잭션에 묶는 이유는 부분 적용이 곧 화면의 모순이기 때문이다 — 이벤트만
// 들어가고 세션이 안 들어가면 세션 정보가 이벤트보다 뒤처진다.
//
// Sessions 는 조립기가 주는 **전체 스냅샷**이다. v3 에는 스냅샷이 정본인 종속 테이블이
// 없으므로 부분 세션을 넣어도 삭제로 해석되지는 않지만, 조립기가 항상 전체를 주는 계약은
// 그대로다.
type Batch struct {
	Events   []EventRecord
	Sessions []session.Session
}

func (b Batch) isEmpty() bool { return len(b.Events) == 0 && len(b.Sessions) == 0 }

// WriteResult 는 배치 하나가 실제로 무엇을 했는지 알려준다.
//
// 특히 EventsDuplicate 가 중요하다. 중복 삽입은 에러가 아니라 정상 동작이지만(재전송은
// exporter 의 기본 동작이다) 조용히 넘어가면 "왜 이벤트 수가 보낸 것보다 적은가" 를
// 아무도 설명하지 못한다.
type WriteResult struct {
	VendorsTouched   int
	SessionsUpserted int
	TurnsUpserted    int

	EventsInserted  int
	EventsDuplicate int

	// PromptsStored 는 turns.prompt_text 에 실제로 쓴 프롬프트 수다.
	PromptsStored int
	// ContentsDropped 는 저장하지 않은 원문 수다. v3 에는 원문 테이블이 없어 사용자
	// 프롬프트를 뺀 나머지는 항상 여기 잡히고, --no-store-content 면 프롬프트도 포함된다.
	ContentsDropped int

	LLMCallsInserted    int
	ToolCallsUpserted   int
	FileChangesInserted int
}

// Write 는 배치를 하나의 트랜잭션으로 적용한다.
//
// # 순서가 계약이다
//
// v3 의 외래 키는 전부 NO ACTION 이고 연결은 foreign_keys=1 이다. 부모가 없는 자식을
// 넣으면 그 자리에서 실패한다. 그래서 삽입은 반드시 부모 → 자식 순서다:
//
//	vendors → sessions → turns → events → llm_calls · tool_calls → file_changes
//
// llm_calls 와 tool_calls 는 turns 와 events 를 **둘 다** 참조하므로 events 뒤에 온다.
// file_changes 는 tool_calls 를 참조하므로 마지막이다.
func (d *DB) Write(ctx context.Context, b Batch) (WriteResult, error) {
	var res WriteResult
	if b.isEmpty() {
		return res, nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("store: 쓰기 트랜잭션 시작: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 커밋 후에는 ErrTxDone 이라 무시한다

	w := newWriter(ctx, d, tx, &res)

	if err := w.writeVendors(b); err != nil {
		return WriteResult{}, err
	}
	if err := w.writeSessions(b); err != nil {
		return WriteResult{}, err
	}
	turnIDs, err := w.writeTurns(b.Events)
	if err != nil {
		return WriteResult{}, err
	}
	eventIDs, err := w.writeEvents(b.Events, turnIDs)
	if err != nil {
		return WriteResult{}, err
	}
	if err := w.promote(b.Events, turnIDs, eventIDs); err != nil {
		return WriteResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return WriteResult{}, fmt.Errorf("store: 쓰기 커밋: %w", err)
	}
	return res, nil
}
