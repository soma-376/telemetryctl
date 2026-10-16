# SQLite 스키마

이 디렉터리는 로컬 데이터베이스의 현재 계약을 설명한다. 실행 DDL의 진실원은
`internal/store/schema.go`의 `schemaSQL`이며, 문서에 실린 DDL은 검토용 사본이다.

> **스키마 버전: v1 고정.** 현재 전체 DDL로 빈 DB를 초기화한다. DDL 변경 시에도 버전을
> 올리지 않으며, 기존 개발 DB에 변경을 적용하려면 재생성한다(ADR 0012).

## 관계

```text
vendors
└── sessions
    └── turns
        ├── codex_turn_provenance
        ├── codex_pending
        ├── events
        │   ├── llm_calls
        │   └── tool_calls
        │       └── file_changes
        ├── llm_calls
        └── tool_calls

codex_jsonl_files
├── codex_jsonl_records
└── codex_jsonl_errors

codex_content_tombstones    codex_worker_state
```

`llm_calls.turn_id`와 `tool_calls.turn_id`는 소유 턴을 직접 참조하고, 두 테이블의 이벤트 ID는
원본 `events` 행을 별도로 참조한다. 세션 → 턴, 턴 → 이벤트·LLM 호출·도구 호출,
도구 호출 → 파일 변경은 `ON DELETE CASCADE`다. 벤더 참조와 원본 이벤트 참조는 `NO ACTION`이다.
Codex 출처·pending은 턴에, JSONL 레코드·오류는 파일 세대에 종속된다.

## 문서 목록

| 문서 | 역할 |
|---|---|
| [`vendor_limit_snapshots`](vendor-limit-snapshots.md) | 벤더별 최신 한도 조회 결과 |
| [`meta`](meta.md) | 스키마 버전과 설치 메타데이터 |
| [`vendors`](vendors.md) | 제품 단위 벤더 상태 |
| [`sessions`](sessions.md) | 벤더 세션과 사용자·워크스페이스 정보 |
| [`turns`](turns.md) | 실제 턴과 세션별 가상 턴 |
| [`events`](events.md) | 턴 안의 순서 있는 원본 이벤트와 JSONB payload |
| [`llm_calls`](llm-calls.md) | 이벤트에서 승격한 LLM 호출 |
| [`tool_calls`](tool-calls.md) | 결정·결과 이벤트에서 승격한 도구 호출 |
| [`file_changes`](file-changes.md) | 도구 호출에서 파생한 파일 변경 |
| [Codex 출처 보강 테이블](codex-provenance.md) | 분류·pending·JSONL 근거·파일 체크포인트·오류·삭제 표식·워커 상태 |

## 명명 인덱스

| 이름 | 정의 | 목적 |
|---|---|---|
| `ux_turns_virtual` | `turns(session_id) WHERE turn_index IS NULL` UNIQUE | 세션별 가상 턴 하나만 허용 |
| `ix_events_name` | `events(event_name)` | 이벤트 종류 조회 |
| `ix_llm_turn` | `llm_calls(turn_id)` | 턴별 LLM 호출 조회 |
| `ix_fc_tool` | `file_changes(tool_call_id)` | 도구 호출별 파일 변경 조회 |
| `ix_tool_calls_turn` | `tool_calls(turn_id)` | 턴별 도구 호출 조회 |
| `ix_turns_session` | `turns(session_id)` | 세션별 턴 조회 |
| `ix_sessions_started` | `sessions(started_at)` | 세션 목록 정렬·구간 필터 |
| `ix_codex_provenance_state` | `codex_turn_provenance(processing_state, label, checked_at)` | 출처·처리 상태 확인 |
| `ix_codex_pending_due` | `codex_pending(next_check_at, turn_id)` | 기한이 된 보강 대상 조회 |
| `ix_codex_jsonl_files_owner` | `codex_jsonl_files(owner_session_key, status)` | 소유 세션의 파일 세대 조회 |
| `ix_codex_jsonl_records_time` | `codex_jsonl_records(file_id, event_time)` | 파일·시각 후보 조회 |
| `ix_codex_jsonl_records_message` | `codex_jsonl_records(message_id)` | 메시지 ID 후보 조회 |
| `ix_codex_jsonl_records_turn` | `codex_jsonl_records(turn_id)` | 원천 turn ID 후보 조회 |

`ix_tool_calls_turn`·`ix_turns_session`·`ix_sessions_started`는 조회 계층이 세션 → 턴 →
도구 호출 방향으로 탐색할 때 쓰는 인덱스다. `events(turn_id)`는 `UNIQUE (turn_id, seq)`가 선두 컬럼으로
받쳐 주므로 따로 만들지 않는다.

인덱스·외래 키 삭제 동작·기본값·`CHECK` 제약은 실행 DDL과 동일하게 유지한다.
제품 최초 배포 전의 인덱스 변경은 `schemaSQL`에 반영한다. 배포 후에는 ADR 0012의 재검토
조건에 따라 증분 마이그레이션으로 전환한다.

## 계약 규칙

