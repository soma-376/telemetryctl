# `mcp_session_usage`

세션별 MCP 서버의 연결 상태와 사용량을 저장한다. 기본 400일간 보존하며 부모 `sessions` 행을 지우면
함께 삭제된다.

## 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `session_id` | `TEXT` | 필수 | MCP 사용량이 속한 세션 ID |
| `server_name` | `TEXT` | 필수 | MCP 서버 이름 |
| `connected` | `INTEGER` | 필수, 기본값 `0` | 연결 상태 표시값 |
| `connect_failures` | `INTEGER` | 필수, 기본값 `0` | 연결 실패 횟수 |
| `tool_calls` | `INTEGER` | 필수, 기본값 `0` | 해당 서버의 툴 호출 횟수 |
| `tokens` | `INTEGER` | 필수, 기본값 `0` | 해당 서버에 귀속된 토큰 수 |

## 키·관계·운영

| 항목 | 내용 |
|---|---|
| 기본 키 | (`session_id`, `server_name`) |
| 부모 | `session_id` → `sessions.session_id` |
| 삭제 | 부모 세션 삭제 시 `ON DELETE CASCADE` |
| 저장 형식 | 복합 기본 키를 사용하는 `WITHOUT ROWID` 테이블 |
| 행 단위 | 한 세션 안에서 MCP 서버 이름당 한 행 |

## 참고용 DDL

```sql
CREATE TABLE mcp_session_usage (
  session_id  TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  server_name TEXT NOT NULL,
  connected        INTEGER NOT NULL DEFAULT 0,
  connect_failures INTEGER NOT NULL DEFAULT 0,
  tool_calls       INTEGER NOT NULL DEFAULT 0,
  tokens           INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (session_id, server_name)
) WITHOUT ROWID;
```
