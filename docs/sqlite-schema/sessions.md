# `sessions`

세션 한 건의 상태, 표시 정보, 누적 사용량을 한 행에 저장하는 화면의 중심 테이블이다.
`turns`, `session_phases`, `session_files`, `tool_events`, `mcp_session_usage`의 부모이며 기본
400일간 보존한다.

## 식별·상태·시간 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `session_id` | `TEXT` | 기본 키, DDL상 `NOT NULL` 미명시 | 벤더가 제공하는 세션 식별자. 애플리케이션 계약상 필수 |
| `vendor` | `TEXT` | 필수 | 세션을 생성한 벤더 식별자 |
| `started_at` | `INTEGER` | 필수 | 세션 시작 시각, UTC unix 초 |
| `last_event_at` | `INTEGER` | 필수 | 마지막 이벤트 시각, UTC unix 초 |
| `ended_at` | `INTEGER` | 선택, `NULL` | 세션 종료 시각, UTC unix 초 |
| `status` | `TEXT` | 필수 | 세션 상태 |

## 화면 표시 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `title` | `TEXT` | 선택, `NULL` | 화면에 표시할 세션 제목 |
| `title_source` | `TEXT` | 선택, `NULL` | 제목 생성 방식 또는 출처 |
| `summary` | `TEXT` | 선택, `NULL` | 세션 요약 |
| `project_hash` | `TEXT` | 선택, `NULL` | 프로젝트 전체 경로 대신 저장하는 해시 |
| `project_name` | `TEXT` | 선택, `NULL` | 프로젝트 basename |

## 누적 측정값 컬럼

모든 누적값은 필수이며 관측값이 없으면 `0`으로 시작한다.

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `duration_ms` | `INTEGER` | 필수, 기본값 `0` | 세션 전체 경과 시간, 밀리초 |
| `active_seconds` | `REAL` | 필수, 기본값 `0` | 실제 활동 시간, 초 |
| `input_tokens` | `INTEGER` | 필수, 기본값 `0` | 입력 토큰 수 |
| `output_tokens` | `INTEGER` | 필수, 기본값 `0` | 출력 토큰 수 |
| `cache_read_tokens` | `INTEGER` | 필수, 기본값 `0` | 캐시에서 읽은 토큰 수 |
| `cache_creation_tokens` | `INTEGER` | 필수, 기본값 `0` | 캐시 생성에 사용한 토큰 수 |
| `cost_usd` | `REAL` | 필수, 기본값 `0` | 추정 비용, USD |
| `tool_calls` | `INTEGER` | 필수, 기본값 `0` | 툴 호출 횟수 |
| `tool_errors` | `INTEGER` | 필수, 기본값 `0` | 실패한 툴 호출 횟수 |
| `tool_rejects` | `INTEGER` | 필수, 기본값 `0` | 거절된 툴 호출 횟수 |
| `api_requests` | `INTEGER` | 필수, 기본값 `0` | API 요청 횟수 |
| `api_errors` | `INTEGER` | 필수, 기본값 `0` | 실패한 API 요청 횟수 |
| `retries` | `INTEGER` | 필수, 기본값 `0` | 재시도 횟수 |
| `prompts` | `INTEGER` | 필수, 기본값 `0` | 프롬프트 수 |
| `responses` | `INTEGER` | 필수, 기본값 `0` | 응답 수 |
| `lines_added` | `INTEGER` | 필수, 기본값 `0` | 추가된 코드 줄 수 |
| `lines_removed` | `INTEGER` | 필수, 기본값 `0` | 제거된 코드 줄 수 |

## 분류 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `phase_json` | `TEXT` | 선택, `NULL` | 스키마 버전 1과의 호환성을 위해 남겨 둔 레거시 단계 분류 JSON. 신규 결과는 `session_phases`에 저장 |
| `work_type` | `TEXT` | 선택, `NULL` | 세션 전체의 후속 작업 유형 분류값. 턴별 결과는 `turns.work_type`에 저장 |

## 키·인덱스·관계

| 항목 | 내용 |
|---|---|
| 기본 키 | `session_id` |
| 자식 테이블 | `turns`, `session_phases`, `session_files`, `tool_events`, `mcp_session_usage` |
| 시간 인덱스 | `idx_sessions_started(started_at)` |
| 상태 인덱스 | `idx_sessions_status(status, last_event_at)` |
| 벤더 인덱스 | `idx_sessions_vendor(vendor, started_at)` |
| 프로젝트 인덱스 | `idx_sessions_project(project_hash, started_at)` |
| 보존 기준 | `started_at`이 아니라 `last_event_at` 기준 400일 |

`phase_json`과 `work_type`은 현재 세션 UPSERT가 덮어쓰지 않는다. `phase_json`은 삭제하거나
백필하지 않지만 신규 단계 분류의 저장소로 사용하지 않는다.

## 참고용 DDL

```sql
CREATE TABLE sessions (
  session_id    TEXT PRIMARY KEY,
  vendor        TEXT NOT NULL,
  started_at    INTEGER NOT NULL,
  last_event_at INTEGER NOT NULL,
  ended_at      INTEGER,
  status        TEXT NOT NULL,

  title         TEXT,
  title_source  TEXT,
  summary       TEXT,
  project_hash  TEXT,
  project_name  TEXT,

  duration_ms           INTEGER NOT NULL DEFAULT 0,
  active_seconds        REAL    NOT NULL DEFAULT 0,
  input_tokens          INTEGER NOT NULL DEFAULT 0,
  output_tokens         INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
  cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd              REAL    NOT NULL DEFAULT 0,
  tool_calls            INTEGER NOT NULL DEFAULT 0,
  tool_errors           INTEGER NOT NULL DEFAULT 0,
  tool_rejects          INTEGER NOT NULL DEFAULT 0,
  api_requests          INTEGER NOT NULL DEFAULT 0,
  api_errors            INTEGER NOT NULL DEFAULT 0,
  retries               INTEGER NOT NULL DEFAULT 0,
  prompts               INTEGER NOT NULL DEFAULT 0,
  responses             INTEGER NOT NULL DEFAULT 0,
  lines_added           INTEGER NOT NULL DEFAULT 0,
  lines_removed         INTEGER NOT NULL DEFAULT 0,

  phase_json    TEXT,
  work_type     TEXT
);

CREATE INDEX idx_sessions_started ON sessions(started_at);
CREATE INDEX idx_sessions_status  ON sessions(status, last_event_at);
CREATE INDEX idx_sessions_vendor  ON sessions(vendor, started_at);
CREATE INDEX idx_sessions_project ON sessions(project_hash, started_at);
```