이 규칙들은 [ADR 0009](../adr/0009-로컬-저장-모델은-세션-턴-이벤트-계층으로-관리한다.md)와
[ADR 0010](../adr/0010-식별-정보를-로컬에만-저장한다.md)이 확정했다.

- 기존 세션·턴·호출의 **정수 시각은 Unix 초**다. Codex 보강의 `codex_jsonl_records.event_time`·
  `codex_content_tombstones.before_at`과 파일의 `observed_mod_ns`는 **Unix 나노초**다.
  한도 스냅샷의 `observed_at`은 RFC3339 문자열이다.
- **`events.seq`는 로컬 수집 도착 순서**이고 벤더 시각이 아니다. 이미 저장된 행의 `seq`는
  재번호하지 않으며, 순서가 뒤집혀 도착해도 정상 입력으로 취급한다.
  독자는 `ORDER BY occurred_at, seq`로 읽는다.
- **보존 삭제는 세션 단위**로 한다. 소유한 자식은 `ON DELETE CASCADE`로 함께 삭제한다.
  테이블별 삭제 건수는 같은 트랜잭션에서 삭제 전에 집계한다.
  `vendors` 삭제는 `AND vendor NOT IN (SELECT vendor_id FROM sessions)`로 보호한다.
- **보존(400일) 판정 기준은 세션의 마지막으로 알려진 활동**이다. `ended_at`·`started_at`·소속
  이벤트 시각 중 가장 늦은 값을 쓰고, 셋 다 없으면 대상에서 빠진다. prune과 purge는 각각
  **하나의 트랜잭션**이다.
- **원문 삭제는 행이 아니라 컬럼을 비운다.** `purge --content`는 `turns.prompt_text`·
  `events.payload`·`tool_calls.error_message`를 `NULL`로 만든다. 대상 Codex JSONL의 본문·해시도
  비우고 턴·owner 삭제 표식을 남겨 재처리로 원문이 되살아나지 않게 한다. 행을 지우면 집계가 함께
  사라지므로 비원문 분류·호출·사용량은 유지한다.
- **세션 상태는 저장하지 않고 조회 시점에 계산한다.** `ended_at IS NULL`이면 `running`,
  아니면 `completed`. `abandoned`·`handoff`는 산출하지 않는다.
- **`sessions.workspace_path`·`user_email`·`user_account_id`, `file_changes.file_path`,
  `tool_calls.error_message`는 식별 정보를 담는다.** 로컬 저장 전용이며 상위 전달에는 실리지
  않는다. 상위 전달 스크럽은 `internal/forward`가 원본 바이트에 대해 수행한다.
- **원문 전문 검색은 `LIKE`로 한다.** 현재 스키마에는 FTS 테이블이 없다.

## 값 제약

- 토큰 수·비용·소요 시간·바이트 수·추가/삭제 줄 수는 `CHECK (컬럼 >= 0)`로 제한한다.
  미확정은 `NULL`을 허용하고 실제 측정값 `0`은 유지한다. 저장 경계에서 음수 수치는
  `NULL`로 바꾸며 비용의 NaN·무한대와 활동 시간의 정수 변환 범위 초과도 `NULL`로 처리한다.
  한 필드의 오류로 같은 배치의 정상 이벤트까지 거부하지 않는다.

- `meta.key`와 `vendors.vendor`는 `NOT NULL`이다.
- `events.seq`는 1 이상의 정수, `turns.turn_index`는 0 이상의 정수 또는 가상 턴의 `NULL`이다.
- 세션과 턴의 시작·종료 시각이 모두 있으면 `ended_at >= started_at`이어야 한다.
  한쪽이 미확정인 `NULL`은 허용한다.
- `vendor_limit_snapshots.state`는 `available` 또는 `unavailable`이다.
  `windows_json`은 유효한 JSON 배열, `extra_json`은 유효한 JSON 객체여야 한다.
  이벤트 수집 전에 한도 조회가 가능하므로 `vendors` 외래 키는 두지 않는다.

## 연결 PRAGMA

연결 설정은 기존 저장소 설정을 유지한다.

| PRAGMA | 값 |
|---|---:|
| `journal_mode` | `WAL` |
| `busy_timeout` | `5000` ms |
| `foreign_keys` | `1` |
| `recursive_triggers` | `1` |
| `synchronous` | `NORMAL` |

## 배포 전 초기화

1. 빈 DB에 `schemaSQL` 전체를 한 트랜잭션으로 실행한다.
2. 같은 트랜잭션에서 `meta.local_schema_version`을 `1`로 기록한다.
3. 이미 있는 DB는 버전 1뿐 아니라 필수 테이블·컬럼 구조도 확인한다. 같은 v1이더라도 현재 구조가
   없으면 자동 변경·삭제하지 않고 백업 후 재생성을 안내하는 오류를 반환한다.

현재 지원 DDL에 들어 있던 과거 행의 출처 보강은 `codex_pending`을 등록해 수행한다.
구형 DB를 업데이트하는 증분 마이그레이션은 제공하지 않는다.
