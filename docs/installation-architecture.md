# Codex · Claude Code OTel 설치 아키텍처

## 1. 문서 목적

이 문서는 개인 개발자에게 한 줄 설치 명령어를 제공하여 Codex와 Claude Code의 OpenTelemetry 설정을 자동으로 적용할 때 필요한 설계 사항을 정리한다.

목표 설치 경험은 다음과 같다.

```powershell
irm "https://get.your-service.com/windows?code=<초대코드>" | iex
```

개발자는 위 명령어 한 줄만 실행하고, 설치 스크립트가 다음 작업을 자동으로 수행한다.

1. 초대 코드로 enroll 호출 (코드가 곧 자격증명이다 — 이 흐름에는 로그인이 없다)
2. 소속 회사 확인과 대상 멤버 `invited → active` 전환
3. 설치 자격 봉투 수신 (`installation_id` · `installation_token` · `telemetry_token`)
4. Codex 전역 OTel 설정 적용
5. Claude Code 전역 OTel 설정 적용
6. 기존 설정 충돌 및 백업 처리
7. Collector 연결 상태 확인
8. 데몬 자동 실행 등록 (macOS·리눅스)

사용자 **회원가입·로그인은 이 설치 흐름과 별개로 진행되는 다른 흐름**이다. §3.2 를 본다.

---

## 2. 전체 설치 구조

```text
개발자
  │
  │ irm "https://get.your-service.com/windows?code=<초대코드>" | iex
  ▼
설치 스크립트
  │
  ├─ 바이너리 내려받기 (GET /bin/{filename})
  ├─ Enrollment API 호출 (초대 코드가 자격증명 — 이 흐름에 로그인 없음)
  ├─ 설치 자격 봉투 수신 (installation_token · telemetry_token)
  ├─ 기존 설정 검사 및 백업
  ├─ Codex config.toml 수정
  ├─ Claude Code settings.json 수정
  └─ 연결 테스트
  │
  ▼
Codex / Claude Code
  │
  │ OTLP/HTTP + HTTPS
  ▼
Authentication Gateway
  │
  ├─ installation token 검증
  ├─ tenant_id 결정
  ├─ rate limit
  └─ 요청 크기 제한
  │
  ▼
OpenTelemetry Collector
  │
  ├─ 민감 정보 제거
  ├─ 이벤트 필터링
  ├─ 메모리 제한
  └─ batch 처리
  │
  ▼
Adapter → Enrichment → ClickHouse
```

위 그림은 **설치 흐름**만 그린 것이다. 사용자 회원가입·로그인은 여기에 없는 별개 흐름이며
§3.2 (b) 가 다룬다.

---

# 3. 회사·사용자별 변수 처리

## 3.1 기본 원칙

회사 Collector endpoint, 인증 토큰, 활성화할 signal과 같은 값은 한 줄 설치 명령어에 직접 넣지 않는다.

다음과 같이 복잡한 명령어를 사용자에게 제공하지 않는다.

```powershell
install.ps1 -Endpoint "https://telemetry.company.com" -Token "..." -TenantId "..."
```

대신 공통 설치 명령어를 사용한다.

```powershell
irm "https://get.your-service.com/windows?code=<초대코드>" | iex
```

설치 스크립트가 초대 코드로 enroll 을 호출해 회사 manifest 와 이 설치의 자격을 받아온다.
이 흐름에는 로그인 단계가 없다 — 사용자 로그인은 별개 흐름이며 §3.2 (b) 가 다룬다.

---

## 3.2 설치 흐름과 로그인 흐름은 별개다

`pulsemetry-backend` ADR 0007(`Accepted`) 이 둘을 나눠 적고 있다 —
「사용자가 telemetryctl 를 설치하는 플로우」와 「**(동시에)** 사용자가 telemetryctl 인증을
위하여 회원가입을 하는 플로우」. enroll 은 로그인을 기다리지 않고, 로그인은 enroll 의
자격증명을 쓰지 않는다.

### (a) 설치·enroll 흐름 — 현재 구현됨

**enroll 엔드포인트에는 인증이 없다.** 초대 코드 자체가 자격증명이다
(backend 명세 §2 엔드포인트 표, §4.2).

