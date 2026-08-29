# 0003. 원문과 tool details 를 로컬에만 보관하고 상위 전달에서 제거한다.

## Status
Accepted — 부분 대체: [ADR 0006](0006-로컬-파이프라인을-opt-out으로-전환하고-OTel-설정을-고정한다.md) 이 "local enable 이 두 게이트를 1 로 강제한다" 는 배선 방식을 고정 로컬 프로필로 대체하고, [ADR 0008](0008-로컬-데이터를-400일간-보존한다.md) 이 원문 30일 보존 조항을 대체한다. 로컬 보관 기본 ON·상위 전달 제거 결정과 절대 저장 금지 목록은 그대로 유효하다.

> 원문을 30일 보존한다는 조항과 완화책은 [ADR 0008](0008-로컬-데이터를-400일간-보존한다.md)로
> 대체되었다. 로컬 저장과 상위 전달 제거 결정은 계속 유효하다.

## Context
`docs/installation-architecture.md` §4.6 「민감 정보 수집 위험」은 MVP 기본값을 이렇게 고정해 두었다.

```text
프롬프트 원문             OFF
assistant 응답 원문       OFF
tool input/output         OFF
파일 내용                 OFF
명령어 상세               OFF 또는 최소화
사용자 이메일             필요성 검토 전 OFF
```

그리고 보호를 **클라이언트 설정 비활성화 + Collector redaction + Adapter allowlist** 세 계층으로 하라고 못박는다.
"클라이언트 설정만 믿으면 사용자가 설정을 변경했을 때 민감 데이터가 들어올 수 있으므로 서버 측 제거가 반드시 필요하다."

이 정책은 `internal/contract/manifest.go` 의 `Privacy` 구조체(기본 전부 false)로 표현되고,
이 정책은 `internal/contract/manifest.go` 의 `Privacy` 구조체(기본 전부 false)로 표현되고,
`internal/config/claude.go` 의 `MergeClaude` 가 그것을 그대로 벤더 환경변수로 옮긴다.

```go
"OTEL_LOG_USER_PROMPTS":        boolEnv(m.Privacy.CollectUserPrompts),
"OTEL_LOG_ASSISTANT_RESPONSES": boolEnv(m.Privacy.CollectAssistantResponses),
"OTEL_LOG_TOOL_DETAILS":        boolEnv(m.Privacy.CollectToolDetails),
```

문제는 **화면에 필요한 데이터의 절반이 정확히 이 게이트 뒤에 있다**는 것이다.

| 게이트 | 없으면 못 만드는 화면 |
|---|---|
| `OTEL_LOG_USER_PROMPTS` | 작업 제목·요약 (원문에서 파생) |
| `OTEL_LOG_TOOL_DETAILS` | 파일 변경 목록, 툴 타임라인의 파일명·명령 |

즉 회사 manifest 를 그대로 따르면 Activity 화면이 빈다.
게이트를 로컬에서 켜면 화면은 살지만, 그 순간 **회사가 수집하지 않기로 한 데이터가 로컬 프록시를 통과해
그대로 상위로 흘러간다.** §4.5 가 그린 로컬 agent 구조가 단순 중계라면 이 두 요구는 양립하지 않는다.

## Decision
- `telemetryctl local enable` 은 벤더 설정에서 `OTEL_LOG_USER_PROMPTS` 와 `OTEL_LOG_TOOL_DETAILS` 를 **`1` 로 강제**한다.
  이 강제는 로컬 manifest 사본에서 일어나며, 회사 manifest 원본과 `MergeClaude`/`MergeCodex` 의 시그니처는 건드리지 않는다.
- 그 결과 **원본 바이트 패스스루를 포기한다.** 포워더는 받은 페이로드를 디코드하고,
  **회사 manifest 의 `Privacy` 기준으로 원문·tool details 를 제거한 뒤 재인코딩**해 상위 Collector 로 보낸다.
  어차피 로컬 집계를 위해 디코드하므로 추가 비용은 거의 없다.
- 이로써 **로컬 agent 가 프라이버시 1차 집행 지점**이 된다(ADR 0006 Decision 3 — 회사 manifest 준수는
  전적으로 `internal/forward` 가 집행한다). §4.6 의 "클라이언트 설정에서 비활성화" 계층이
  벤더 설정에서 포워더로 옮겨 온 것이며, 상위 계층(Collector redaction · Adapter allowlist)은 그대로 유지된다.
- 로컬 원문 보관은 **기본 ON, opt-out** 이다(`--no-store-content`). Settings 화면의 「프라이버시 모드」와 대응한다.
  원문은 `event_content` 에 항목당 16KB 캡으로 저장하고 기본 30일 뒤 삭제한다. `purge --content` 로 즉시 지울 수 있다.
- 로컬 저장에도 무제한 허용은 없다. **절대 저장하지 않는 것**을 스키마로 고정한다.
  - 전체 작업 경로 — `project_hash`(sha256) + basename 만 저장
  - `user.email` · `user.id` · `user.account_uuid` · `organization.id`
  - 모든 토큰
  속성은 allowlist 컬럼으로만 받고 catch-all `map[string]string` 을 두지 않는다.
