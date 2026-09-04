# 0017. Codex 세션 제목은 App Server 스레드 이름을 따른다.

## Status

Accepted

## Context

[ADR 0005](0005-세션을-1급-엔티티로-조립.md)는 첫 사용자 프롬프트의 첫 문장을 세션 제목으로
정했다. Claude Code의 OTel 프롬프트에는 실제 사용자 입력이 오지만, Codex의 OTel
`user_prompt`에는 내부 catch-up 요청도 섞여 같은 규칙을 적용할 수 없다.

Codex App Server의 `thread/read` 응답은 사용자에게 보이는 제목인 `thread.name`과 타입이
구분된 `userMessage`를 제공한다. 스레드 이름은 처음에는 없을 수 있고 이후 바뀔 수 있으므로,
한 번 정한 제목을 영구히 잠그면 Codex 화면과 Pulsemetry 화면이 달라진다.

이 결정은 telemetryctl의 로컬 조회·저장에만 영향을 주며 레포 간 계약은 바꾸지 않는다.

## Decision

- Codex 세션 제목의 정본은 App Server `thread/read`의 `thread.name`이다.
- `thread.name`이 없으면 첫 `userMessage`의 첫 문장 60룬을 임시 제목으로 쓴다.
- OTel의 Codex 프롬프트 원문은 제목 생성에 쓰지 않는다.
- 별도 App Server 프로세스의 알림은 다른 Codex 클라이언트에서 일어난 변경을 보장하지
  않으므로 사용하지 않는다.
- 진행 중 Codex 세션은 `thread/read(includeTurns=false)`를 세션별 최소 간격으로 반복하고,
  같은 `(vendor, session_key)` 행의 제목을 최신 `thread.name`으로 교체한다.
- 종료 시에는 쿨다운과 무관하게 최종 조회한다. 이름이 비었거나 조회가 실패하면 수집
  flush에 기대지 않고 워커가 제한된 횟수만큼 지수 간격으로 다시 조회한다.
- `thread.name`만 저장한다. 이름이 없으면 제목을 만들지 않고 NULL 로 둔다 (ADR 0018 로 좁혀짐).
- 제목 재료가 없으면 DB에는 `NULL`을 유지한다. 화면은 벤더명 기반 표시 문구를 사용한다.
- transcript 파일은 제목의 정상 조회 경로로 읽지 않는다.

이 결정은 ADR 0005의 제목 규칙 중 Codex에 적용되는 부분만 대체한다. Claude Code와 다른
벤더의 제목 규칙, 세션을 1급 엔티티로 두는 결정과 유휴 마감 규칙은 계속 유효하다.

## Alternatives Considered

### A. Codex OTel 프롬프트의 첫 문장을 계속 쓴다

- 장점: 별도 프로세스 호출이 없다.
- 단점: 내부 catch-up 요청이 사용자 제목으로 저장될 수 있다.
- 탈락 이유: 사용자 입력과 내부 입력을 구분할 안정적인 필드가 없다.

### B. transcript 파일을 직접 읽는다

- 장점: App Server가 없어도 로컬 기록을 읽을 수 있다.
- 단점: 내부 JSONL 형식·파일 배치·부분 쓰기와 회전에 종속된다.
- 탈락 이유: 같은 데이터를 제공하는 App Server 계약이 있고 별도 파일 감시 파이프라인이 필요 없다.

### C. 첫 App Server 조회 결과를 영구히 유지한다

- 장점: 세션당 호출이 한 번이다.
- 단점: 처음에는 이름이 없을 수 있고 이후 이름 변경을 반영하지 못한다.
- 탈락 이유: `session_key`는 그대로인데 화면 제목만 낡게 남는다.

## Consequences/Tradeoffs

### Positive

- Codex가 사용자에게 보여주는 제목과 Pulsemetry 제목이 같아진다.
- 내부 catch-up 프롬프트를 문자열 패턴으로 판별하지 않는다.
- transcript tail·offset·회전 관리가 필요 없다.

### Negative

- 데몬이 실험적 App Server 프로토콜과 외부 `codex` 프로세스에 의존한다.
- 활동 중 제목 재조회 호출이 추가된다.
  - 세션별 최소 간격을 두고 수집 저장 경로 밖의 단일 워커에서 처리한다.
- App Server가 없거나 로그인되지 않은 경우 제목이 늦게 채워지거나 비어 있을 수 있다.
  - 로컬 수집은 계속하고 화면에서 벤더명 폴백을 표시한다.

## Acceptance Criteria

- `thread.name`이 있으면 `sessions.title`에 저장된다. 없으면 NULL 이다.
- 처음 이름이 없으면 NULL을 유지하고, 이후 조회에서 이름이 생기면 제목이 저장된다.
- 이후 조회에서 이름이 바뀌면 같은 세션 행의 제목이 교체된다.
- Codex OTel 프롬프트는 제목을 만들지 않는다.
- App Server 실패가 OTLP 수집·저장을 막지 않는다.

## References

- [ADR 0005](0005-세션을-1급-엔티티로-조립.md)
- [ADR 0011](0011-Codex-사용-한도는-App-Server를-통해-조회한다.md)