```text
1. 관리자가 POST /v1/invitations 로 초대 코드를 발급한다 (X-Admin-Token 인증)
2. 사용자가 초대 코드가 실린 한 줄 설치 명령을 실행한다
     irm "<server>/windows?code=<초대코드>" | iex
3. 서버가 코드를 정규식으로만 검증하고(DB 조회 없음 — 코드 탐색 오라클 방지)
   코드가 박힌 부트스트랩 스크립트를 내려준다
4. 스크립트가 OS·아키텍처를 판별해 GET /bin/{filename} 으로 바이너리를 받는다
5. 스크립트가 `enroll --invite <코드> --server <주소>` 를 실행한다
6. 서버가 초대 코드를 원자적으로 소비하고 installation 을 만든다
   대상 멤버가 suspended 면 403 으로 끊고 롤백한다 — 코드는 살아 있다
7. enroll 성공이 대상 멤버를 invited → active 로 전환한다
   이 전환이 없으면 auth-proxy 가 이후 모든 텔레메트리를 401 로 막는다
8. 서버가 봉투를 내려준다 — {installation_id, installation_token, telemetry_token, manifest}
9. 클라이언트가 로컬 파이프라인을 배선한다
   벤더 설정에는 로컬 ingest 토큰만 들어가고, 회사 telemetry token 은 OS 키링에 저장된다
10. 데몬 자동 실행을 best-effort 로 등록한다 (macOS·리눅스, **이 레포** ADR 0007)
```

**설계에 있으나 아직 없는 두 단계.** `pulsemetry-backend` ADR 0007 Context 는 1번과 2번 사이에 **초대 링크가 담긴
메일 발송**과 **OS 를 감지해 설치 명령어를 안내하는 페이지**를 둔다. backend 명세 §1 이
"초대 이메일 발송" 을 범위 밖으로 두고 §2 엔드포인트 표에도 안내 페이지가 없어서, 지금은
관리자가 코드를 직접 전달한다.

초대 코드가 설치 명령 URL 에 노출되는 것은 **알고 수용한 위험**이다. 코드는 일회성이고
enroll 이 원자적으로 소비하며, 서버는 코드의 존재 여부를 응답으로 알려 주지 않는다.

### (b) 회원가입·로그인 흐름 — 채택됐으나 미구현

`pulsemetry-backend` ADR 0007 이 정한 흐름이다. **아직 구현이 없다** — backend 명세 §1 이
"사용자 로그인" 을 범위 밖으로 둔다. 범위 밖이라는 것은 설계에 없다는 뜻이 아니라
**그 서버 명세가 다루지 않는다**는 뜻이다.

```text
1. 관리자가 대시보드에서 사용자 정보를 등록하고 초대한다
2. 초대 링크가 담긴 메일이 발송된다
3. 사용자가 회원가입 링크로 이동해 id + pw + 초대 코드를 입력한다
4. 회원가입이 끝나면 (a) 대로 CLI 를 내려받는다
5. CLI 설치 후 사용자가 로그인한다 — 웹 페이지를 띄워 로그인하고
   콜백 URL 로 CLI 에 AT 와 RT 를 전달한다
   (Codex·Claude Code 를 CLI 에서 로그인하는 방식과 같다)
6. CLI 가 해당 사용자가 소속된 부서의 manifest 를 적용한다
```

여기서 쓰는 토큰은 설치 자격증명(`pit_`·`ptt_`)과 성격이 다르다.

| | 형태 | 담는 것 | 폐기 |
|---|---|---|---|
| AT | 단수명 **JWT** | tenant · member · role + **적용 중인 manifest revision** | 만료로 자동 |
| RT | DB 에 저장하는 **불투명 토큰** | — | 사용 시 회전. 서버가 폐기할 수 있다 |

manifest 재동기화 응답이 **새 AT·RT 를 함께 내려준다.** manifest 갱신과 토큰 재발급이 한 응답,
한 트랜잭션이다. 새 토큰이 곧 manifest 적용이 끝났다는 증거이고, 적용에 실패한 클라이언트는
낡은 토큰을 든 채 계속 거부된다(fail-closed).

**아직 정해지지 않은 것** — 전부 ADR 0007 Follow-up 에 있고, 이 레포와 함께 정하기로 돼 있다.

- 콜백 주소 규칙과 PKCE 적용 여부 (별도 ADR 로 미뤄져 있다)
- AT 클레임 스키마(발급자·수명·revision 필드명)와 재동기화 응답 봉투
- 데몬 → 서버 구간이 사용자 AT 를 실을지, 지금처럼 installation 귀속 `telemetry_token` 을
  유지할지. **현재는 `telemetry_token` 이다.**
- fail-closed 의 한계 — 클라이언트에서 새 AT 저장과 manifest 반영은 원자적이지 않다.
  반영 완료 후에만 새 AT 를 저장하는 프로토콜을 계약으로 정의해야 한다.

---

## 3.3 서버가 반환하는 봉투와 Manifest

