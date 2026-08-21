# `events`

OTLP 시그널을 정규화한 이벤트를 저장한다. 속성은 allowlist 컬럼만 허용하며 catch-all 컬럼이 없으므로
전체 경로와 임의 속성이 저장될 자리가 없다. 기본 30일간 보존한다.

## 식별·시간 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `id` | `INTEGER` | 필수, 기본 키 | SQLite가 부여하는 이벤트 대리 키 |
| `dedup_key` | `TEXT` | 필수, UNIQUE | 재시작 이후에도 중복 저장을 막는 키 |
| `ts` | `INTEGER` | 필수 | 이벤트 시각, UTC unix 나노초 |
| `hour` | `INTEGER` | 필수 | `ts`를 UTC 정시로 내린 unix 나노초 |
| `session_id` | `TEXT` | 선택, `NULL` | 세션 조립 연결 키. 외래 키는 아님 |
| `vendor` | `TEXT` | 필수 | 이벤트를 생성한 벤더 식별자 |
| `signal` | `TEXT` | 필수 | 정규화 시그널 종류 |
| `name` | `TEXT` | 필수 | 원본 이벤트 또는 메트릭 이름 |
| `installation_id` | `TEXT` | 필수 | enrollment 설치 식별자 |
| `event_id` | `TEXT` | 선택, `NULL` | 벤더가 제공한 이벤트 식별자 |
| `trace_id` | `TEXT` | 선택, `NULL` | 연결된 트레이스 식별자 |
| `span_id` | `TEXT` | 선택, `NULL` | 연결된 스팬 식별자 |

## 허용 속성 컬럼

아래 컬럼이 저장 가능한 속성의 전체 목록이다. 임의 속성을 담는 catch-all 컬럼은 없다.

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `model` | `TEXT` | 선택, `NULL` | 사용한 모델 이름 |
| `type` | `TEXT` | 선택, `NULL` | 이벤트 또는 작업 유형 |
| `tool_name` | `TEXT` | 선택, `NULL` | 툴 이름 |
| `decision` | `TEXT` | 선택, `NULL` | 승인·거절 등 결정값 |
| `decision_source` | `TEXT` | 선택, `NULL` | 결정이 발생한 출처 |
| `language` | `TEXT` | 선택, `NULL` | 프로그래밍 언어 |
| `query_source` | `TEXT` | 선택, `NULL` | 쿼리 또는 요청 출처 |
| `speed` | `TEXT` | 선택, `NULL` | 속도 관련 분류값 |
| `effort` | `TEXT` | 선택, `NULL` | 노력 수준 관련 분류값 |
| `agent_name` | `TEXT` | 선택, `NULL` | 에이전트 이름 |
| `skill_name` | `TEXT` | 선택, `NULL` | 스킬 이름 |
| `plugin_name` | `TEXT` | 선택, `NULL` | 플러그인 이름 |
| `mcp_server` | `TEXT` | 선택, `NULL` | MCP 서버 이름 |
| `mcp_tool` | `TEXT` | 선택, `NULL` | MCP 툴 이름 |
| `start_type` | `TEXT` | 선택, `NULL` | 시작 방식 |
| `terminal_type` | `TEXT` | 선택, `NULL` | 터미널 종류 |
| `app_version` | `TEXT` | 선택, `NULL` | 벤더 애플리케이션 버전 |
| `entrypoint` | `TEXT` | 선택, `NULL` | 실행 진입점 |
| `environment` | `TEXT` | 선택, `NULL` | 실행 환경 분류값 |
| `project_hash` | `TEXT` | 선택, `NULL` | 프로젝트 전체 경로 대신 저장하는 해시 |
| `project_name` | `TEXT` | 선택, `NULL` | 프로젝트 basename |

## 측정값·결과 컬럼

