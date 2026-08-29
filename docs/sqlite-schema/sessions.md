# `sessions`

벤더가 제공하는 세션 키를 로컬 대리 키에 연결하고 표시·사용자·워크스페이스 정보를 저장한다.

| 컬럼 | 타입 | 제약 | 설명 |
|---|---|---|---|
| `id` | `INTEGER` | 기본 키 | SQLite rowid 별칭 |
| `vendor_id` | `TEXT` | 필수, FK | `vendors.vendor` 참조 |
| `session_key` | `TEXT` | 필수 | Claude Code `session.id`, Codex `conversation.id` |
| `title` | `TEXT` | 선택 | 세션 제목. ETL은 `NULL`일 때만 기록 |
| `workspace_path` | `TEXT` | 선택 | 작업공간 경로 |
| `user_email` | `TEXT` | 선택 | 관측된 사용자 이메일 |
| `user_account_id` | `TEXT` | 선택 | 벤더 사용자 계정 ID |
| `terminal_type` | `TEXT` | 선택 | 터미널 종류 |
| `started_at` | `INTEGER` | 선택 | 시작 시각 |
| `ended_at` | `INTEGER` | 선택 | 종료 시각. 진행 중이면 `NULL` |
| `active_time_sec` | `INTEGER` | 선택 | Claude Code 활동 시간 |

`(vendor_id, session_key)`는 UNIQUE다. `turns.session_id`가 `id`를 참조하며 삭제 동작은
`NO ACTION`이다.

## 생명주기 컬럼

세션 상태는 저장하지 않고 `ended_at IS NULL`로 계산하므로(ADR 0009) 이 세 컬럼이 곧 상태다.

| 컬럼 | 규칙 |
|---|---|
| `started_at` | 가장 이른 관측. 늦게 도착한 배치가 시작 시각을 밀지 않는다 |
| `ended_at` | 조립기 스냅샷이 정본이다. 마감된 세션에 같은 `session_key`로 이벤트가 다시 오면 조립기가 마감을 되돌리므로 컬럼도 `NULL`로 돌아간다. 이벤트만 저장되는 쓰기는 이 컬럼을 건드리지 않는다 |
| `active_time_sec` | 단조 증가. 데몬이 재시작하면 조립기가 0부터 다시 세므로 새 값이 더 작아도 기록된 값을 줄이지 않는다. 한 번도 관측되지 않으면 `NULL`이며 0초와 다르다 |

```sql
CREATE TABLE sessions (
  id              INTEGER PRIMARY KEY,
  vendor_id       TEXT NOT NULL REFERENCES vendors (vendor),
  session_key     TEXT NOT NULL,
  title           TEXT,
  workspace_path  TEXT,
  user_email      TEXT,
  user_account_id TEXT,
  terminal_type   TEXT,
  started_at      INTEGER,
  ended_at        INTEGER,
  active_time_sec INTEGER,
  UNIQUE (vendor_id, session_key)
);
```