기계 판독 원본은 `contracts/enrollment-envelope.schema.json` 과
`contracts/enrollment-manifest.schema.json` 이다. 아래는 그 사본이 아니라 읽기용 예시다.

**자격은 manifest 밖에 둔다(봉투 분리).** `installation_id` 와 두 토큰은 "설정" 이 아니라 이
설치의 자격이다. 클라이언트는 `DisallowUnknownFields` 로 파싱하고 그 설정이 중첩 manifest 까지
적용되므로, **manifest 안에 봉투 필드가 하나라도 있으면 설치가 그 자리에서 실패한다.**

```json
{
  "installation_id": "ins_01JABC",
  "installation_token": "pit_...",
  "telemetry_token": "ptt_...",
  "manifest": {
    "schema_version": 1,
    "config_revision": 12,

    "otlp": {
      "endpoint": "https://telemetry.company.com",
      "protocol": "http/protobuf",
      "compression": "gzip",
      "timeout_ms": 10000
    },

    "signals": {
      "logs": true,
      "metrics": true,
      "traces": false
    },

    "privacy": {
      "collect_user_prompts": false,
      "collect_assistant_responses": false,
      "collect_tool_details": false,
      "collect_tool_content": false,
      "collect_user_email": false,
      "collect_raw_api_bodies": false
    },

    "repository_allowlist": [],
    "resource_attributes": {
      "deployment.environment": "production"
    }
  }
}
```

**토큰이 둘인 이유**는 역할이 다르기 때문이다.

| 토큰 | 접두사 | 저장 위치 | 용도 | 교체 |
|---|---|---|---|---|
| `installation_token` | `pit_` | OS 키링 | 이 설치의 장기 신원. 재발급 요청의 근거 | 하지 않는다 |
| `telemetry_token` | `ptt_` | OS 키링 (데몬이 상위 전송 시 `Authorization` 에 주입) | 텔레메트리 전송 | 언제든 재발급 |

`telemetry_token` 은 **벤더 설정 파일로 나가지 않는다.** enroll 이 로컬 파이프라인을 배선하면서
Codex·Claude 설정에는 로컬 ingest 토큰이 들어간다(§4.5 의 평문 노출 위험이 여기서 줄어든다).

`config_revision` 은 서버가 저장된 `manifests.version` 으로 덮어써서 내려준다. tenant 당 활성
manifest 는 최대 하나이고, 활성 manifest 가 없으면 enroll 은 409 `manifest_not_configured` 다.

---

## 3.4 변수 범위

### 회사 단위 변수

해당 회사의 모든 개발자에게 동일하게 적용되는 값이다.

| 변수 | 역할 |
|---|---|
| `otlp_endpoint` | telemetry를 전송할 Collector 주소 |
| `otlp_protocol` | `http/protobuf` 또는 gRPC 등 전송 방식 |
| `enabled_signals` | logs, metrics, traces 활성화 여부 |
| `privacy_policy` | 프롬프트 및 도구 내용 수집 여부 |
| `repository_allowlist` | 수집을 허용할 회사 저장소 |
| `custom_ca` | 사내 인증서 사용 여부 |
| `deployment_environment` | production, staging 등의 환경 구분 |
| `request_timeout` | telemetry 전송 제한 시간 |
| `compression` | gzip 등 압축 방식 |

셀프호스트 고객 예시:

```text
https://telemetry.customer-a.internal
```

SaaS 고객 예시:

```text
https://ingest.your-service.com
```

SaaS에서는 공통 endpoint를 사용하되, 인증된 installation token으로 tenant를 구분한다.

---

### 설치 단위 변수

PC 또는 설치 환경마다 달라지는 값이다.

| 변수 | 역할 |
|---|---|
| `installation_id` | 설치 단위 식별자 |
| `installation_token` | OTLP 전송 인증용 토큰 |
| `device_id` | 장치 구분이 필요한 경우 사용 |
| `config_revision` | 현재 적용된 설정 버전 |
| `installer_version` | 설치 프로그램 버전 |
| `installed_tools` | Codex, Claude Code 설치 여부 |
| `last_verified_at` | 마지막 연결 확인 시간 |
| `operating_environment` | Windows, WSL 등 실행 환경 |

`installation_token`에는 다음 권한만 부여한다.

```text
허용:
- OTLP 데이터 전송
- 자신의 설치 상태 갱신

금지:
- 대시보드 조회
- 조직 설정 변경
- 다른 사용자 또는 설치 조회
- 팀 또는 프로젝트 정보 변경
```

---

### 사용자 단위 변수

다음 값은 가능하면 Codex나 Claude Code 설정 파일에 직접 기록하지 않는다.

