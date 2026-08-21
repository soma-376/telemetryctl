# `session_phases`

세션의 연속된 턴을 작업 단계로 묶은 분류 결과를 저장한다. 분류 taxonomy와 분류기 구현은 후속
작업에서 정하며, 이 테이블은 결과를 질의하고 분류기 버전을 추적할 저장 계약만 제공한다. 세션 계층에
속해 기본 400일간 보존하며 부모 세션이 삭제되면 함께 삭제된다.

## 식별·분류 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `session_id` | `TEXT` | 필수, 복합 기본 키 | 단계가 속한 세션 ID |
| `phase_index` | `INTEGER` | 필수, 복합 기본 키 | 세션 안에서 1부터 시작하는 단계 순번 |
| `phase_type` | `TEXT` | 필수 | 분류된 단계 유형. taxonomy를 DB 제약으로 고정하지 않음 |
| `confidence` | `REAL` | 선택, `NULL` | 분류 신뢰도. 분류기가 제공하지 않으면 `NULL` |
| `classifier` | `TEXT` | 선택, `NULL` | 분류기 또는 분류 방식의 이름 |
| `classifier_version` | `TEXT` | 선택, `NULL` | 결과를 만든 분류기 버전 |

## 턴 범위·시간 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `start_turn_index` | `INTEGER` | 필수 | 단계에 포함되는 첫 턴 번호 |
| `end_turn_index` | `INTEGER` | 필수 | 단계에 포함되는 마지막 턴 번호 |
| `started_at` | `INTEGER` | 필수 | 첫 턴 시작 시각, UTC unix 초 |
| `last_event_at` | `INTEGER` | 필수 | 마지막 턴의 마지막 이벤트 시각, UTC unix 초 |
| `turn_count` | `INTEGER` | 필수, 기본값 `0` | 단계에 포함된 턴 수 |

## 키·인덱스·관계

| 항목 | 내용 |
|---|---|
| 기본 키 | `(session_id, phase_index)` |
| 부모 | `session_id` → `sessions.session_id` |
| 턴 범위 | `start_turn_index`부터 `end_turn_index`까지 같은 세션의 `turns`를 논리적으로 참조 |
| 삭제·보존 | 부모 세션 삭제 시 `ON DELETE CASCADE`, 기본 400일 |
| 저장 형태 | 복합 키를 직접 저장 구조로 사용하는 `WITHOUT ROWID` |

## 운영·프라이버시 주의사항

- 단계 순번은 1부터 시작하며 `start_turn_index`는 `end_turn_index`보다 클 수 없다.
- 같은 세션의 단계 범위는 겹치지 않고 단계 순서대로 이어져야 한다.
- `turn_count`는 포함된 턴 수와 일치해야 하고 범위의 양 끝 턴은 같은 세션에 존재해야 한다.
- 위 조건은 분류 결과를 쓰는 애플리케이션이 검증한다. taxonomy 변경과 재분류를 막지 않도록
  CHECK 제약과 `turns`에 대한 복합 외래 키는 두지 않는다.
- 프롬프트나 응답 원문은 저장하지 않는다.

## 참고용 DDL

```sql
CREATE TABLE session_phases (
  session_id          TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  phase_index         INTEGER NOT NULL,
  phase_type          TEXT NOT NULL,
  start_turn_index    INTEGER NOT NULL,
  end_turn_index      INTEGER NOT NULL,
  started_at          INTEGER NOT NULL,
  last_event_at       INTEGER NOT NULL,
  turn_count          INTEGER NOT NULL DEFAULT 0,
  confidence          REAL,
  classifier          TEXT,
  classifier_version  TEXT,
  PRIMARY KEY (session_id, phase_index)
) WITHOUT ROWID;
```
