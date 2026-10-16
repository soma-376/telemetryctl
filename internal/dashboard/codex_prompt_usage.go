package dashboard

import (
	"context"
	"time"
)

// CodexPromptUsage는 동일 호출 시각 범위의 Codex 전체 사용량을 출처별로 나눈다.
// NULL인 토큰 필드는 0으로 계산하며 cache/reasoning은 중복 가산하지 않는다.
type CodexPromptUsage struct {
	TotalTokens        int64 `json:"total_tokens"`
	UserTokens         int64 `json:"user_tokens"`
	SystemTokens       int64 `json:"system_tokens"`
	UnclassifiedTokens int64 `json:"unclassified_tokens"`
	OtherTokens        int64 `json:"other_tokens"`
}

func ReadCodexPromptUsage(ctx context.Context, db SQLQuerier, start, end time.Time) (CodexPromptUsage, error) {
	var out CodexPromptUsage
	if db == nil {
		return out, nil
	}
	const query = `SELECT
 COALESCE(SUM(COALESCE(c.input_tokens,0)+COALESCE(c.output_tokens,0)),0),
 COALESCE(SUM(CASE WHEN p.label='client_submitted' AND p.processing_state='finalized' AND t.turn_index IS NOT NULL
  THEN COALESCE(c.input_tokens,0)+COALESCE(c.output_tokens,0) ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN t.turn_index IS NOT NULL AND p.processing_state='finalized' AND p.label IN ('internal_task','unknown')
  THEN COALESCE(c.input_tokens,0)+COALESCE(c.output_tokens,0) ELSE 0 END),0)
FROM llm_calls c JOIN turns t ON t.id=c.turn_id JOIN sessions s ON s.id=t.session_id
LEFT JOIN codex_turn_provenance p ON p.turn_id=t.id
WHERE s.vendor_id='codex' AND c.called_at>=? AND c.called_at<?`
	if err := db.QueryRowContext(ctx, query, start.Unix(), end.Unix()).Scan(&out.TotalTokens, &out.UserTokens, &out.SystemTokens); err != nil {
		return CodexPromptUsage{}, QueryErr("Codex 프롬프트 사용량 조회", err)
	}
	out.OtherTokens = out.TotalTokens - out.UserTokens
	out.UnclassifiedTokens = out.OtherTokens - out.SystemTokens
	return out, nil
}
