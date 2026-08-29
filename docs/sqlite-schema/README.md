# SQLite 스키마

이 디렉터리는 로컬 데이터베이스의 스키마 버전 3 계약을 설명한다. 실행 DDL의 진실원은
`internal/store/schema.go`의 `schemaV3`이며, 각 문서의 DDL은 검토용 사본이다.

> **전환 상태:** v3는 기존 v1/v2 도메인 테이블과 데이터를 트랜잭션 안에서 모두 삭제하고 새
> 모델을 만든다. `meta`와 DB 파일은 유지한다. 현재 쓰기·조회·보존 런타임은 아직 v3로 전환되지
> 않았으므로 데몬과 CLI 기능 및 전체 테스트는 후속 작업 전까지 실패한다.

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

DDL에 없는 인덱스, `ON DELETE CASCADE`, 기본값, 추가 `CHECK` 제약은 만들지 않는다.
읽기 인덱스가 필요하면 **마이그레이션 v4 이후로 덧붙이고**, 이미 배포된 `schemaV3`의 문장은
고치지 않는다.

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

## v3 마이그레이션

1. v1/v2의 FTS5 트리거와 자식 테이블부터 부모 테이블 순서로 삭제한다.
2. 이 문서의 일곱 도메인 테이블을 부모부터 자식 순서로 생성한다.
3. 같은 트랜잭션에서 `meta.local_schema_version`을 `3`으로 기록한다.
4. 어느 문장이든 실패하면 삭제·생성과 버전 기록을 모두 롤백한다.

백업, 데이터 매핑, 백필은 수행하지 않는다. 이미 배포된 `schemaV1`과 `schemaV2` 문장은 변경하지
않으며 새 DB도 v1 → v2 → v3 순서로 적용된다.
