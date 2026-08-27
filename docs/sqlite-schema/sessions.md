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
