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