```text
user_id
email
team_id
department_id
tenant_id
project_id
```

이 값은 서버에서 신뢰 가능한 정보로 결정한다.

```text
installation_token
→ installation 조회
→ tenant_id 확인
→ 사용자 identity 매핑
→ user_id 결정
→ team_id 및 department_id 연결
→ repository를 기준으로 project_id 연결
```

클라이언트가 직접 보낸 `tenant_id`, `team_id`, `project_id`는 신뢰하지 않는다.

---

## 3.5 MVP 필수 변수

MVP에서는 아래 정도면 충분하다.

봉투(자격)와 manifest(설정)를 나눠서 본다.

| 위치 | 필드 | 범위 | 필수 여부 |
|---|---|---|---:|
| 봉투 | `installation_id` | 설치 | 필수 |
| 봉투 | `installation_token` | 설치 | 필수 |
| 봉투 | `telemetry_token` | 설치 | 필수 |
| manifest | `otlp.endpoint` | 회사 | 필수 |
| manifest | `signals` | 회사 | 필수 |
| manifest | `privacy` | 회사 | 필수 |
| manifest | `schema_version` | 회사 | 필수 |
| manifest | `config_revision` | 회사 | 필수 (서버가 `manifests.version` 으로 덮어쓴다) |
| manifest | `repository_allowlist` | 회사 | 권장 |
| 요청 | `client_version` | 설치 | 권장 (`installer_version` 은 deprecated) |

---

# 4. 전역 OTel 설정에서 예상되는 문제

## 4.1 기존 개인 설정 덮어쓰기

개발자가 이미 Codex 또는 Claude Code 설정을 사용하고 있을 수 있다.

예:

```text
- 개인 모델 설정
- MCP 설정
- 다른 OTel endpoint
- Datadog 또는 Grafana 연동
- 개인 환경변수
```

설치 스크립트가 설정 파일 전체를 새로 작성하면 기존 설정이 사라질 수 있다.

### 해결 방법

```text
- 설정 파일 전체 덮어쓰기 금지
- TOML 및 JSON 파서를 사용해 필요한 키만 수정
- 수정 전 백업 생성
- 설치 프로그램이 관리한 키 목록 기록
- uninstall 시 관리한 키만 제거
```

Codex에서는 `[otel]` 영역만 관리한다.

```toml
model = "existing-model"

[otel]
environment = "production"
log_user_prompt = false
```

Claude Code에서는 `env` 객체 전체를 교체하지 않고 OTel 관련 키만 병합한다.

```json
{
  "env": {
    "EXISTING_USER_VARIABLE": "keep",

    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "https://telemetry.company.com"
  }
}
```

정규식만으로 TOML이나 JSON을 수정하는 방식은 피하고, 파서를 사용하는 것이 안전하다.

---

## 4.2 기존 OTel endpoint 충돌

사용자가 이미 다른 Collector로 데이터를 보내고 있을 수 있다.

이 경우 네 endpoint로 자동 교체하면 기존 모니터링이 중단된다.

### 처리 정책

```text
기존 OTel 설정 없음
→ 네 endpoint 설정

이미 네 endpoint 사용 중
→ 설정 버전만 갱신

다른 endpoint가 존재
→ 자동 덮어쓰기 금지
→ 충돌 상태 표시
→ 관리자 또는 사용자 선택 필요

고객사가 기존 Collector 운영 중
→ 기존 Collector에서 네 서버로 fan-out
```

기업 고객에게 가장 적합한 구조는 다음과 같다.

```text
Codex / Claude Code
        ↓
고객사의 기존 OTel Collector
        ├─ Datadog / Grafana 등 기존 시스템
        └─ 네 Adapter 및 Enrichment 서버
```

기존 OTel 인프라가 있다면 개발자의 전역 endpoint를 직접 교체하는 것보다 Collector 통합을 우선한다.

---

## 4.3 개인 프로젝트까지 수집될 위험

전역 설정은 회사 프로젝트뿐 아니라 개인 프로젝트, 오픈소스 활동, 학습 프로젝트까지 전송할 수 있다.

```text
회사 프로젝트
개인 GitHub 프로젝트
오픈소스 프로젝트
다른 고객사 프로젝트
학습용 로컬 디렉터리
```

이는 개인정보 및 법무 측면에서 큰 위험이다.

### 해결 방법

등록된 저장소만 저장하는 allowlist 정책을 적용한다.

```text
git remote 또는 repository identity 확인
→ tenant에 등록된 repository인지 조회
→ 등록됨: 저장
→ 등록되지 않음: 폐기
→ 식별 불가: quarantine 또는 최소 정보만 기록
```

