package store

import (
	"math"
	"testing"

	"github.com/your-org/pulsemetry/internal/session"
)

func TestNonnegativeConstraints(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)
	for table, columns := range map[string][]string{
		"sessions":     {"active_time_sec"},
		"turns":        {"ttft_ms"},
		"llm_calls":    {"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "cost_usd", "duration_ms"},
		"tool_calls":   {"duration_ms", "blocked_on_user_ms", "input_size_bytes", "result_size_bytes"},
		"file_changes": {"additions", "deletions"},
	} {
		for _, column := range columns {
			t.Run(table+"/"+column, func(t *testing.T) {
				query := `UPDATE ` + table + ` SET ` + column + ` = ?`
				for _, value := range []any{nil, 0, 1} {
					mustExec(t, db, query, value)
				}
				expectConstraint(t, db, query, -1)
			})
		}
	}
}

// 잘못된 필드는 비우되 같은 행의 정상 필드와 같은 배치의 정상 이벤트는 저장한다.
func TestInvalidMeasuresDoNotRejectBatch(t *testing.T) {
	for _, invalidCost := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		db := openTestDB(t)
		bad := evrec("claude_code.api_request", baseTime, 0, cost(invalidCost), tokens(-1, 0))
		bad.Event.Measure.CacheReadTokens = someInt(-1)
		bad.Event.Measure.CacheCreationTokens = someInt(-1)
		bad.Event.Measure.DurationMS = someInt(-1)
		tool := evrec("claude_code.tool_result", baseTime, 1, call("invalid-measures"), fileChange(session.OperationModify, "main.go"))
		tool.Event.Measure.DurationMS = someInt(-1)
		tool.Event.Measure.ToolInputBytes = someInt(-1)
		tool.Event.Measure.ToolResultBytes = someInt(0)
		tool.File.Additions, tool.File.Deletions = someInt(-1), someInt(0)
		good := evrec("claude_code.api_request", baseTime, 2, cost(0.5), tokens(10, 20))
		mustWrite(t, db, Batch{Events: []EventRecord{bad, tool, good}})
		if countRows(t, db, "events") != 3 || countRows(t, db, "llm_calls") != 2 {
			t.Fatal("잘못된 수치가 정상 이벤트 저장을 막았다")
		}
		if n := countWhere(t, db, "llm_calls", `input_tokens IS NULL AND output_tokens = 0 AND cache_read_tokens IS NULL AND cache_write_tokens IS NULL AND cost_usd IS NULL AND duration_ms IS NULL`); n != 1 {
			t.Fatal("잘못된 LLM 수치를 NULL로 저장하지 않았다")
		}
		if n := countWhere(t, db, "llm_calls", `input_tokens = 10 AND output_tokens = 20 AND cost_usd = 0.5`); n != 1 {
			t.Fatal("정상 수치가 변경됐다")
		}
		if n := countWhere(t, db, "tool_calls", `duration_ms IS NULL AND input_size_bytes IS NULL AND result_size_bytes = 0`); n != 1 {
			t.Fatal("도구 수치의 NULL과 0이 구분되지 않았다")
		}
		if n := countWhere(t, db, "file_changes", `additions IS NULL AND deletions = 0`); n != 1 {
			t.Fatal("파일 변경 수치의 NULL과 0이 구분되지 않았다")
		}
	}
}

func TestInvalidActiveTime(t *testing.T) {
	for _, value := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), float64(math.MaxInt64), math.MaxFloat64} {
		db := openTestDB(t)
		s := newSession("sess-1", baseTime)
		s.ActiveSeconds = value
		mustWrite(t, db, Batch{Sessions: []session.Session{s}})
		if got := scanOne(t, db, `SELECT active_time_sec FROM sessions`); got != nil {
			t.Fatalf("활동 시간 %v 저장 결과 = %v, want NULL", value, got)
		}
	}
}
