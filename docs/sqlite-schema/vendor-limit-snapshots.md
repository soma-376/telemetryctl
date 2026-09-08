# `vendor_limit_snapshots`

벤더별 최신 한도 조회 결과를 담는 v1 스키마 테이블이다. 사용 이벤트 이력과 독립된 스냅샷이며,
벤더 ID당 한 행을 둔다.

## 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `vendor` | `TEXT` | 필수 기본 키 | 벤더 ID. `WITHOUT ROWID` 기본 키는 `NULL`을 허용하지 않음 |
| `state` | `TEXT` | 필수 | `available` 또는 `unavailable` |
| `reason` | `TEXT` | 필수, `''` | 조회 불가 사유 코드 |
| `detail` | `TEXT` | 필수, `''` | 조회 상태 상세 설명 |
| `plan` | `TEXT` | 필수, `''` | 벤더 구독 플랜 |
| `windows_json` | `TEXT` | 필수, `'[]'` | 한도 구간 목록. 유효한 JSON 배열만 허용 |
| `extra_json` | `TEXT` | 필수, `'{}'` | 부가 한도 정보. 유효한 JSON 객체만 허용 |
| `observed_at` | `TEXT` | 필수, `''` | 벤더 관측 시각 문자열. 한도 결과의 RFC3339 표현 |
| `checked_at` | `INTEGER` | 필수 | 조회 확인 시각, Unix 초 |

## 키·관계·보존

- 벤더 ID 기본 키로 최신 결과 한 행을 식별한다. 별도 보조 인덱스는 없다.
- 사용 이벤트가 발생하기 전에 한도 조회가 가능하므로 `vendors`를 참조하는 외래 키는 두지 않는다.
- 세션 소유 데이터가 아니므로 세션의 CASCADE 삭제 대상이 아니다.
- 현재 세션 보존 삭제와 원문 삭제 로직은 이 테이블을 삭제하지 않는다. 개발 DB를 재생성하면 초기화된다.
- 이 PR은 저장 테이블을 정의한다. 한도 결과 저장·갱신 경로의 구현은 후속 PR에서 다룬다.
- `observed_at`의 형식과 `checked_at`의 범위는 DDL의 CHECK로 제한하지 않는다.

## 참고용 DDL

```sql
CREATE TABLE vendor_limit_snapshots (
  vendor TEXT PRIMARY KEY,
  state TEXT NOT NULL CHECK (state IN ('available', 'unavailable')),
  reason TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  plan TEXT NOT NULL DEFAULT '',
  windows_json TEXT NOT NULL DEFAULT '[]' CHECK (CASE WHEN json_valid(windows_json) THEN json_type(windows_json) = 'array' ELSE 0 END),
  extra_json TEXT NOT NULL DEFAULT '{}' CHECK (CASE WHEN json_valid(extra_json) THEN json_type(extra_json) = 'object' ELSE 0 END),
  observed_at TEXT NOT NULL DEFAULT '',
  checked_at INTEGER NOT NULL
) WITHOUT ROWID;
```
