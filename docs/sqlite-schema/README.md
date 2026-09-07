# SQLite 스키마

이 디렉터리는 로컬 데이터베이스의 현재 계약을 설명한다. 실행 DDL의 진실원은
`internal/store/schema.go`의 `schemaSQL`이며, 각 문서의 DDL은 검토용 사본이다.

> **전환 상태:** v3는 기존 v1/v2 도메인 테이블과 데이터를 트랜잭션 안에서 모두 삭제하고 새
> 모델을 만든다. `meta`와 DB 파일은 유지한다. **쓰기 런타임은 PROJ-85가, 세션 생명주기·보존·
> 원문 삭제는 PROJ-86이, 조회 계층(`internal/dashboard`)과 CLI 출력은 PROJ-87이 v3로 옮겼다.**
> 읽기 인덱스 세 개도 최신 전체 DDL에 포함한다(ADR 0012).

## 관계

```text
vendors
└── sessions
    └── turns
        ├── events
        │   ├── llm_calls
        │   └── tool_calls
        │       └── file_changes
        ├── llm_calls
        └── tool_calls
```

`llm_calls.turn_id`와 `tool_calls.turn_id`는 소유 턴을 직접 참조하고, 두 테이블의 이벤트 ID는
원본 `events` 행을 별도로 참조한다. 모든 외래 키의 삭제 동작은 SQLite 기본값인 `NO ACTION`이다.

## 문서 목록

| 문서 | 역할 |
|---|---|
| [`meta`](meta.md) | 스키마 버전과 설치 메타데이터 |
| [`vendors`](vendors.md) | 제품 단위 벤더 상태 |
| [`sessions`](sessions.md) | 벤더 세션과 사용자·워크스페이스 정보 |
| [`turns`](turns.md) | 실제 턴과 세션별 가상 턴 |
| [`events`](events.md) | 턴 안의 순서 있는 원본 이벤트와 JSONB payload |
| [`llm_calls`](llm-calls.md) | 이벤트에서 승격한 LLM 호출 |
| [`tool_calls`](tool-calls.md) | 결정·결과 이벤트에서 승격한 도구 호출 |
| [`file_changes`](file-changes.md) | 도구 호출에서 파생한 파일 변경 |

## 명명 인덱스

| 이름 | 정의 | 목적 |
|---|---|---|
| `ux_turns_virtual` | `turns(session_id) WHERE turn_index IS NULL` UNIQUE | 세션별 가상 턴 하나만 허용 |
| `ix_events_name` | `events(event_name)` | 이벤트 종류 조회 |
| `ix_llm_turn` | `llm_calls(turn_id)` | 턴별 LLM 호출 조회 |
| `ix_fc_tool` | `file_changes(tool_call_id)` | 도구 호출별 파일 변경 조회 |
| `ix_tool_calls_turn` | `tool_calls(turn_id)` | 턴별 도구 호출 조회 (v4) |
| `ix_turns_session` | `turns(session_id)` | 세션별 턴 조회 (v4) |
| `ix_sessions_started` | `sessions(started_at)` | 세션 목록 정렬·구간 필터 (v4) |

`ix_tool_calls_turn`·`ix_turns_session`·`ix_sessions_started`는 조회 계층이 세션 → 턴 →
도구 호출 방향으로 탐색할 때 쓰는 인덱스다. `events(turn_id)`는 `UNIQUE (turn_id, seq)`가 선두 컬럼으로
받쳐 주므로 따로 만들지 않는다.

DDL에 없는 인덱스, `ON DELETE CASCADE`, 기본값, 추가 `CHECK` 제약은 만들지 않는다.
제품 최초 배포 전의 인덱스 변경은 `schemaSQL`에 반영한다. 배포 후에는 ADR 0012의 재검토
조건에 따라 증분 마이그레이션으로 전환한다.

## 계약 규칙

이 규칙들은 [ADR 0009](../adr/0009-로컬-저장-모델을-v3로-전환한다.md)와
[ADR 0010](../adr/0010-v3가-요구하는-식별-정보를-로컬에만-저장한다.md)이 확정했다.

- **모든 시각 컬럼의 단위는 Unix 초다.** 나노초를 쓰는 컬럼은 없다.
- **`events.seq`는 로컬 수집 도착 순서**이고 벤더 시각이 아니다. 이미 저장된 행의 `seq`는
  재번호하지 않으며, 순서가 뒤집혀 도착해도 정상 입력으로 취급한다.
  독자는 `ORDER BY occurred_at, seq`로 읽는다.
- **삭제는 자식에서 부모 순서**로 한다. 모든 외래 키가 `NO ACTION`이라 순서를 어기면 실패한다.
  `file_changes → tool_calls → llm_calls → events → turns → sessions → vendors`.
  `vendors` 삭제는 `AND vendor NOT IN (SELECT vendor_id FROM sessions)`로 보호한다.
- **보존(400일) 판정 기준은 세션의 마지막으로 알려진 활동**이다. `ended_at`·`started_at`·소속
  이벤트 시각 중 가장 늦은 값을 쓰고, 셋 다 없으면 대상에서 빠진다. prune과 purge는 각각
  **하나의 트랜잭션**이다.
- **원문 삭제는 행이 아니라 컬럼을 비운다.** `purge --content`는 `turns.prompt_text`·
  `events.payload`·`tool_calls.error_message`를 `NULL`로 만든다. 행을 지우면 집계가 함께
  사라진다. 원문이 아닌 필드(`tool_calls.error_type`·`tool_name` 등)와 `meta`는 남는다.
- **세션 상태는 저장하지 않고 조회 시점에 계산한다.** `ended_at IS NULL`이면 `running`,
  아니면 `completed`. `abandoned`·`handoff`는 산출하지 않는다.
- **`sessions.workspace_path`·`user_email`·`user_account_id`, `file_changes.file_path`,
  `tool_calls.error_message`는 식별 정보를 담는다.** 로컬 저장 전용이며 상위 전달에는 실리지
  않는다. 상위 전달 스크럽은 `internal/forward`가 원본 바이트에 대해 수행한다.
- **원문 전문 검색은 `LIKE`로 한다.** v3에는 FTS 테이블이 없다.

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
2. 같은 트랜잭션에서 `meta.local_schema_version`을 현재 단일 세대로 기록한다.
3. 다른 세대의 개발 DB는 자동 변환하지 않고 삭제 후 재생성을 안내한다.
