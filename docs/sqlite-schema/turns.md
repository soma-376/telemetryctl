# `turns`

세션 안에서 사용자 프롬프트로 시작하는 턴의 시간 경계와 누적 사용량을 저장한다. 프롬프트 원문은
저장하지 않고 길이만 남긴다. 세션 계층에 속해 기본 400일간 보존하며 부모 세션이 삭제되면 함께
삭제된다. 현재 스키마는 저장 자리만 제공하고 실제 턴 조립은 후속 작업에서 구현한다.

## 식별·시간 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `session_id` | `TEXT` | 필수, 복합 기본 키 | 턴이 속한 세션 ID |
| `turn_index` | `INTEGER` | 필수, 복합 기본 키 | 세션 안에서 1부터 시작하는 턴 번호 |
| `started_at` | `INTEGER` | 필수 | 사용자 프롬프트가 관측된 턴 시작 시각, UTC unix 초 |
| `last_event_at` | `INTEGER` | 필수 | 턴에 포함된 마지막 이벤트 시각, UTC unix 초 |
| `ended_at` | `INTEGER` | 선택, `NULL` | 다음 프롬프트 또는 세션 종료로 확정된 턴 종료 시각, UTC unix 초 |

## 표시·분류 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `prompt_length` | `INTEGER` | 필수, 기본값 `0` | 프롬프트 원문 대신 저장하는 문자 길이 |
| `work_type` | `TEXT` | 선택, `NULL` | 후속 분류기가 정하는 턴의 작업 유형 |

## 누적 측정값 컬럼

모든 누적값은 필수이며 관측값이 없으면 `0`으로 시작한다.

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `duration_ms` | `INTEGER` | 필수, 기본값 `0` | 턴 전체 경과 시간, 밀리초 |
| `active_seconds` | `REAL` | 필수, 기본값 `0` | 턴의 실제 활동 시간, 초 |
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
| `responses` | `INTEGER` | 필수, 기본값 `0` | 응답 수 |
| `lines_added` | `INTEGER` | 필수, 기본값 `0` | 추가된 코드 줄 수 |
| `lines_removed` | `INTEGER` | 필수, 기본값 `0` | 제거된 코드 줄 수 |

## 키·인덱스·관계

| 항목 | 내용 |
|---|---|
| 기본 키 | `(session_id, turn_index)` |
| 부모 | `session_id` → `sessions.session_id` |
| 툴 이벤트 | `tool_events.(session_id, turn_index)`가 논리적으로 참조하며 복합 외래 키는 없음 |
| 단계 | `session_phases`의 시작·종료 턴 번호가 같은 세션의 턴 범위를 논리적으로 참조 |
| 삭제·보존 | 부모 세션 삭제 시 `ON DELETE CASCADE`, 기본 400일 |
| 저장 형태 | 복합 키를 직접 저장 구조로 사용하는 `WITHOUT ROWID` |

## 운영·프라이버시 주의사항

- 사용자 프롬프트가 새 턴을 시작하고 다음 사용자 프롬프트 또는 세션 종료가 현재 턴을 닫는다.
- 프롬프트보다 먼저 발생했거나 턴을 판별할 수 없는 이벤트는 턴에 억지로 귀속하지 않는다.
- 프롬프트 원문은 `event_content`의 별도 보존 정책을 따르며 이 테이블에는 복제하지 않는다.
- 스키마 버전 2 적용 시 기존 데이터는 턴을 추측해 백필하지 않는다.

## 참고용 DDL

```sql
CREATE TABLE turns (
  session_id          TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  turn_index          INTEGER NOT NULL,
  started_at          INTEGER NOT NULL,
  last_event_at       INTEGER NOT NULL,
  ended_at            INTEGER,
  prompt_length       INTEGER NOT NULL DEFAULT 0,
  work_type           TEXT,
  duration_ms         INTEGER NOT NULL DEFAULT 0,
  active_seconds      REAL NOT NULL DEFAULT 0,
  input_tokens        INTEGER NOT NULL DEFAULT 0,
  output_tokens       INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens   INTEGER NOT NULL DEFAULT 0,
  cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd            REAL NOT NULL DEFAULT 0,
  tool_calls          INTEGER NOT NULL DEFAULT 0,
  tool_errors         INTEGER NOT NULL DEFAULT 0,
  tool_rejects        INTEGER NOT NULL DEFAULT 0,
  api_requests        INTEGER NOT NULL DEFAULT 0,
  api_errors          INTEGER NOT NULL DEFAULT 0,
  retries             INTEGER NOT NULL DEFAULT 0,
  responses           INTEGER NOT NULL DEFAULT 0,
  lines_added         INTEGER NOT NULL DEFAULT 0,
  lines_removed       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (session_id, turn_index)
) WITHOUT ROWID;
```
