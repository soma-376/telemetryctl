# 0030. 맥에서 Claude 자격증명을 security CLI로 읽는다

## Status

Proposed

## Context

`internal/vendorlimit` 은 Claude 구독 사용 한도를 `GET /api/oauth/usage` 로 조회한다(ADR 0011 이 Codex 쪽에 세운 것과 같은 축이다). 그 요청에 실을 Bearer 토큰은 Claude Code 가 보관하는 OAuth 자격증명에서 얻는다.

`loadClaudeCredential` 은 `~/.claude/.credentials.json` 만 읽는다. **맥에는 그 파일이 없다.** 맥의 Claude Code 는 자격증명을 로그인 키체인의 `Claude Code-credentials` 항목에 둔다. 최근 버전은 파일을 아예 쓰지 않는 것으로 관측된다. 즉 맥에서 Claude 한도는 "가끔 없음" 이 아니라 **사실상 항상** `ReasonCredentialMissing` 이다. 윈도우·리눅스는 평문 파일이라 영향이 없다.

`claude_credentials.go` 는 키체인을 읽지 않기로 하고 그 이유를 이렇게 적었다 — 남의 도구의 키체인 항목을 읽으면 승인 대화상자가 뜨고, 데몬이 백그라운드에서 그 대화상자를 띄우는 것은 받아들일 수 없다.

**그 전제가 사실과 다르다.** 키체인 항목에는 접근을 허용받은 주체 목록과 파티션 목록이 붙는다. Claude Code 는 이 항목을 `/usr/bin/security` 로 만들고, 그래서 항목의 파티션이 `apple-tool:` 이다. 같은 도구를 거쳐 접근하면 요청 주체가 이미 허용 목록에 있는 애플 도구이므로 **대화상자가 뜨지 않는다.** 대화상자는 Security 프레임워크로 *우리 바이너리 신원* 을 들고 접근할 때 뜬다. 즉 프롬프트 여부를 가르는 것은 "남의 항목인가" 가 아니라 "어느 문으로 들어가는가" 다.

같은 문제를 다루는 공개 구현들도 같은 결론에 있다. `mwgreen/ClaudeUsageBar` 는 *"the CLI creates the item with an ACL that only allows `security` to decrypt it"* 를 이유로 프레임워크 대신 `security` 를 쓴다고 적었고, `diegocp01/top_bar_claude_code_usage` 는 *"That item already trusts `/usr/bin/security`, so there is no prompt to approve"* 라고 명시한다.

결정하지 않으면 맥 사용자의 한도 화면에서 Claude 칸은 영구히 비어 있고, 화면에는 이유도 남지 않는다.

## Decision

1. **맥에서 자격증명 파일이 없으면 `security` 로 읽는다.** 조회 순서는 파일 → `security` 다. 윈도우·리눅스는 파일만 본다.

   ```
   /usr/bin/security find-generic-password -s "Claude Code-credentials" -w
   ```

2. **읽기 전용이다.** 어떤 경로로도 이 항목에 쓰지 않는다. 쓰기는 항목의 파티션 목록을 우리 신원으로 교체해 Claude Code 본체가 매 실행마다 로그인 암호를 묻게 만든다.

3. **토큰을 갱신하지 않는다.** 만료된 토큰은 조회 실패로 떨어뜨리고, 화면은 `claude` 를 한 번 실행하면 갱신된다고 안내한다. refresh token 을 쓰는 순간 2번의 쓰기 금지와 충돌한다.

4. **읽은 토큰은 `expiresAt` 까지만 메모리에 둔다.** 디스크에도, 우리 키링(`internal/credential`)에도 복제하지 않는다. 캐시가 살아 있는 동안은 키체인을 다시 부르지 않는다.

5. **실행에 시간 상한을 건다.** 상한을 넘으면 그 벤더는 unavailable 로 처리하고 갱신 주기를 막지 않는다.

6. **출력을 로그에 남기지 않는다.** stdout 전체가 곧 토큰이다. 파싱 실패 시에도 원문 조각을 오류 메시지에 싣지 않는다 — `claude_credentials.go` 가 파일 경로에 대해 이미 지키는 규율을 그대로 적용한다.

## Constraints

- `security` 는 애플이 OS 와 함께 제공한다. 우리가 설치하거나 버전을 고를 수 없다.
- 데몬은 CGO 없이 빌드돼야 한다(ADR 0002, `check:cli:nocgo`). Security 프레임워크 직접 호출은 선택지가 아니다.
- 로그인 키체인이 잠겨 있으면 읽기가 실패한다. 로그인 직후 자동 해제되는 것이 기본값이지만 보장은 아니다.
- 항목 이름 `Claude Code-credentials` 와 저장 형식 `{"claudeAiOauth": {...}}` 는 벤더가 정한다.

## Alternatives Considered

### A. Security 프레임워크로 읽되 GUI 프로세스에서 수행한다

- 장점: 우리 앱 신원으로 접근하므로 승인 범위가 우리 앱으로 좁다.
- 단점: 대화상자가 실제로 뜬다. 프레임워크 경로는 항목의 허용 목록에 없기 때문이다. 게다가 Claude Code 가 토큰 갱신 시 항목을 다시 쓰면 받아둔 승인이 초기화돼 반복해서 뜬다(`steipete/CodexBar` 가 이 문제로 기능을 철회했다가 옵트인으로 되돌렸다). CGO 가 필요해 데몬에 둘 수 없고, 그 결과 GUI 가 떠 있을 때만 한도를 알 수 있어 트레이가 비게 된다.
- 탈락 이유: 프롬프트를 없애지 못하면서 구조만 복잡해진다.

