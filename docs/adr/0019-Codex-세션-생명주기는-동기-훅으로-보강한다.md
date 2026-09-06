# 0019. Codex 세션 생명주기는 동기 훅으로 보강한다

## Status

Accepted — 부분 대체: [ADR 0020](0020-세션-시작-훅을-유휴-판정의-활동-바닥값으로-쓴다.md)이 `last_activity_at` 갱신 결정을 대체한다. 나머지 결정은 유효하다.

## Context

OTel 유휴 마감만으로는 데몬 재시작 전 세션과 정상 종료 시점을 즉시 알 수 없다. Codex는
`SessionStart`·`SessionEnd` 훅을 제공하지만 HTTP handler는 없어 command 실행이 필요하다.

## Decision

- 로컬 배선 시 Codex의 `SessionStart`·`SessionEnd`에 동기 command hook을 설치한다.
- command는 설치 중인 Pulsemetry 바이너리의 **절대 경로**와 숨은 `hook codex` 명령으로
  stdin을 인증된 loopback HTTP에 전달한다. 사용자 PATH는 변경하지 않는다.
- Codex가 허용하는 SessionEnd 최대 예산 3초를 handler timeout으로 쓰되, 내부 HTTP 왕복은
  750ms로 제한해 종료를 오래 붙잡지 않는다.
- 시작 훅은 `started_at`의 최솟값을 보존하고 `ended_at`을 NULL로 만들어 재개한다.
- **대체됨(ADR 0020)** — 종료 훅은 `ended_at`을 기록한다. `last_activity_at`은 OTel 이벤트만 갱신한다.
- 훅 누락은 `last_activity_at` 기반 SQL 유휴 스윕이 복구한다.
- 예약 command 서명(`type=command`, Pulsemetry 실행 파일 + `hook codex`)과 일치하는 개별
  handler만 관리하고 사용자 훅은 보존한다. 구버전 상대 명령도 제거 대상으로 인정한다.

## Alternatives Considered

### A. OTel 유휴 마감만 사용한다

- 장점: 설정 변경이 없다.
- 단점: 정상 종료 반영이 늦고 데몬 재시작 경계에서 메모리 상태가 사라진다.
- 탈락 이유: Codex가 제공하는 명시적 생명주기 신호를 버린다.

### B. 훅 command가 SQLite를 직접 연다

- 장점: 데몬 HTTP 경로가 없다.
- 단점: 종료 훅의 짧은 예산 안에 DB 초기화가 들고 데몬과 쓰기 주체가 갈린다.
- 탈락 이유: 로컬 저장소 소유자는 데몬 하나로 유지한다.

## Consequences/Tradeoffs

### Positive

- 정상 시작·재개·종료가 즉시 화면에 반영된다.
- 별도 테이블 없이 기존 `sessions`를 사용한다.

### Negative

- 훅마다 짧은 프로세스 실행과 loopback 요청이 생긴다.
- 강제 종료·신뢰 미승인·데몬 중단 때 훅이 빠질 수 있어 SQL 스윕을 함께 유지해야 한다.

## Follow-up

- 실제 설치에서 사용자 레벨 Codex 훅의 최초 신뢰 상태를 검증한다.

## Acceptance Criteria

- 기존 사용자 훅을 보존하며 설치·재설치·제거가 멱등이다.
- 시작 훅은 세션을 열고 종료 훅은 닫으며, 재개 훅은 같은 세션을 다시 연다.
- 데몬이 없거나 요청이 실패해도 Codex command hook은 성공 종료한다.

## References

- ADR 0005 — 이벤트를 세션으로 조립하고 세션을 1급 엔티티로 둔다
- `internal/store/lifecycle.go`
- `internal/config/codex.go`
