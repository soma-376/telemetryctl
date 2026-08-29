# `events`

턴 안에서 순서가 확정된 원본 이벤트를 저장한다. 승격된 LLM·도구 호출도 원본 이벤트 행을 유지한다.

| 컬럼 | 타입 | 제약 | 설명 |
|---|---|---|---|
| `id` | `INTEGER` | 기본 키 | SQLite rowid 별칭 |
| `turn_id` | `INTEGER` | 필수, FK | `turns.id` 참조 |
| `seq` | `INTEGER` | 필수 | 턴 내 **로컬 수집 도착 순서**. 벤더 시각이 아니다 |
| `event_name` | `TEXT` | 필수 | 원본 이벤트 종류 |
| `occurred_at` | `INTEGER` | 선택 | 이벤트 발생 시각 (**Unix 초**) |
| `record_hash` | `TEXT` | 필수, UNIQUE | 원본 레코드 중복 방지 해시 |
| `payload` | `BLOB` | 선택, CHECK | SQLite JSONB. `json_valid(payload, 8)`을 만족해야 함 |

`(turn_id, seq)`가 UNIQUE이며 `ix_events_name(event_name)` 인덱스를 둔다. payload는 쓸 때
`jsonb(?)`로 변환하고 읽을 때 SQLite JSON 함수를 사용한다.

```sql
CREATE TABLE events (
  id          INTEGER PRIMARY KEY,
  turn_id     INTEGER NOT NULL REFERENCES turns (id),
  seq         INTEGER NOT NULL,
  event_name  TEXT NOT NULL,
  occurred_at INTEGER,
  record_hash TEXT NOT NULL UNIQUE,
  payload     BLOB
    CHECK (payload IS NULL OR json_valid(payload, 8)),
  UNIQUE (turn_id, seq)
);

CREATE INDEX ix_events_name ON events (event_name);
```
