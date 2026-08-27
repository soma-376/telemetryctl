# `turns`

세션 안의 실제 턴과 귀속할 수 없는 이벤트를 담는 가상 턴을 저장한다.

| 컬럼 | 타입 | 제약 | 설명 |
|---|---|---|---|
| `id` | `INTEGER` | 기본 키 | SQLite rowid 별칭 |
| `session_id` | `INTEGER` | 필수, FK | `sessions.id` 참조 |
| `turn_key` | `TEXT` | 필수 | Claude Code `prompt.id` 또는 Codex 합성 키 |
| `turn_index` | `INTEGER` | 선택 | 실제 턴 순서. 가상 턴은 `NULL` |
| `client_version` | `TEXT` | 선택 | 벤더 클라이언트 버전 |
| `started_at` | `INTEGER` | 선택 | 턴 시작 시각 |
| `ended_at` | `INTEGER` | 선택 | 턴 종료 시각 |
| `prompt_text` | `TEXT` | 선택 | 프롬프트 원문 |
| `ttft_ms` | `INTEGER` | 선택 | Codex time-to-first-token |

`(session_id, turn_key)`와 `(session_id, turn_index)`는 각각 UNIQUE다. SQLite는 UNIQUE의
`NULL`을 서로 다른 값으로 취급하므로 `ux_turns_virtual` 부분 UNIQUE 인덱스가 세션별
`turn_index IS NULL` 행을 하나로 제한한다.

```sql
CREATE TABLE turns (
  id             INTEGER PRIMARY KEY,
  session_id     INTEGER NOT NULL REFERENCES sessions (id),
  turn_key       TEXT NOT NULL,
  turn_index     INTEGER,
  client_version TEXT,
  started_at     INTEGER,
  ended_at       INTEGER,
  prompt_text    TEXT,
  ttft_ms        INTEGER,
  UNIQUE (session_id, turn_key),
  UNIQUE (session_id, turn_index)
);

CREATE UNIQUE INDEX ux_turns_virtual
ON turns (session_id) WHERE turn_index IS NULL;
```
