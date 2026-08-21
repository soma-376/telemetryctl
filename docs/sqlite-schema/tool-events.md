# `tool_events`

세션 안에서 실행된 툴의 시간순 타임라인을 저장한다. 대상은 전체 경로가 아니라 해시와 basename만
남긴다. 이벤트 계층에 속해 기본 30일간 보존한다.

## 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `id` | `INTEGER` | 필수, 기본 키 | 툴 이벤트 대리 키 |
| `session_id` | `TEXT` | 필수 | 툴 실행이 속한 세션 ID |
| `ts` | `INTEGER` | 필수 | 실행 시각, UTC unix 초 |
| `tool_name` | `TEXT` | 필수 | 실행한 툴 이름 |
| `action` | `TEXT` | 선택, `NULL` | 툴에서 수행한 동작 |
| `target_name` | `TEXT` | 선택, `NULL` | 대상의 basename 또는 안전한 표시 이름 |
| `target_hash` | `TEXT` | 선택, `NULL` | 대상 전체 경로 대신 저장하는 해시 |
| `success` | `INTEGER` | 선택, `NULL` | 성공 여부. `NULL`은 실패가 아니라 결과 미상 |
| `duration_ms` | `INTEGER` | 선택, `NULL` | 실행 시간, 밀리초 |
| `error_type` | `TEXT` | 선택, `NULL` | 실패 유형 |
| `decision` | `TEXT` | 선택, `NULL` | 사용자 승인·거절 등 툴 결정 |
| `mcp_server` | `TEXT` | 선택, `NULL` | MCP 툴인 경우 서버 이름 |

## 키·인덱스·관계

| 항목 | 내용 |
|---|---|
| 기본 키 | `id` |
| 부모 | `session_id` → `sessions.session_id` |
| 삭제 | 부모 세션 삭제 시 `ON DELETE CASCADE`; 보존 정책은 30일 후 직접 삭제 |
| 인덱스 | `idx_tool_events_session(session_id, ts)`로 세션별 시간순 조회 지원 |
| 프라이버시 | 대상 전체 경로는 저장하지 않고 해시와 표시 이름만 저장 |

## 참고용 DDL

```sql
CREATE TABLE tool_events (
  id          INTEGER PRIMARY KEY,
  session_id  TEXT    NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  ts          INTEGER NOT NULL,
  tool_name   TEXT    NOT NULL,
  action      TEXT,
  target_name TEXT,
  target_hash TEXT,
  success     INTEGER,
  duration_ms INTEGER,
  error_type  TEXT,
  decision    TEXT,
  mcp_server  TEXT
);

CREATE INDEX idx_tool_events_session ON tool_events(session_id, ts);
```