예:

```text
github.com/company/payment-api   → 수집
github.com/company/admin-web     → 수집
github.com/user/private-project  → 폐기
repository 식별 불가             → quarantine
```

다만 모든 telemetry 이벤트에 repository 정보가 포함된다고 가정하면 안 된다.

보안 요구가 높은 고객은 다음 방식을 선택할 수 있다.

```text
- 회사 관리 장비에만 전역 설정
- 회사 전용 launcher 또는 wrapper 사용
- 회사 저장소에서만 활성화되는 실행 환경 제공
- 기존 사내 Collector에서 repository 기준 필터링
```

---

## 4.4 사용자가 설정을 삭제하거나 수정할 수 있음

사용자 전역 설정은 사용자가 직접 수정할 수 있다.

```text
~/.codex/config.toml 수정
~/.claude/settings.json 수정
endpoint 교체
token 삭제
telemetry 비활성화
```

한 줄 설치 방식은 편의 기능이지 강제 정책이 아니다.

### 해결 방법

PoC 및 소규모 고객:

```text
- 사용자 전역 설정 사용
- 마지막 수신 시간 확인
- 설정 drift 감지
- repair 명령 제공
```

기업 고객:

```text
- Intune 또는 MDM 배포
- Claude Code managed settings 사용
- 그룹 정책 적용
- 설정 변경 방지
```

제품에서는 다음 설치 상태를 제공하는 것이 좋다.

```text
Connected
Outdated
Misconfigured
Drifted
Revoked
Never Seen
Unsupported Client Version
```

---

## 4.5 인증 토큰이 평문으로 저장될 위험

Codex header와 Claude Code 환경변수에 토큰을 넣으면 설정 파일에 평문으로 남을 수 있다.

동일 사용자 권한으로 실행되는 다른 프로세스가 파일을 읽을 가능성이 있다.

### MVP 대응

```text
- 회사 공용 토큰 금지
- 설치별 독립 토큰 발급
- ingest-only 권한
- tenant 및 installation에 귀속
- HTTPS 필수
- 토큰 로그 출력 금지
- rate limit 적용
- 재설치 시 rotation
- 관리자에 의한 즉시 폐기 지원
```

### 제품화 이후

로컬 agent를 두는 방식도 고려할 수 있다.

```text
Codex / Claude Code
→ localhost agent
→ OS Credential Manager에서 토큰 조회
→ 회사 Collector 전송
```

이 구조에서는 Codex와 Claude Code 설정에 장기 토큰 대신 로컬 endpoint만 들어간다.

```text
http://localhost:4318
```

> **주의 (PROJ-36).** 이 절의 초안은 위 주소를 `http://127.0.0.1:4318` 로 적었으나 **그 표기는 쓸 수 없다.**
> `internal/contract/manifest.go` 의 `validOTLPEndpoint` 와 `contracts/enrollment-manifest.schema.json`
> 은 `http://` 를 리터럴 호스트 `localhost` 에만 허용하므로 `127.0.0.1` 은 manifest 검증에서 거부된다.
> 실제 구현은 벤더 설정에 `http://localhost:<port>` 를 쓰고, 수신기가 `127.0.0.1` 과 `[::1]` 두 리스너를
> 하나의 서버에 문다(`localhost` 가 `::1` 로 풀리는 환경이 있기 때문). 자세한 내용은
> [로컬 파이프라인 문서](local-pipeline.md) 7.3절과 [ADR 0001](adr/0001-로컬-OTLP-수신기-인라인-프록시-토폴로지.md)을 보라.

다만 로컬 agent는 설치, 업데이트, 자동 실행, 장애 복구, 보안 관리가 추가되므로 MVP에는 과할 수 있다.

> **PROJ-36 이후.** 이 「제품화 이후」 항목은 더 이상 미래형이 아니다. `telemetryctl daemon` 이 로컬
> 수신기를 띄우고 ~~`telemetryctl local enable` 이 opt-in 으로 재배선한다(기본 OFF)~~ — PROJ-45(ADR 0006)
> 부터 **`enroll` 이 자동으로 배선한다(opt-out, 기본 ON)**. ~~자동 실행 등록만 후속 티켓으로 남아 있다.~~
>
> **PROJ-55 이후.** 자동 실행 등록도 끝났다. `telemetryctl autostart enable` 이 macOS LaunchAgent 와
> 리눅스 systemd user unit 을 등록하고 `enroll` 이 best-effort 로 호출한다
> ([로컬 파이프라인 문서](local-pipeline.md) 7.7절, [ADR 0007](adr/0007-데몬은-비정상-종료일-때만-자동-재시작한다.md)).
> Windows 작업 스케줄러만 후속 티켓으로 남아 있다.

