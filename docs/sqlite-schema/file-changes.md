# `file_changes`

도구 호출에서 관측한 파일 생성·수정·삭제·이름 변경을 저장한다.

| 컬럼 | 타입 | 제약 | 설명 |
|---|---|---|---|
| `id` | `INTEGER` | 기본 키 | SQLite rowid 별칭 |
| `tool_call_id` | `INTEGER` | 필수, FK | `tool_calls.id` 참조 |
| `file_path` | `TEXT` | 필수 | 파일 경로. rename이면 새 경로 |
| `operation` | `TEXT` | 필수, CHECK | `create`, `modify`, `delete`, `rename` 중 하나 |
| `renamed_from` | `TEXT` | 선택 | rename 이전 경로 |
| `additions`, `deletions` | `INTEGER` | 선택 | 관측된 줄 수. 미관측은 `NULL` |
| `old_hash`, `new_hash` | `TEXT` | 선택 | 변경 전·후 해시 |

`ix_fc_tool(tool_call_id)`이 도구 호출별 조회를 지원한다.

```sql
CREATE TABLE file_changes (
  id           INTEGER PRIMARY KEY,
  tool_call_id INTEGER NOT NULL REFERENCES tool_calls (id),
  file_path    TEXT NOT NULL,
  operation    TEXT NOT NULL
    CHECK (operation IN ('create', 'modify', 'delete', 'rename')),
  renamed_from TEXT,
  additions    INTEGER,
  deletions    INTEGER,
  old_hash     TEXT,
  new_hash     TEXT
);

CREATE INDEX ix_fc_tool ON file_changes (tool_call_id);
```
