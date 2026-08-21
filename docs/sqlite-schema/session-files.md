# `session_files`

세션에서 변경한 파일의 해시·basename과 변경량을 저장한다. 전체 경로는 저장하지 않는다.

기본 400일간 보존하며 부모 `sessions` 행을 지우면 함께 삭제된다.

## 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `session_id` | `TEXT` | 필수 | 파일 변경이 속한 세션 ID |
| `file_path_hash` | `TEXT` | 필수 | 전체 경로 대신 저장하는 경로 해시 |
| `file_name` | `TEXT` | 필수 | 디렉터리를 제거한 파일 basename |
| `file_ext` | `TEXT` | 선택, `NULL` | 파일 확장자 |
| `lines_added` | `INTEGER` | 필수, 기본값 `0` | 파일에 추가된 줄 수 |
| `lines_removed` | `INTEGER` | 필수, 기본값 `0` | 파일에서 제거된 줄 수 |
| `edits` | `INTEGER` | 필수, 기본값 `0` | 파일 편집 횟수 |
| `last_ts` | `INTEGER` | 필수 | 마지막 변경 시각, UTC unix 초 |

## 키·관계·운영

| 항목 | 내용 |
|---|---|
| 기본 키 | (`session_id`, `file_path_hash`) |
| 부모 | `session_id` → `sessions.session_id` |
| 삭제 | 부모 세션 삭제 시 `ON DELETE CASCADE` |
| 저장 형식 | 복합 기본 키를 사용하는 `WITHOUT ROWID` 테이블 |
| 프라이버시 | 전체 경로는 저장하지 않고 해시와 basename만 저장 |
| 정확도 | 파일별 라인 배분은 근사일 수 있지만 합계가 세션 합계를 초과하지 않음 |

## 참고용 DDL

```sql
CREATE TABLE session_files (
  session_id     TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  file_path_hash TEXT NOT NULL,
  file_name      TEXT NOT NULL,
  file_ext       TEXT,
  lines_added    INTEGER NOT NULL DEFAULT 0,
  lines_removed  INTEGER NOT NULL DEFAULT 0,
  edits          INTEGER NOT NULL DEFAULT 0,
  last_ts        INTEGER NOT NULL,
  PRIMARY KEY (session_id, file_path_hash)
) WITHOUT ROWID;
```
