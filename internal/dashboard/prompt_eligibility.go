package dashboard

// promptEligibleSQL은 기간 집계·세션 카드·상세·Activity에서 공통으로 사용한다.
// turns 별칭 t와 sessions 별칭 s가 있어야 한다.
const promptEligibleSQL = `t.turn_index IS NOT NULL AND (s.vendor_id <> 'codex' OR EXISTS (
  SELECT 1 FROM codex_turn_provenance p WHERE p.turn_id=t.id
    AND p.label='client_submitted' AND p.processing_state='finalized'))`
