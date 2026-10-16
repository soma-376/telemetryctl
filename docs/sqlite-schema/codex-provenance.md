# Codex 프롬프트 출처 보강 테이블

이 테이블들은 기존 OTel 턴의 출처만 보강한다([ADR 0032](../adr/0032-Codex-프롬프트-출처를-JSONL로-보강하고-단일-워커로-복구한다.md)). JSONL 행에서 새 턴·LLM 호출·토큰을 만들지 않는다. 실행 DDL과 제약의 단일 진실원은 `internal/store/schema.go`의 `schemaSQL`이다. 스키마 버전은 계속 v1이다.

| 테이블 | 키·주요 컬럼 | 역할 |
|---|---|---|
| `codex_turn_provenance` | PK/FK `turn_id`; `label`, `processing_state`, `link_state`, `link_method`, `linked_record_id`, `classifier_version`, `structure_evidence`, `reason`, `checked_at` | 턴당 현재 판정 한 행. 라벨·처리·연결 상태를 별도 축으로 저장한다. `structure_evidence`는 유효한 JSON이다. |
| `codex_pending` | PK/FK `turn_id`; `next_check_at`, `attempts`, `last_error` | 재시작·알림 유실 뒤에도 남는 재조회 대상. `ix_codex_pending_due`가 기한 순 조회를 돕는다. |
| `codex_jsonl_files` | PK `id`; UNIQUE `(path, generation)`; `os_file_id`, `owner_session_key`, `parent_session_key`, `owner_source`, `committed_offset`, `committed_prefix_hash`, `observed_size`, `observed_mod_ns`, `status`, `observed_at` | 파일 소유자와 세대·검증 위치를 보존한다. `status`는 `active`·`rotated`·`superseded`·`quarantined`·`missing`이다. |
| `codex_jsonl_records` | PK `id`; FK `file_id`; UNIQUE `(file_id, start_offset)`; `end_offset`, `record_type`, `event_time`, `message_id`, `turn_id`, `body`, `body_hash`, `completeness`, `completeness_evidence`, `structure_evidence`, `representation_key` | 완성된 JSONL 행의 연결·출처 근거. `event_time`은 UTC Unix 나노초다. |
| `codex_jsonl_errors` | PK `(file_id, start_offset)`; `end_offset`, `error_kind`, `diagnostic`, `observed_at` | 완성된 손상 행과 격리 원인을 남긴다. |
| `codex_content_tombstones` | PK `owner_session_key`; `before_at` | 원문 삭제 뒤 파일 재처리가 이전 본문을 복원하지 못하게 하는 경계. `before_at`은 Unix 나노초다. |
| `codex_worker_state` | 단일 행 `id=1`; `epoch`, `status`, `heartbeat_at`, `job_started_at`, `restart_count`, `reason` | 단일 워커 세대와 상태. `status`는 `stopped`·`running`·`backoff`·`degraded`다. |

`codex_turn_provenance`·`codex_pending`은 턴 삭제에 따라, JSONL 레코드·오류는 파일 세대 삭제에 따라 `ON DELETE CASCADE`로 정리된다. 소유 세션이 400일 보존 정책으로 삭제되면 그 세션의 JSONL 근거와 삭제 표식도 정리한다.

## 저장·복구 경계

- 첫 `session_meta.payload.id`가 파일 owner다. 부모·복사 문맥 또는 파일명으로 owner를 바꾸지 않는다. 파일의 OS ID·owner·세대·저장 위치 검증 정보를 비교한다. 다른 OS 파일 ID로 정상 rotation되면 옛 유효 세대를 `rotated`로 보존하고, rename된 같은 파일은 검증된 ID·체크포인트를 재사용한다. 같은 파일의 truncate·중간 수정 또는 격리 복구는 옛 세대를 `superseded`로 두어 그 근거를 재사용하지 않는다.
- `session_meta.payload.source="exec"`는 CLI 사용자 문맥으로 판정한다. 진행 중 턴의 본문 후보는 그 턴 시작 이상·다음 OTel 턴 시작 미만에만 연결한다. 마지막 열린 턴에는 추정 종료 상한이 없고, 완료 턴은 저장된 종료 초의 마지막 소수 초까지 포함한다. 같은 초에 시작한 두 턴에는 본문 exact·prefix 연결을 금지하되 유효한 명시 ID는 허용한다. 새 OTel 턴이 저장되면 같은 트랜잭션에서 직전 열린 턴을 pending으로 되돌려 새 배타적 상한을 적용한다.
- 완성된 newline 행의 끝 위치까지만 체크포인트를 전진시킨다. 행의 근거·분류 결과 또는 재처리 가능한 pending·오류와 위치를 같은 트랜잭션에 commit한다. 저장 시 워커 `epoch`을 검증한다. 부분 마지막 행은 다음 읽기를 기다린다.
- 파일은 읽기 전용으로 열고 생산자 쓰기를 막는 잠금을 잡지 않는다. 한 행은 최대 4 MiB, 한 배치는 최대 64행·4 MiB다. map 디코드 전에 JSON 구조를 최대 32,768토큰·깊이 64로 제한한다.
- 후보 레코드의 모든 문자열을 합쳐 8 MiB, pending 조회는 최대 64행·행당 근거 256 KiB로 제한한다. 초과 근거를 잘라서 임의 분류하지 않고 `unknown` 재시도와 진단을 남긴다. 파서·보강 캐시의 증가 한도는 64 MiB다.
- 보강은 OTel 저장 commit 후 알림을 받고 DB의 pending을 재조회한다. owner 파일이 없다는 이유나 1시간 경과만으로 unknown을 시스템 사용량으로 확정하지 않는다. 1시간은 재조회 간격이다.

## 원문과 조회

`--no-store-content`에서는 원문을 보관하지 않고 출처 판정에 필요한 비원문 근거만 다룬다. `purge --content`는 `turns.prompt_text`·`events.payload`·`tool_calls.error_message`와 대상 JSONL 본문·해시를 비우고 턴·owner의 삭제 표식을 기록한다. 확정된 비원문 분류 라벨과 전체 사용량은 유지한다. 재처리로 삭제된 원문을 되살리지 않는다.

Codex 사용자 프롬프트는 실제 턴(`turn_index IS NOT NULL`)의 분류가 `client_submitted/finalized`일 때만 센다. `internal_task/finalized`와 `unknown/finalized`는 시스템 사용량이며 pending·retrying·근거 결손과 가상 턴은 미분류 사용량이다. 가상 턴은 분류 행이 있더라도 미분류다. 모든 `llm_calls`의 토큰·비용은 분류와 무관하게 보존한다.