### B. `claude` CLI 를 띄워 `/usage` 화면을 읽는다

- 장점: 자격증명을 우리가 만지지 않는다.
- 단점: `/usage` 에는 비대화형 경로가 없어 TUI 를 PTY 로 몰아야 한다. ANSI 제거, 커서 위치 질의 응답, 렌더될 때까지 주기적 입력, 신뢰 프롬프트 자동 응답, 프로세스 트리 종료, 세션 아티팩트 정리가 전부 필요하다. 조회 한 번에 수백 MB 짜리 바이너리를 띄운다.
- 탈락 이유: 유지비가 조회 대상의 가치를 넘는다. 화면 레이아웃은 JSON 필드명보다 자주, 더 조용히 바뀐다.

### C. 맥에서는 Claude 한도를 지원하지 않는다

- 장점: 비용 0. 남의 자격증명을 읽지 않는다.
- 단점: 주력 개발 환경에서 기능의 절반이 빈다.
- 탈락 이유: 프롬프트 없이 읽을 방법이 있으므로 감수할 이유가 사라졌다.

### D. 조직의 Admin API 키로 서버에서 조회한다

- 장점: 로컬 자격증명 접근이 전혀 없다.
- 단점: `/v1/organizations/cost_report` 와 `usage_report/messages` 는 API 사용량과 비용이다. 구독의 5시간·주간 한도 창은 그 응답에 없다.
- 탈락 이유: 다른 질문에 대한 답이다. 사용량·비용 축에서는 따로 검토할 가치가 있다.

## Consequences/Tradeoffs

### Positive

- 맥에서 Claude 한도가 채워진다. 지금은 빈 값이다.
- 승인 대화상자가 없다. 주기 갱신과 수동 갱신을 구분할 필요가 없어져 `Refresher` 가 단순해진다.
- 조회가 데몬에 남는다. GUI 실행 여부와 무관하고 트레이도 채워진다.
- CGO 가 필요 없어 ADR 0002 를 건드리지 않는다.
- 플랫폼 분기가 자격증명 읽기 한 곳에 갇힌다. 조회·파싱·저장 경로는 그대로다.

### Negative

- **우리는 다른 제품의 자격증명을 읽는다.** 그 항목이 `apple-tool:` 파티션이라 같은 사용자 계정의 어떤 프로세스든 이미 읽을 수 있는 상태이고 우리가 노출을 늘리지는 않지만, **우리 기능이 그 사실에 기대고 있다**는 점을 기록해 둔다.
- 토큰이 데몬 프로세스 메모리에 들어온다. 로그나 오류 메시지로 새면 그대로 유출이다. 6번 규칙과 누출 테스트로 막는다.
- 관측에 기반한다. Claude Code 가 항목 이름·저장 형식·생성 방식을 바꾸면 조용히 unavailable 로 떨어진다. 틀린 숫자를 보여주는 것보다는 낫다는 `claude.go` 의 기존 원칙을 따른다.
- 만료 토큰에 대한 자동 복구가 없다. 사용자가 `claude` 를 실행해야 한다. 3번의 대가다.
- `security` 실행 비용이 조회마다 붙는다. 4번의 메모리 캐시로 토큰 수명당 1회로 줄인다.

## Follow-up

- **다중 프로필** — `CLAUDE_CONFIG_DIR` 를 쓰면 `Claude Code-credentials-<sha256 앞 8자>` 형태의 별도 항목이 생긴다. 이번에는 기본 항목만 읽는다. 다계정 요구가 생기면 항목 열거를 검토한다.
- **사용자 차단 수단** — 이 읽기를 끌 수 있어야 하는지. 프롬프트가 없어 사용자에게 보이지 않는 동작이므로, 설정 노출 여부를 프라이버시 관점에서 판단한다.
- **프라이버시 문서** — 데몬이 맥에서 Claude Code 자격증명을 읽는다는 사실을 설치·프라이버시 문서에 명시할지.
- **Admin API** — 대안 D 를 사용량·비용 축에서 다시 본다. 한도와는 별개 결정이다.

## Acceptance Criteria

- 맥에서 `~/.claude/.credentials.json` 이 없는 상태로 한도 조회가 성공한다.
- 조회 중 어떤 승인 대화상자도 뜨지 않는다.
- `security` 경로를 타는 캐너리 토큰이 로그·오류 문자열에 나타나지 않음을 `internal/vendorlimit` 누출 테스트가 확인한다.
- `task check:cli:nocgo` 가 통과한다.
- 항목이 없거나 키체인이 잠긴 환경에서 데몬이 실패하지 않고 해당 벤더만 unavailable 로 떨어진다.

## References

- ADR 0002 — CGO 없는 빌드
- ADR 0011 — Codex 사용 한도는 App Server 를 통해 조회한다
- ADR 0014 — 한도 갱신 억제를 등급별 쿨다운으로 나눈다
- `internal/vendorlimit/claude.go` — 엔드포인트·헤더·응답 가정의 단일 보관소
- `internal/vendorlimit/claude_credentials.go` — 이 ADR 이 대체하는 "키체인을 뒤지지 않는다" 주석의 위치
- [steipete/CodexBar](https://github.com/steipete/CodexBar) — 프레임워크 경로의 반복 프롬프트 문제와 옵트인 철회
- [mwgreen/ClaudeUsageBar](https://github.com/mwgreen/ClaudeUsageBar) · [diegocp01/top_bar_claude_code_usage](https://github.com/diegocp01/top_bar_claude_code_usage) — `security` 경로와 파티션 목록 손상 사례
