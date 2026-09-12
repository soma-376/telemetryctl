# `llm_calls`

원본 이벤트에서 승격한 LLM 호출의 모델·토큰·비용·시간 정보를 저장한다.

| 컬럼 | 타입 | 제약 | 설명 |
|---|---|---|---|
| `id` | `INTEGER` | 기본 키 | SQLite rowid 별칭 |
| `turn_id` | `INTEGER` | 필수, FK | 소유 `turns.id` |
| `source_event_id` | `INTEGER` | 필수, UNIQUE, FK | 승격 원본 `events.id` |
| `called_at` | `INTEGER` | 선택 | 호출 시각 |
| `model` | `TEXT` | 선택 | 모델 이름 |
| `input_tokens` | `INTEGER` | 선택 | 입력 토큰 |
| `output_tokens` | `INTEGER` | 선택 | reasoning을 포함한 전체 출력 토큰 |
| `cache_read_tokens` | `INTEGER` | 선택 | 캐시 읽기 토큰 |
| `cache_write_tokens` | `INTEGER` | 선택 | 캐시 쓰기 토큰 |
| `reasoning_tokens` | `INTEGER` | 선택 | 출력 토큰의 부분집합. 총량에 재가산하지 않음 |
| `cost_usd` | `NUMERIC` | 선택 | Claude Code 보고 비용 |
| `duration_ms` | `INTEGER` | 선택 | Claude Code 호출 시간 |
| `request_id` | `TEXT` | 선택 | Claude Code 요청 ID |

`source_event_id` UNIQUE가 같은 이벤트의 이중 승격을 막고 `ix_llm_turn(turn_id)`이 턴별 조회를
지원한다.

```sql
CREATE TABLE llm_calls (
  id                 INTEGER PRIMARY KEY,
  turn_id            INTEGER NOT NULL REFERENCES turns (id) ON DELETE CASCADE,
  source_event_id    INTEGER NOT NULL UNIQUE REFERENCES events (id),
  called_at          INTEGER,
  model              TEXT,
  input_tokens       INTEGER CHECK (input_tokens >= 0),
  output_tokens      INTEGER CHECK (output_tokens >= 0),
  cache_read_tokens  INTEGER CHECK (cache_read_tokens >= 0),
  cache_write_tokens INTEGER CHECK (cache_write_tokens >= 0),
  reasoning_tokens   INTEGER CHECK (reasoning_tokens >= 0),
  cost_usd           NUMERIC CHECK (cost_usd >= 0),
  duration_ms        INTEGER CHECK (duration_ms >= 0),
  request_id         TEXT
);

CREATE INDEX ix_llm_turn ON llm_calls (turn_id);
```
