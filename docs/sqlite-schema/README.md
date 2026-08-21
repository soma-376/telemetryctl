# SQLite 스키마

이 디렉터리는 로컬 데이터베이스의 스키마 계약을 사람이 읽기 쉬운 표로 설명한다. 각 테이블 문서의
DDL은 설명을 검증하고 복사할 때 쓰는 참고 자료다. 스키마를 변경할 때는 이 문서를 먼저 갱신하고,
새 마이그레이션과 구현·테스트를 문서에 맞춰 변경한다. 이미 배포된 마이그레이션은 고치지 않고 다음
버전의 마이그레이션으로 변경한다.

현재 구현은 `internal/store/schema.go`의 스키마 버전 2이며, 데이터베이스 파일은
`<data-dir>/pulsemetry.db`(기본 `~/.pulsemetry/pulsemetry.db`)다. 드라이버는 순수 Go 구현인
`modernc.org/sqlite`이고 드라이버 이름은 `sqlite`다. 데몬이 유일한 쓰기 주체이고 GUI와 CLI는
read-only 연결을 사용한다. CI의 `CGO_ENABLED=0` 빌드는 SQLite 계층이 C 툴체인을 요구하지 않는다는
선택을 검증한다.

## 문서 목록

| 문서 | 역할 | 기본 보존 |
|---|---|---|
| [`meta`](meta.md) | 스키마 버전과 설치·보존 메타데이터 | 삭제 대상 아님 |
| [`sessions`](sessions.md) | 화면의 중심이 되는 세션별 합계 | 400일 |
| [`turns`](turns.md) | 세션 안에서 프롬프트로 구분한 턴별 합계 | 400일, 세션 삭제 시 CASCADE |
| [`session_phases`](session-phases.md) | 연속된 턴을 묶은 세션 단계 분류 결과 | 400일, 세션 삭제 시 CASCADE |
| [`session_files`](session-files.md) | 세션별 파일 변경량 | 400일, 세션 삭제 시 CASCADE |
| [`tool_events`](tool-events.md) | 세션의 툴 실행 타임라인 | 30일 |
| [`mcp_session_usage`](mcp-session-usage.md) | 세션별 MCP 서버 사용량 | 400일, 세션 삭제 시 CASCADE |
| [`vendors`](vendors.md) | 벤더 연결 상태와 누적 이벤트 수 | 400일 |
| [`events`](events.md) | allowlist 속성만 담는 정규화 이벤트 | 30일 |
| [`event_content`와 `content_fts`](event-content.md) | 로컬 원문과 FTS5 검색 인덱스 | 30일, 이벤트 삭제 시 CASCADE |
| [`rollup_hourly`](rollup-hourly.md) | 시간·차원별 집계 | 400일 |

`content_fts`는 독립 도메인 테이블이 아니라 `event_content.body`의 external-content FTS5 인덱스이므로
`event-content.md`에서 트리거와 함께 다룬다.

## 공통 시간 단위

| 범위 | 단위 | 의미 |
|---|---|---|
| `events.ts` | UTC unix 나노초 | 원본 이벤트 시각 |
| `events.hour` | UTC unix 나노초 | `events.ts`를 UTC 정시로 내린 값 |
| `rollup_hourly.hour` | UTC unix 초 | 시간 단위 집계 버킷의 시작 시각 |
| 그 외 시각 컬럼 | UTC unix 초 | 세션·툴·파일·벤더·메타데이터 시각 |

## 연결 PRAGMA

쓰기 연결은 모든 커넥션에 적용되도록 DSN에 다음 PRAGMA를 넣는다.

| PRAGMA | 값 | 목적 |
|---|---:|---|
| `journal_mode` | `WAL` | 데몬 쓰기와 GUI 읽기의 병행 |
| `busy_timeout` | `5000` ms | 잠금 경합 시 즉시 실패 방지 |
| `foreign_keys` | `1` | 외래 키와 `ON DELETE CASCADE` 활성화 |
| `recursive_triggers` | `1` | CASCADE 삭제 시 FTS5 동기화 트리거 실행 |
| `synchronous` | `NORMAL` | 텔레메트리 쓰기 지연과 내구성의 균형 |

읽기 연결은 `mode=ro`와 `busy_timeout(5000)`을 사용한다. PRAGMA는 커넥션별 설정이므로 연결 후
한 번 실행하는 방식으로 옮기면 안 된다.

## 보존 계층

| 계층 | 기본 보존 | 직접 삭제 | 연쇄 삭제 | 삭제 후 화면 동작 |
|---|---:|---|---|---|
| 이벤트 | 30일 | `event_content`, `events`, `tool_events` | `events` 삭제 시 남은 `event_content` | 세션 합계와 파일 목록은 보이고 원문과 툴 타임라인은 비어 있음 |
| 세션 | 400일 | `sessions`, `rollup_hourly`, `vendors` | `turns`, `session_phases`, `session_files`, `mcp_session_usage`, 남은 `tool_events` | 세션과 장기 집계가 화면에서 제거됨 |

세션 보존 기준은 `started_at`이 아니라 `last_event_at`이다. 전체 prune은 단일 트랜잭션이며, 실패하면
부분 삭제 없이 다음 주기에 재시도한다. `event_content`는 FTS5 동기화를 보장하기 위해 `events`보다
먼저 직접 삭제한다.

## 마이그레이션 규칙

`meta.local_schema_version`을 기준으로 `internal/store/migrate.go`의 마이그레이션을 버전 순서대로
적용한다. 마이그레이션 하나는 트랜잭션 하나다.

버전 2는 `turns`와 `session_phases`를 만들고 `tool_events.turn_index`를 추가한다. 기존 행은
turn 경계를 추측해 백필하지 않으므로 `tool_events.turn_index`가 `NULL`로 남는다.

| 순서 | 작업 | 규칙 |
|---:|---|---|
| 1 | 문서 변경 | 컬럼 표, 관계, 운영 설명과 참고용 DDL을 먼저 갱신 |
| 2 | 마이그레이션 추가 | `migrations` 끝에 직전보다 1 큰 버전을 추가 |
| 3 | 구현·테스트 변경 | 변경 DDL을 실행 순서대로 넣고 코드와 테스트를 문서에 맞춤 |
| 4 | 기존 버전 보존 | 배포된 마이그레이션은 수정하지 않고 잘못된 DDL도 새 버전에서 교정 |