- 회귀를 테스트로 고정한다. 경로가 포함된 골든 픽스처를 protojson·protobuf 양쪽으로 디코드해
  `project_hash` 와 `file_name` 은 채워지되 **전체 경로 문자열이 출력 어디에도 없음**을 단언하고,
  `user.email` · `organization.id` 부재도 단언한다. 포워더는 전 과정 로그를 버퍼로 캡처해 토큰 부재를 단언한다.

## Alternatives
### A. 받은 본문을 그대로 상위로 흘려보낸다 (원본 바이트 패스스루)
- 장점: 가장 단순하고 가장 빠르다. 디코드·재인코딩이 없으니 로컬 프록시가 데이터를 변형하지 않고, 회사 Collector 가 받는 바이트가 직결 시절과 완전히 동일해 상위 파이프라인 회귀 위험이 0 이다.
- 단점: 로컬에서 켠 `OTEL_LOG_USER_PROMPTS` · `OTEL_LOG_TOOL_DETAILS` 의 결과물이 그대로 회사로 간다. §4.6 이 OFF 로 고정한 항목이 클라이언트 설정 변경만으로 수집되는, 그 문서가 명시적으로 경고한 시나리오다.
- 탈락 이유: 회사 정책 위반이다. 사용자가 자기 화면을 보려고 켠 설정이 회사 수집 범위를 조용히 넓히는 구조는 어떤 편의로도 정당화되지 않는다.

### B. manifest 의 `Privacy` 플래그를 로컬에서도 그대로 따른다
- 장점: 정책 표현이 한 곳뿐이라 가장 정직하다. 포워더는 중계만 하면 되고, 로컬과 회사가 보는 데이터가 정확히 같아 "왜 로컬에만 있냐"는 질문이 아예 생기지 않는다. 디스크 증가도 없다.
- 단점: 회사 기본값이 전부 OFF 이므로 프롬프트도 tool details 도 오지 않는다. 작업 제목·요약, 파일 변경 목록, 툴 타임라인의 파일명과 명령이 전부 비고, Activity 화면과 검색 기능이 성립하지 않는다.
- 탈락 이유: 티켓의 산출물이 그 화면들이다. 이 대안은 PROJ-36 을 "Today 카드만 있는 대시보드"로 축소한다.

### C. 로컬 화면용으로 별도 exporter 를 하나 더 띄워 원문만 로컬로 보낸다
- 장점: 회사로 가는 스트림에 원문이 물리적으로 섞이지 않는다. 제거 로직이 필요 없어 실수로 유출될 여지가 구조적으로 없다.
- 단점: 벤더의 OTel 설정은 시그널당 endpoint 를 하나만 받는다. 목적지를 둘로 나눌 설정 표면이 없다(ADR 0001 대안 B).
- 탈락 이유: 구현 문제가 아니라 벤더 설정 표면의 제약이라 우회가 불가능하다.

## Consequences/Tradeoffs
### Positive
- 화면이 필요로 하는 데이터를 확보하면서 회사 수집 범위는 직결 시절과 동일하게 유지된다.
- 프라이버시 집행이 벤더 설정(사용자가 편집 가능)에서 우리 코드(테스트로 고정)로 옮겨 온다. §4.6 이 요구한 "설정만 믿지 않는다"가 클라이언트 쪽에서도 성립한다.
- 원문 보유 주체가 명확해진다. 프롬프트는 그것을 쓴 사람의 기계를 떠나지 않는다.
- 전체 경로 부재를 `.dump | grep '/Users/'` 한 줄로 수동 검증할 수 있을 만큼 규칙이 단순하다.

### Negative
- 포워더가 상위로 보내는 본문의 정확성을 우리가 책임진다. 재인코딩 버그가 곧 데이터 손상이다.
  - 완화: 골든 픽스처 왕복 테스트, protojson·protobuf 양쪽 실행.
- 프라이버시 규칙이 두 벌이 된다 — 로컬 저장 규칙과 상위 전달 규칙. 어느 쪽이 무엇을 지우는지 계속 설명해야 한다.
- 로컬 디스크에 프롬프트 원문이 쌓인다. 파일은 평문이고 디스크 암호화에 의존한다.
  - 완화: 16KB 캡 + 30일 보존 + `--no-store-content` + `purge --content` + `status` 사용량 표시.
- `local enable` 이 사용자 벤더 설정의 두 키를 회사 manifest 와 다른 값으로 바꾼다. 관리 키 목록에 포함시켜 `uninstall` 이 되돌릴 수 있게 해야 한다(§5.2).
- 회사 Collector 가 받는 바이트가 더 이상 벤더가 만든 바이트가 아니다. 상위에서 이상이 보이면 원인 후보에 포워더가 추가된다.

## Follow-up
- **제목·요약 품질 개선**(`title_source='llm'`) — 프롬프트 원문을 외부 모델로 보내는 문제이므로 **별도 프라이버시 검토가 선행**되어야 한다. 이 ADR 은 원문이 로컬을 떠나지 않는다는 전제 위에 서 있고, 그 전제를 깨는 결정은 새 ADR 을 요구한다.
- 상위 전달 제거 대상은 현재 `Privacy` 구조체 여섯 필드에 대응한다. manifest 스키마에 필드가 추가되면 포워더 제거 규칙도 같이 늘려야 한다.
- 이벤트 원문 검색(`content_fts`)의 결과가 GUI 밖으로 나가는 경로(내보내기·공유)는 만들지 않는다. 필요해지면 별도 결정으로 다룬다.