---

## 4.6 민감 정보 수집 위험

OTel 설정을 잘못 적용하면 다음 데이터가 수집될 수 있다.

```text
사용자 프롬프트 원문
assistant 응답 원문
tool input 및 output
파일 내용
터미널 명령어
사용자 이메일
로컬 경로
환경변수
```

### 기본 정책

MVP에서는 다음을 기본값으로 고정한다.

```text
프롬프트 원문             OFF
assistant 응답 원문       OFF
tool input/output         OFF
파일 내용                 OFF
명령어 상세               OFF 또는 최소화
사용자 이메일             필요성 검토 전 OFF
```

민감 정보 보호는 세 계층에서 수행한다.

```text
클라이언트 설정에서 비활성화
        +
Collector에서 redaction
        +
Adapter에서 allowlist 기반 필드 추출
```

클라이언트 설정만 믿으면 사용자가 설정을 변경했을 때 민감 데이터가 들어올 수 있으므로 서버 측 제거가 반드시 필요하다.

---

# 5. 추가 고려사항

## 5.1 Windows와 WSL은 별도 환경

Windows와 WSL은 서로 다른 홈 디렉터리와 설정 파일을 사용할 수 있다.

```text
Windows:
C:\Users\user\.codex
C:\Users\user\.claude

WSL:
~/.codex
~/.claude
```

설치 스크립트는 다음을 확인해야 한다.

```text
- Codex가 Windows에서 실행되는가
- Codex가 WSL에서 실행되는가
- Claude Code가 어느 환경에 설치되어 있는가
- Windows와 WSL 양쪽에 설정이 필요한가
```

Windows 설정만 적용했는데 사용자가 WSL에서 Codex를 실행하면 telemetry가 수집되지 않을 수 있다.

---

## 5.2 설치와 제거는 대칭이어야 함

다음 기능을 제공하는 것이 좋다.

```text
install
status
repair
update
uninstall
```

제거 시 과거 백업 파일 전체를 복원하면 안 된다.

설치 이후 사용자가 추가로 수정한 설정까지 사라질 수 있기 때문이다.

올바른 제거 방식:

```text
- 네 제품이 추가한 설정 키만 제거
- installation token 폐기
- 서버 installation 상태를 revoked로 변경
- 사용자가 추가한 다른 설정은 유지
```

---

## 5.3 설정 버전 관리

설치 시점과 클라이언트 버전에 따라 필요한 OTel 설정이 달라질 수 있다.

따라서 다음 버전을 관리한다.

```text
schema_version
config_revision
installer_version
codex_version
claude_code_version
```

예:

```json
{
  "schema_version": 1,
  "config_revision": 12,
  "installer_version": "0.2.1"
}
```

설치 프로그램은 현재 설정과 서버 최신 revision을 비교하여 update 또는 repair 필요 여부를 판단한다.

---

## 5.4 Collector 장애가 개발을 방해하면 안 됨

네 서버 장애 때문에 Codex나 Claude Code 사용이 느려지거나 실패해서는 안 된다.

Collector 장애는 telemetry 손실로 끝나야 한다.

### 권장 설정

```text
- 짧은 export timeout
- 비동기 전송
- 제한된 retry
- retry queue 크기 제한
- Collector memory limiter
- batch processor
- rate limit
- request size limit
```

피해야 하는 구조:

```text
Collector 응답 대기
→ 개발 도구 실행 지연
→ 개발자 작업 중단
```

권장 구조:

```text
개발 도구 정상 동작
+
telemetry는 best-effort로 전송
```

---

## 5.5 중복 이벤트 처리

다음 상황에서 동일한 이벤트가 중복 수신될 수 있다.

```text
exporter retry
Collector 재전송
프로세스 재시작
Codex CLI와 IDE 확장 동시 사용
Windows와 WSL 양쪽 설정
```

저장 전에 다음 값들을 조합해 deduplication을 수행한다.

```text
vendor
installation_id
event_id
trace_id
span_id
timestamp
event kind
sequence
```

중복 제거가 완벽하지 않은 경우에도 집계 결과가 크게 왜곡되지 않도록 설계해야 한다.

---

## 5.6 고카디널리티 관리

다음 값을 Metric label로 과도하게 사용하면 저장 비용과 조회 성능이 악화될 수 있다.

```text
session_id
user_id
repository_id
project_id
request_id
trace_id
```

권장 구분:

```text
Metric:
- team_id
- model
- client product
- environment
- status

Log / Trace:
- session_id
- user_id
- request_id
- trace_id
- span_id
```

