# `event_content`와 `content_fts`

`event_content`는 프롬프트·응답·툴 입력·툴 결과 원문을 로컬에 저장하고, `content_fts`는 본문 검색을
위한 FTS5 external-content 가상 테이블이다. 두 객체와 동기화 트리거를 하나의 계약으로 관리하며
기본 30일간 보존한다.

## `event_content` 컬럼

| 컬럼 | 타입 | 필수 여부·기본값 | 설명 |
|---|---|---|---|
| `id` | `INTEGER` | 필수, 기본 키 | 원문 항목의 대리 키이자 FTS5 rowid |
| `event_id` | `INTEGER` | 필수 | 원문을 생성한 정규화 이벤트 ID |
| `kind` | `TEXT` | 필수 | `prompt`, `response`, `tool_input`, `tool_result` 중 하나 |
| `body` | `TEXT` | 필수 | 최대 16KB로 보관하는 원문 본문 |
| `truncated` | `INTEGER` | 필수, 기본값 `0` | 본문이 저장 상한으로 잘렸는지 여부 |

이벤트 하나가 여러 종류의 원문을 가질 수 있으므로 대리 키 `id`를 두고 (`event_id`, `kind`)를
UNIQUE로 묶는다. 이벤트당 원문 하나만 가정하면 한 로그에 `tool_input`과 `tool_result`가 함께 오는
기본 경로에서 둘 중 하나가 사라진다.

## `content_fts` 구성

| 항목 | 내용 |
|---|---|
| 종류 | FTS5 external-content 가상 테이블 |
| 검색 컬럼 | `body` |
| 원본 테이블 | `event_content` |
| 원본 rowid | `event_content.id` |
| 수명 | `event_content`와 동기화되며 독립 보존 정책 없음 |

## 키·관계·동기화

| 항목 | 내용 |
|---|---|
| 기본 키 | `event_content.id` |
| 고유 제약 | (`event_id`, `kind`) UNIQUE |
| 부모 | `event_id` → `events.id` |
| 삭제 | 부모 이벤트 삭제 시 `ON DELETE CASCADE`; 30일 보존과 `purge --content`는 원문을 직접 삭제 |
| INSERT 트리거 | `event_content_ai`가 새 본문을 `content_fts`에 추가 |
| DELETE 트리거 | `event_content_ad`가 삭제된 본문을 `content_fts`에서 제거 |
| UPDATE 트리거 | `event_content_au`가 이전 본문을 제거한 뒤 새 본문을 추가 |

external-content FTS5는 원본 테이블을 자동 추적하지 않으므로 세 트리거를 모두 유지해야 한다.
저장 API가 이벤트와 원문 목록을 하나의 `store.EventRecord`로 받는 것도 이벤트는 중복으로 거부됐는데
원문만 저장되는 상태를 표현하지 못하게 하기 위해서다.

## 프라이버시·운영

- `tool_input`에는 세션 파일 목록을 만들기 위한 전체 경로가 포함될 수 있다.
- 원문은 로컬에만 남고 상위 Collector 전달 전 제거된다.
- `purge --content`는 원문과 검색 색인만 지우며 이벤트 수치와 롤업은 유지한다.
- 쓰기 연결의 `recursive_triggers=1`이 CASCADE 삭제에서도 FTS5 트리거 실행을 보장한다.

## 참고용 DDL

```sql
CREATE TABLE event_content (
  id        INTEGER PRIMARY KEY,
  event_id  INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  kind      TEXT    NOT NULL,
  body      TEXT    NOT NULL,
  truncated INTEGER NOT NULL DEFAULT 0,
  UNIQUE (event_id, kind)
);

CREATE VIRTUAL TABLE content_fts USING fts5(
  body, content='event_content', content_rowid='id'
);

CREATE TRIGGER event_content_ai AFTER INSERT ON event_content BEGIN
  INSERT INTO content_fts(rowid, body) VALUES (new.id, new.body);
END;

CREATE TRIGGER event_content_ad AFTER DELETE ON event_content BEGIN
  INSERT INTO content_fts(content_fts, rowid, body) VALUES ('delete', old.id, old.body);
END;

CREATE TRIGGER event_content_au AFTER UPDATE ON event_content BEGIN
  INSERT INTO content_fts(content_fts, rowid, body) VALUES ('delete', old.id, old.body);
  INSERT INTO content_fts(rowid, body) VALUES (new.id, new.body);
END;
```
