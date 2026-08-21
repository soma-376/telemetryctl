# `rollup_hourly`

UTC 시간 버킷과 차원 키별 누적 집계를 저장한다. 기본 400일간 보존한다.

## 버킷·차원 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `hour` | `INTEGER` | 필수 | UTC 정시 버킷, unix 초 |
| `dim` | `TEXT` | 필수 | 집계 차원. `total`, `vendor`, `model`, `tool`, `project`, `type` 중 하나 |
| `key` | `TEXT` | 필수 | 차원 안의 값. `dim='total'`이면 빈 문자열 |

## 누적 측정값 컬럼

모든 측정값은 필수이며 해당 버킷에 관측값이 없으면 `0`으로 시작한다.

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `cost_usd` | `REAL` | 필수, 기본값 `0` | 추정 비용, USD |
| `input_tokens` | `INTEGER` | 필수, 기본값 `0` | 입력 토큰 수 |
| `output_tokens` | `INTEGER` | 필수, 기본값 `0` | 출력 토큰 수 |
| `cache_read_tokens` | `INTEGER` | 필수, 기본값 `0` | 캐시에서 읽은 토큰 수 |
| `cache_creation_tokens` | `INTEGER` | 필수, 기본값 `0` | 캐시 생성에 사용한 토큰 수 |
| `api_requests` | `INTEGER` | 필수, 기본값 `0` | API 요청 횟수 |
| `api_errors` | `INTEGER` | 필수, 기본값 `0` | 실패한 API 요청 횟수 |
| `retries` | `INTEGER` | 필수, 기본값 `0` | 재시도 횟수 |
| `lines_added` | `INTEGER` | 필수, 기본값 `0` | 추가된 코드 줄 수 |
| `lines_removed` | `INTEGER` | 필수, 기본값 `0` | 제거된 코드 줄 수 |
| `commits` | `INTEGER` | 필수, 기본값 `0` | 커밋 수 |
| `pull_requests` | `INTEGER` | 필수, 기본값 `0` | Pull Request 수 |
| `prompts` | `INTEGER` | 필수, 기본값 `0` | 프롬프트 수 |
| `tool_calls` | `INTEGER` | 필수, 기본값 `0` | 툴 호출 횟수 |
| `tool_accepts` | `INTEGER` | 필수, 기본값 `0` | 승인된 툴 호출 수 |
| `tool_rejects` | `INTEGER` | 필수, 기본값 `0` | 거절된 툴 호출 수 |
| `active_seconds` | `REAL` | 필수, 기본값 `0` | 활동 시간, 초 |
| `sessions_started` | `INTEGER` | 필수, 기본값 `0` | 시작된 세션 수 |

## 키·관계·운영

| 항목 | 내용 |
|---|---|
| 기본 키 | (`hour`, `dim`, `key`) |
| 저장 형식 | 복합 기본 키를 사용하는 `WITHOUT ROWID` 테이블 |
| 관계 | 다른 테이블과 외래 키 관계 없음 |
| 쓰기 방식 | 동일 버킷을 UPSERT로 누적 |
| 보존 | `hour` 기준 400일 후 직접 삭제 |
| 컬럼 순서 | 측정값 순서는 `rollup.Bucket` 필드와 쓰기 쿼리 인자 순서에 맞춰 유지 |

## 참고용 DDL

```sql
CREATE TABLE rollup_hourly (
  hour INTEGER NOT NULL,
  dim  TEXT    NOT NULL,
  "key" TEXT   NOT NULL,
  cost_usd              REAL    NOT NULL DEFAULT 0,
  input_tokens          INTEGER NOT NULL DEFAULT 0,
  output_tokens         INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
  cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
  api_requests          INTEGER NOT NULL DEFAULT 0,
  api_errors            INTEGER NOT NULL DEFAULT 0,
  retries               INTEGER NOT NULL DEFAULT 0,
  lines_added           INTEGER NOT NULL DEFAULT 0,
  lines_removed         INTEGER NOT NULL DEFAULT 0,
  commits               INTEGER NOT NULL DEFAULT 0,
  pull_requests         INTEGER NOT NULL DEFAULT 0,
  prompts               INTEGER NOT NULL DEFAULT 0,
  tool_calls            INTEGER NOT NULL DEFAULT 0,
  tool_accepts          INTEGER NOT NULL DEFAULT 0,
  tool_rejects          INTEGER NOT NULL DEFAULT 0,
  active_seconds        REAL    NOT NULL DEFAULT 0,
  sessions_started      INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (hour, dim, "key")
) WITHOUT ROWID;
```
