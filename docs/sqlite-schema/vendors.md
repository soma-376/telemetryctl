# `vendors`

Claude Code, Codex 같은 제품 단위 벤더의 관측 범위와 상태를 저장한다.

| 컬럼 | 타입 | 제약 | 설명 |
|---|---|---|---|
| `vendor` | `TEXT` | 기본 키 | 벤더 식별자 |
| `first_seen` | `INTEGER` | 필수 | 최초 관측 시각 |
| `last_seen` | `INTEGER` | 필수 | 최근 관측 시각 |
| `status` | `TEXT` | 필수, CHECK | `enabled`, `disabled`, `error` 중 하나 |

`sessions.vendor_id`가 이 테이블을 참조한다. 삭제 동작은 `NO ACTION`이다.

```sql
CREATE TABLE vendors (
  vendor     TEXT PRIMARY KEY,
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  status     TEXT NOT NULL
    CHECK (status IN ('enabled', 'disabled', 'error'))
);
```
