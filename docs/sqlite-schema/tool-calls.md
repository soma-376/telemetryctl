# `tool_calls`

도구 승인 결정과 실행 결과 이벤트를 하나의 호출로 승격해 저장한다.

| 컬럼 | 타입 | 제약 | 설명 |
|---|---|---|---|
| `id` | `INTEGER` | 기본 키 | SQLite rowid 별칭 |
| `turn_id` | `INTEGER` | 필수, FK | 결과 이벤트의 턴, 결과가 없으면 결정 이벤트의 턴 |
| `call_key` | `TEXT` | 필수, UNIQUE | Claude Code `tool_use_id`, Codex `call_id` |
| `decision_event_id` | `INTEGER` | 선택, UNIQUE, FK | 결정 원본 `events.id`. 자동 승인은 `NULL` |
| `result_event_id` | `INTEGER` | 선택, UNIQUE, FK | 결과 원본 `events.id`. 거절은 `NULL` |
| `tool_name`, `target`, `mcp_server` | `TEXT` | 선택 | 도구와 대상 정보 |
| `called_at`, `duration_ms`, `blocked_on_user_ms` | `INTEGER` | 선택 | 호출·실행·사용자 대기 시간 |
| `success` | `INTEGER` | 선택 | `0`, `1`, 또는 결과 없음 `NULL` |
| `decision`, `decision_source` | `TEXT` | 선택 | 승인 결정과 출처 |
| `input_size_bytes`, `result_size_bytes` | `INTEGER` | 선택 | 입력·결과 크기 |
| `error_type`, `error_message` | `TEXT` | 선택 | 오류 정보 |

`decision_event_id`와 `result_event_id` 중 하나 이상이 반드시 존재해야 한다. 두 이벤트 ID는 각각
UNIQUE라 한 원본 이벤트가 여러 호출에 소비될 수 없다.

```sql
CREATE TABLE tool_calls (
  id                 INTEGER PRIMARY KEY,
  turn_id            INTEGER NOT NULL REFERENCES turns (id),
  call_key           TEXT NOT NULL UNIQUE,
  decision_event_id  INTEGER UNIQUE REFERENCES events (id),
  result_event_id    INTEGER UNIQUE REFERENCES events (id),
  tool_name          TEXT,
  target             TEXT,
  mcp_server         TEXT,
  called_at          INTEGER,
  duration_ms        INTEGER,
  blocked_on_user_ms INTEGER,
  success            INTEGER,
  decision           TEXT,
  decision_source    TEXT,
  input_size_bytes   INTEGER,
  result_size_bytes  INTEGER,
  error_type         TEXT,
  error_message      TEXT,
  CHECK (decision_event_id IS NOT NULL OR result_event_id IS NOT NULL)
);
```