세션 및 사용자 단위 세부 분석은 Log와 Trace에서 수행하고, Metric에는 비교적 낮은 카디널리티 속성만 사용한다.

---

## 5.7 인증서와 사내 네트워크

셀프호스트 고객은 사내 인증기관에서 발급한 인증서나 프록시를 사용할 수 있다.

설치 프로그램은 다음 상황을 고려해야 한다.

```text
- 사내 Root CA
- HTTPS inspection proxy
- VPN 내부 endpoint
- 인터넷 차단 환경
- Private DNS
- 프록시 환경변수
```

필요한 경우 custom CA 인증서 설치 또는 인증서 경로 설정 기능을 제공한다.

---

## 5.8 Offline 및 재연결

개발자가 VPN에 연결하지 않았거나 Collector가 일시적으로 접근 불가능할 수 있다.

다음 정책을 결정해야 한다.

```text
- 전송 실패 데이터를 로컬에 보관할 것인가
- 최대 보관 크기는 얼마인가
- 최대 보관 시간은 얼마인가
- 재연결 시 일괄 전송할 것인가
- 로컬 디스크 암호화가 필요한가
```

MVP에서는 로컬 장기 보관보다 제한된 메모리 queue 또는 짧은 retry만 사용하는 것이 단순하다.

---

## 5.9 설치 프로그램 공급망 보안

다음 명령은 원격 코드를 즉시 실행한다.

```powershell
irm "https://get.your-service.com/windows?code=<초대코드>" | iex
```

PoC에는 편리하지만 기업에서는 보안 검토 대상이 된다.
초대 코드가 명령줄과 셸 히스토리에 남는 것도 이 절의 검토 대상에 포함된다 —
일회성 코드이고 enroll 이 원자적으로 소비한다는 전제 위에서 수용한 위험이다.

### 대응 방안

```text
초기 PoC:
- HTTPS
- 고정된 공식 도메인
- 설치 스크립트 해시 제공
- 스크립트 변경 이력 관리

제품화:
- 코드 서명된 MSI 또는 EXE
- WinGet 배포
- Intune 또는 MDM 배포
- 설치 파일 서명 및 검증
```

장기적으로 한 줄 PowerShell은 부트스트랩 용도로 두고, 기업용으로 서명된 설치 패키지를 제공하는 것이 좋다.

---

## 5.10 Client 버전 호환성

Codex와 Claude Code의 telemetry schema 또는 설정 키가 변경될 수 있다.

따라서 다음 정보를 수집하고 관리해야 한다.

```text
client product
client version
installer version
config revision
adapter version
normalized schema version
```

Adapter는 지원하지 않는 client version을 진단할 수 있어야 한다.

```text
supported
deprecated
unsupported
unknown
```

---

## 5.11 데이터 보존 및 삭제

기업 고객은 다음 정책을 요구할 수 있다.

```text
- 원본 telemetry 보존 기간
- 정규화 데이터 보존 기간
- 개인정보 삭제 요청
- 퇴사자 데이터 처리
- 특정 프로젝트 데이터 삭제
- tenant 탈퇴 시 전체 삭제
```

ClickHouse 파티션과 데이터 보존 정책을 tenant 및 날짜 기준으로 설계하는 것이 좋다.

---

## 5.12 감사 로그

설치 및 설정 변경 자체도 감사 대상이 될 수 있다.

기록할 항목:

```text
누가 설치했는가
어느 장치에 설치했는가
어떤 설정 revision이 적용됐는가
endpoint가 변경됐는가
token이 언제 발급·폐기됐는가
설정 drift가 언제 감지됐는가
누가 repair 또는 uninstall을 수행했는가
```

---

# 6. 권장 제품 구조

```text
개발자
  │
  ▼
한 줄 설치 명령
  │
  ▼
Installer
  ├─ 초대 코드로 enroll (이 흐름에 로그인 없음 — §3.2 (b) 참고)
  ├─ Windows / WSL 탐지
  ├─ 기존 설정 충돌 검사
  ├─ 설정 백업
  ├─ TOML / JSON 안전 병합
  ├─ 연결 테스트
  └─ 설치 결과 등록
  │
  ▼
Codex / Claude Code
  │
  ▼
Authentication Gateway
  ├─ token → tenant_id
  ├─ rate limit
  ├─ request size limit
  └─ installation 상태 확인
  │
  ▼
Collector
  ├─ redaction
  ├─ allowlist
  ├─ filter
  ├─ memory limiter
  └─ batch
  │
  ▼
Adapter
  ├─ Codex 이벤트 해석
  ├─ Claude Code 이벤트 해석
  └─ 공통 스키마 정규화
  │
  ▼
Enrichment
  ├─ user_id 연결
  ├─ team_id 연결
  ├─ project_id 연결
  ├─ tenant_id 적용
  └─ pricing 및 정책 문맥 추가
  │
  ▼
ClickHouse / PostgreSQL
```