이벤트 종류에 해당하지 않는 측정값은 `NULL`로 남는다.

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `value` | `REAL` | 선택, `NULL` | 정규화된 수치 값 |
| `unit` | `TEXT` | 선택, `NULL` | `value`의 단위 |
| `cost_usd` | `REAL` | 선택, `NULL` | 이벤트 비용, USD |
| `input_tokens` | `INTEGER` | 선택, `NULL` | 입력 토큰 수 |
| `output_tokens` | `INTEGER` | 선택, `NULL` | 출력 토큰 수 |
| `cache_read_tokens` | `INTEGER` | 선택, `NULL` | 캐시에서 읽은 토큰 수 |
| `cache_creation_tokens` | `INTEGER` | 선택, `NULL` | 캐시 생성에 사용한 토큰 수 |
| `duration_ms` | `INTEGER` | 선택, `NULL` | 처리 시간, 밀리초 |
| `status_code` | `INTEGER` | 선택, `NULL` | 관측된 상태 코드 |
| `attempt` | `INTEGER` | 선택, `NULL` | 요청 또는 작업 시도 횟수 |
| `success` | `INTEGER` | 선택, `NULL` | 성공 여부. `NULL`은 결과 미상 |
| `error_type` | `TEXT` | 선택, `NULL` | 오류 유형 |
| `prompt_length` | `INTEGER` | 선택, `NULL` | 프롬프트 원문의 문자 길이 |
| `response_length` | `INTEGER` | 선택, `NULL` | 응답 원문의 문자 길이 |
| `tool_input_bytes` | `INTEGER` | 선택, `NULL` | 툴 입력 원문의 실제 바이트 수 |
| `tool_result_bytes` | `INTEGER` | 선택, `NULL` | 툴 결과 원문의 실제 바이트 수 |

## 키·인덱스·관계

| 항목 | 내용 |
|---|---|
| 기본 키 | `id` |
| 고유 제약 | `dedup_key` UNIQUE |
| 시간 인덱스 | `idx_events_hour(hour)`로 보존 삭제 범위 탐색 지원 |
| 세션 인덱스 | `idx_events_session(session_id, ts)`로 세션별 시간순 조회 지원 |
| 자식 테이블 | `event_content.event_id`가 `events.id`를 참조하고 삭제 시 CASCADE |
| 세션 관계 | `session_id`는 논리적 연결 키이며 `sessions` 외래 키가 아님 |

## 프라이버시·운영

- 전체 작업·파일 경로는 저장하지 않고 프로젝트 해시와 basename만 저장한다.
- 프롬프트·응답·툴 원문은 이 테이블에 넣지 않고 길이·바이트 수만 남긴다.
- 실제 원문은 [`event_content`](event-content.md)에 로컬 전용으로 저장한다.
- `hour` 조건으로 인덱스 범위를 줄이고 `ts`로 실제 30일 보존 경계를 판정한다.

## 참고용 DDL

```sql
CREATE TABLE events (
  id              INTEGER PRIMARY KEY,
  dedup_key       TEXT    NOT NULL UNIQUE,
  ts              INTEGER NOT NULL,
  hour            INTEGER NOT NULL,
  session_id      TEXT,
  vendor          TEXT    NOT NULL,
  signal          TEXT    NOT NULL,
  name            TEXT    NOT NULL,
  installation_id TEXT    NOT NULL,
  event_id        TEXT,
  trace_id        TEXT,
  span_id         TEXT,

  model           TEXT,
  type            TEXT,
  tool_name       TEXT,
  decision        TEXT,
  decision_source TEXT,
  language        TEXT,
  query_source    TEXT,
  speed           TEXT,
  effort          TEXT,
  agent_name      TEXT,
  skill_name      TEXT,
  plugin_name     TEXT,
  mcp_server      TEXT,
  mcp_tool        TEXT,
  start_type      TEXT,
  terminal_type   TEXT,
  app_version     TEXT,
  entrypoint      TEXT,
  environment     TEXT,
  project_hash    TEXT,
  project_name    TEXT,

  value                 REAL,
  unit                  TEXT,
  cost_usd              REAL,
  input_tokens          INTEGER,
  output_tokens         INTEGER,
  cache_read_tokens     INTEGER,
  cache_creation_tokens INTEGER,
  duration_ms           INTEGER,
  status_code           INTEGER,
  attempt               INTEGER,
  success               INTEGER,
  error_type            TEXT,
  prompt_length         INTEGER,
  response_length       INTEGER,
  tool_input_bytes      INTEGER,
  tool_result_bytes     INTEGER
);

CREATE INDEX idx_events_hour    ON events(hour);
CREATE INDEX idx_events_session ON events(session_id, ts);
```
