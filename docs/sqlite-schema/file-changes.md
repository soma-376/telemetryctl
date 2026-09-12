# `file_changes`

도구 호출에서 관측한 파일 생성·수정·삭제·이름 변경을 저장한다.

| 컬럼 | 타입 | 제약 | 설명 |
|---|---|---|---|
| `id` | `INTEGER` | 기본 키 | SQLite rowid 별칭 |
| `tool_call_id` | `INTEGER` | 필수, FK | `tool_calls.id` 참조 |
| `file_path` | `TEXT` | 필수 | 파일 경로. rename이면 새 경로 |
| `operation` | `TEXT` | 필수, CHECK | `create`, `modify`, `delete`, `rename` 중 하나 |
| `renamed_from` | `TEXT` | rename일 때 필수 | rename 이전 경로 |
| `additions`, `deletions` | `INTEGER` | 선택 | 관측된 줄 수. 미관측은 `NULL` |
| `old_hash`, `new_hash` | `TEXT` | 선택 | 변경 전·후 해시 |

`ix_fc_tool(tool_call_id)`이 도구 호출별 조회를 지원한다.

Codex `apply_patch`는 성공한 `codex.tool_result`의 `arguments` 패치를 파싱하여
파일마다 한 행을 만든다([ADR 0023](../adr/0023-Codex-패치를-파일별-변경으로-저장한다.md)).
`Add File`은 create, `Update File`은 modify, `Delete File`은 delete,
`Update File`과 `Move to`의 조합은 rename이다. 한 도구 호출이 여러 파일을 바꾸면
같은 `tool_call_id`를 공유한다. 이 경우 단일 대상인 `tool_calls.target`은 비어 있을 수 있다.

파싱은 로컬 원문 길이 제한 전에 수행한다. 이벤트가 작업 디렉터리를 제공하면 상대 경로를
결합하고, 없으면 수신한 상대 경로를 보존한다.

줄 수는 성공한 호출의 패치에 명시된 편집량이다([ADR 0024](../adr/0024-Codex-패치의-추가와-삭제-줄-수를-저장한다.md)).
생성은 본문의 `+` 줄 수와 삭제 0, 수정·이름 변경은 본문의 `+`·`-` 줄 수를 저장한다.
이름만 바꾸면 양쪽 모두 0이다. 문맥·제어 줄은 제외하고 추가·삭제된 빈 줄은 포함하며,
여러 수정 블록은 파일별로 합산한다. 파일 전체 삭제는 추가 0, 삭제 NULL이다.
이는 작업 트리의 최종 diff나 순증가량이 아니며 화면·세션 합계에 자동으로 더하지 않는다.
해시는 추정하지 않고 NULL로 둔다.

액티비티 상세의 파일별 줄 수 합계도 미관측을 NULL로 보존한다. 화면은 NULL을 `-`로,
관측된 0을 `+0`·`−0`으로 표시한다. 일부 변경만 관측했다면 관측된 값의 합을 표시한다.
입력 없음·불완전한 패치·형식 오류·성공 미확인은 파일 행을 만들지 않으며 데몬의
`파일 변경 추출 생략` 로그에 이유별 건수만 남긴다. 기존 DB의 과거 이벤트는 자동 보정하지 않는다.

Claude Code는 `tool_input`에 파일 경로가 있는 도구만 행을 만든다 — `Edit`은 modify,
`Write`는 create다([ADR 0026](../adr/0026-Claude-편집의-줄-수를-tool_input-원문에서-센다.md)).
줄 수는 같은 `tool_input`의 편집 전후 원문에서 센다. `Write`는 `content`의 줄 수를 추가로
저장하고 삭제는 NULL이다 — 새 파일인지 덮어쓴 것인지 구분할 수 없다. `Edit`은 `old_string`과
`new_string`의 최장 공통 부분수열로 세므로 같은 줄 수를 교체한 편집도 0이 되지 않는다.
`replace_all` 편집, `tool_input`이 없는 이벤트, 계산 상한을 넘는 편집은 NULL이다.

## 벤더에 따라 차지 않는 값

`operation`의 네 값이 모든 벤더에서 나오지는 않는다.

| | `create` | `modify` | `delete` | `rename` |
|---|---|---|---|---|
| Codex (`apply_patch`) | O | O | O | O |
| Claude Code (`Edit`·`Write`) | O | O | — | — |

Claude Code에는 파일을 지우거나 옮기는 도구가 없다. 삭제·이동은 `Bash`로 흘러가고 그 이벤트의
`tool_input`에는 명령 문자열만 있어 대상 경로가 없다. 따라서 행 자체가 만들어지지 않는다.
셸 명령을 파싱하거나 파일시스템을 감시해 이 값을 채우지 않는다 — 이유는 ADR 0026에 있다.
벤더를 가로지르는 집계는 이 비대칭을 감안해야 한다.

`old_hash`·`new_hash`는 어느 벤더에서도 채워지지 않는다. 두 벤더 모두 해시를 주지 않고,
변경 전후 파일 전체를 읽지 않으면 계산할 수 없다.

```sql
CREATE TABLE file_changes (
  id           INTEGER PRIMARY KEY,
  tool_call_id INTEGER NOT NULL REFERENCES tool_calls (id),
  file_path    TEXT NOT NULL,
  operation    TEXT NOT NULL
    CHECK (operation IN ('create', 'modify', 'delete', 'rename')),
  renamed_from TEXT,
  additions    INTEGER,
  deletions    INTEGER,
  old_hash     TEXT,
  new_hash     TEXT,
  CHECK (operation <> 'rename' OR renamed_from IS NOT NULL)
);

CREATE INDEX ix_fc_tool ON file_changes (tool_call_id);
```
