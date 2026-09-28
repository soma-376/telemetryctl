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
| `diagnostic` | `TEXT` | 필수 | 정규화한 사용량 필드의 오류 진단. 기본값은 빈 문자열 |
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

수신 배치에서 이벤트별로 분리한 원본 record JSON을 로컬에만 JSONB로 저장한다([ADR 0025](../adr/0025-이벤트별-수신-payload를-로컬에-보관한다.md)). 이 값은 별도 턴·호출·토큰을 만들지 않는다. 원문 저장을 끄거나 이벤트별·배치별 보관 한도를 넘으면 payload만 생략하고 정규화된 이벤트와 정상 사용량은 남긴다. `purge --content`는 이 컬럼을 `NULL`로 비운다. 상위 전달은 별도 포워더의 스크럽 정책을 따른다.

```sql
CREATE TABLE events (
  id          INTEGER PRIMARY KEY,
  turn_id     INTEGER NOT NULL REFERENCES turns (id) ON DELETE CASCADE,
  seq         INTEGER NOT NULL CHECK (typeof(seq) = 'integer' AND seq >= 1),
  event_name  TEXT NOT NULL,
  occurred_at INTEGER,
  record_hash TEXT NOT NULL UNIQUE,
  diagnostic TEXT NOT NULL DEFAULT '',
  payload     BLOB
    CHECK (payload IS NULL OR json_valid(payload, 8)),
  UNIQUE (turn_id, seq)
);

CREATE INDEX ix_events_name ON events (event_name);
```
