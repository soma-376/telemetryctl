package dashboard

// codexClientTurnSQL은 저장된 Codex 턴의 확정된 사용자 제출 분류다.
// turns t 별칭을 요구한다. 원문과 JSONL 연결 상태는 자격 조건이 아니다.
const codexClientTurnSQL = `EXISTS (
  SELECT 1 FROM codex_turn_provenance p WHERE p.turn_id=t.id
    AND p.label='client_submitted' AND p.processing_state='finalized')`

// promptEligibleSQL은 기존 프롬프트 수·상세의 실제 턴 자격이다.
// turns t와 sessions s 별칭을 요구한다.
const promptEligibleSQL = `t.turn_index IS NOT NULL AND (s.vendor_id <> 'codex' OR ` + codexClientTurnSQL + `)`

// ActivityContentEligibleSQL은 원문 검색에서 Codex만 출처로 제한한다.
// 다른 벤더는 기존대로 가상 턴을 포함한 저장 원문을 검색한다.
const ActivityContentEligibleSQL = `s.vendor_id <> 'codex' OR
  (t.turn_index IS NOT NULL AND ` + codexClientTurnSQL + `)`

// DashboardSessionEligibleSQL은 Home·Activity 목록과 표시용 세션 수에만 쓴다.
// CLI·트레이·설정의 원시 조회와 ID 직접 상세에는 적용하지 않는다 (ADR 0033).
// 세션의 전체 실제 턴을 검사하며, 원문과 JSONL 연결 상태는 자격 조건이 아니다.
const DashboardSessionEligibleSQL = `s.vendor_id <> 'codex' OR EXISTS (
  SELECT 1 FROM turns t WHERE t.session_id=s.id AND t.turn_index IS NOT NULL
    AND ` + codexClientTurnSQL + `)`
