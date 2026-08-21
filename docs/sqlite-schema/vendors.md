# `vendors`

로컬에서 관측한 벤더별 최초·최근 시각과 누적 이벤트 수를 저장해 Settings의 연결 상태에 사용한다.
`last_seen` 기준으로 기본 400일간 보존한다.

## 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `vendor` | `TEXT` | 기본 키, DDL상 `NOT NULL` 미명시 | 벤더 식별자. 애플리케이션 계약상 필수 |
| `first_seen` | `INTEGER` | 필수 | 최초 관측 시각, UTC unix 초 |
| `last_seen` | `INTEGER` | 필수 | 최근 관측 시각, UTC unix 초 |
| `events_total` | `INTEGER` | 필수, 기본값 `0` | 누적 관측 이벤트 수 |

## 키·관계·운영

| 항목 | 내용 |
|---|---|
| 기본 키 | `vendor` |
| 관계 | 다른 테이블과 외래 키 관계 없음 |
| 보존 기준 | `last_seen`이 400일 컷오프보다 오래된 행을 직접 삭제 |
| 조회 용도 | Settings의 벤더 연결 상태와 누적 이벤트 표시 |

## 참고용 DDL

```sql
CREATE TABLE vendors (
  vendor       TEXT PRIMARY KEY,
  first_seen   INTEGER NOT NULL,
  last_seen    INTEGER NOT NULL,
  events_total INTEGER NOT NULL DEFAULT 0
);
```