---

# 7. MVP 구현 범위

> 이 목록은 **최초 MVP 계획**이다. 현재 구현 상태는 `README.md` 와 `docs/adr/` 를 본다 —
> 체크 상태를 여기서 따로 관리하지 않는다.

## 반드시 구현

- [ ] 공통 한 줄 설치 명령
- [ ] 초대 코드 기반 enroll — 설치 흐름에는 로그인이 없다 (§3.2 (a))
- [ ] 웹 로그인 — 콜백 URL 로 AT·RT 전달 (`pulsemetry-backend` ADR 0007 채택, 미구현.
      콜백 주소 규칙과 PKCE 적용 여부는 별도 ADR)
- [ ] 설치별 `installation_id` 생성
- [ ] 설치별 ingest-only token 발급
- [ ] Codex 설정 파일 안전 병합
- [ ] Claude Code 설정 파일 안전 병합
- [ ] 기존 endpoint 충돌 감지
- [ ] 설정 파일 백업
- [ ] 프롬프트 및 tool 내용 수집 차단
- [ ] Collector 서버 측 redaction
- [ ] 등록된 repository만 저장
- [ ] 첫 telemetry 수신 상태 표시
- [ ] `status` 및 `uninstall` 지원
- [ ] 설정 revision 관리
- [ ] token 폐기 기능
- [ ] 중복 이벤트 처리

## 이후 구현

- [ ] `repair` 및 자동 update
- [ ] Windows와 WSL 동시 지원
- [ ] 설정 drift 주기적 감지
- [ ] 코드 서명된 MSI 또는 EXE
- [ ] WinGet 패키지
- [ ] Intune 및 MDM 배포
- [ ] 로컬 agent
- [ ] OS Credential Manager 연동
- [ ] 기존 고객 Collector fan-out 지원
- [ ] custom CA 및 사내 프록시 지원
- [ ] client version 호환성 정책
- [ ] 감사 로그 및 보존 정책

---

# 8. 핵심 결정사항

## 설치 명령어

```powershell
irm "https://get.your-service.com/windows?code=<초대코드>" | iex
```

## 사용자별 변수 전달

```text
초대 코드를 설치 URL 에 싣는다 (노출 위험을 알고 수용)
→ 서버가 코드를 박은 부트스트랩 스크립트를 내려준다
→ 스크립트가 enroll 호출
→ 자격 봉투 + 회사 manifest 수신
```

## 인증

```text
회사 공용 token 금지
→ 설치별 ingest-only token 사용
```

## 전역 설정 처리

```text
설정 파일 전체 덮어쓰기 금지
→ 기존 설정 검사
→ 백업
→ 필요한 키만 병합
→ 충돌 시 자동 교체 금지
```

## 개인정보 보호

```text
프롬프트·응답·tool 내용 기본 미수집
+
Collector redaction
+
Adapter allowlist
```

## 회사 프로젝트 구분

```text
전역 설정만으로 완벽한 구분은 어려움
→ repository allowlist
→ 회사 관리 장비
→ 필요 시 wrapper 또는 기존 Collector 통합
```

## 기업용 배포

```text
PoC:
PowerShell 한 줄 설치

제품화:
서명된 MSI/EXE + WinGet

대기업:
Intune/MDM + managed settings
```

---

# 9. 최종 정리

한 줄 설치 자체보다 중요한 것은 설치 이후의 안전한 설정 관리다.

제품은 단순히 OTel endpoint를 전역 설정에 강제로 넣는 도구가 아니라 다음 역할을 수행해야 한다.

```text
- 사용자의 회사와 설치 환경 식별
- 개인·회사별 설정 자동 주입
- 기존 OTel 환경 충돌 감지
- 민감 데이터 수집 차단
- 개인 프로젝트 수집 최소화
- 설정 변경 및 drift 감지
- 설치·복구·삭제 생명주기 관리
- 서버 장애가 개발 작업에 영향을 주지 않도록 격리
```

따라서 초기 MVP의 핵심은 다음 세 가지다.

> **안전한 설정 병합, 설치별 인증, 회사 프로젝트만 수집하는 정책**

이 세 가지가 갖춰져야 한 줄 설치가 실제 기업 환경에서도 사용할 수 있는 온보딩 방식이 된다.
