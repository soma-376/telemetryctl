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

`(turn_id, seq)`가 UNIQUE이며 `ix_events_name(event_name)` 인덱스를 둔다.

## `seq` 와 `occurred_at` 의 차이

두 컬럼은 서로 다른 시계를 가리킨다. 섞어 쓰면 순서가 조용히 틀린다.

| 컬럼 | 누구의 시간인가 | 성질 |
|---|---|---|
| `occurred_at` | **벤더 시각** — 이벤트가 실제로 일어난 때 (Unix 초) | 배치가 섞이면 도착 순서와 어긋난다. 벤더가 안 주면 `NULL` |
| `seq` | **로컬 수집 도착 순서** — 데몬이 이 이벤트를 받은 차례 | 턴 안에서 1부터 단조 증가. 빈틈이 없고 `NULL`이 아니다 |

- 쓰기는 트랜잭션마다 턴별 high-water mark를 한 번 조회해 `seq`를 할당한다.
  **이미 저장된 행의 `seq`는 재번호하지 않는다.** 순서가 뒤집혀 도착해도 정상 입력이다.
- 중복(`record_hash` 충돌)으로 건너뛴 이벤트는 번호를 태우지 않는다.
- **독자는 `ORDER BY occurred_at, seq`로 읽는다.** 벤더 시각이 1차 기준이고 `seq`는 같은 초에
  일어난 이벤트의 안정 정렬용 tie-breaker다. `seq`만으로 정렬하면 늦게 도착한 이른 이벤트가
  타임라인 끝에 붙고, `occurred_at`만으로 정렬하면 같은 초의 이벤트 순서가 실행마다 달라진다.

## `payload`

**현재 쓰기 경로는 이 컬럼을 항상 `NULL`로 둔다.** 원본 OTLP 바이트를 붙들고 있는 경로가 없기
때문이다 — 수신한 바이트는 디코드 전에 포워더로 넘어가고, 로컬 저장은 정규화된 `event.Event`만
받는다. 원본을 통째로 담는 catch-all은 [ADR 0002](../adr/0002-로컬-집계-저장소로-SQLite-채택.md)·
[ADR 0003](../adr/0003-원문과-tool-details를-로컬에만-보관.md)이 명시적으로 거부한 것이기도 하다.

나중에 쓰게 되면 **반드시 `jsonb(?)`로 바인딩한다.** CHECK가 `json_valid(payload, 8)`이므로
텍스트 JSON이 아니라 SQLite JSONB를 요구한다. 읽을 때는 SQLite JSON 함수를 쓴다.

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
