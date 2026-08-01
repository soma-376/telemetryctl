# Enrollment 서버 스펙 (MVP)

이 문서는 pulsemetry 설치 도구가 대화하는 **Enrollment 서버(Control Plane)**의 스펙을 정의한다.
설치 후 telemetry 가 흐르는 **Ingest 파이프라인(Data Plane)**은 별도 서비스이며 이 문서 범위 밖이다
(§8 경계 참조). 전체 그림과 근거는 [설치 아키텍처](installation-architecture.md)를 따른다.

> 표기: 문서에서 **[문서]**는 installation-architecture.md 에서 확정된 사항, **[제안]**은 이 스펙에서
> 새로 정하는 사항이다. REST 경로·요청/응답 JSON·DB 스키마는 대부분 **[제안]**이다.

---

## 1. 서버의 역할 (한 줄)

> "요청자가 **어느 회사(tenant)의 누구인지** 인증하고, 그 **설치(installation)**에 맞는
> **설정(manifest)**과 **전용 ingest 토큰**을 발급하고, 이후 **설치 상태를 추적**한다."

Enrollment 서버가 하는 일 **[문서 §3.2]**:

1. 브라우저/device-code 로그인 → 회사 계정 인증
2. tenant + user 결정
3. installation 생성 (`installation_id`)
4. 설치별 **ingest-only 토큰** 발급
5. **설정 manifest 반환**
6. 설치 상태 추적, config revision 관리, 토큰 폐기, 감사 로그

명시적으로 **하지 않는** 것:

- telemetry 수신·정제·저장 (→ Ingest 파이프라인)
- 대시보드·분석 (→ 별도 제품 서비스)

---

## 2. 도메인 모델

```
tenant(회사)
   ├──< invite(관리자 발급 초대)          ← MVP 등록 수단 (§3)
   ├──< user(직원) ──< installation(설치 단위) ──< installation_token
   └──< tenant_config(회사 설정)          (config_revision 으로 버전관리)
```

| 엔터티 | 의미 | 비고 |
|---|---|---|
| `tenant` | 회사/조직 | SaaS 는 공통 endpoint + 토큰으로 구분, 셀프호스트는 전용 endpoint **[문서 §3.4]** |
| `invite` | 관리자가 발급하는 등록 초대(링크/코드) | tenant(org) 또는 특정 user 에 귀속. MVP 신원 수단 **[제안 §3]** |
| `user` | 직원. 회사 SSO 로 인증 | identity 는 서버만 신뢰. 클라이언트가 보낸 값 불신 **[문서 §3.4]** |
| `installation` | PC/환경 단위 설치 | Windows·WSL 은 별개 설치일 수 있음 **[문서 §5.1]** |
| `installation_token` | OTLP 전송·상태 갱신용 토큰 | ingest-only, 설치에 귀속 **[문서 §4.5]** |
| `tenant_config` | 회사 단위 OTel 설정 | manifest 의 회사 파트 소스. 변경 시 `config_revision` 증가 |

**핵심 신뢰 규칙 [문서 §3.4]**: 클라이언트가 보낸 `tenant_id`·`user_id`·`team_id`·`project_id`는 **신뢰하지 않는다.**
서버가 토큰으로 도출한다.

```
installation_token → installation → tenant_id → user identity → team/department → (repo 기준) project
```

> MVP 의 **org 초대**는 `tenant` + `installation`(+ `device_id`)까지만 확정하고 **user identity 는 비움**
> (§3.2). 개인 단위 attribution 이 필요하면 user 초대 또는 후속 SSO 로 채운다.

---

## 3. 신원 확인과 등록 흐름

전용 SSO IdP 가 있다고 **가정하지 않는다.** MVP 는 **관리자가 발급한 초대(링크/코드)** 로 등록한다.

### 3.1 신원 보증 모델 (IdP 불가정) [제안]

"이 사람이 직원인가"의 근거는 IdP 로그인이 아니라 **관리자가 초대를 건넸다는 사실**이다.

```
tenant 관리자(신뢰됨) ─ 초대 생성 ─▶ 직원에게 배포(Slack/이메일/MDM)
        → 초대 보유자 = 관리자가 보증한 사람
        → 초대 검증 → tenant 확정 → installation + 토큰 발급
```

이는 신원 소스 사다리의 '관리자 배포 key' 층을 링크/코드로 구체화한 것이다. SSO·OIDC 는 §3.4 후속.

### 3.2 초대 종류 [제안]

| 종류 | 바인딩 | 사용 횟수 | 개인 식별 | 용도 |
|---|---|---|---|---|
| **org 초대** | tenant 전체 | 다회 (`max_uses`) | ✗ (device 로만 구분) | 사내 배포·MDM. 간편. **MVP 기본** |
| user 초대 | 특정 이메일 | 1회 | ✓ (개인 확정) | 좌석형. 개인 귀속 필요 시 (후속) |

MVP 는 **org 초대**만으로 성립한다 — 토큰의 임무는 "이 설치가 어느 회사 것인지" 묶는 것이고(§2 신뢰 규칙),
개인 attribution 은 필요해질 때 user 초대로 추가한다.

### 3.3 등록 흐름 (단일 왕복 · 브라우저 불요) [제안]

```
관리자                    Enrollment 서버              직원 PC
  │ POST /v1/admin/invites    │                          │
  │ ◀─ {code, link} ──────────│                          │
  │ ── 코드/링크 전달 ─────────────────────────────────────▶│
  │                           │   irm ... | iex           │
  │                           │   (프롬프트: 초대 코드) 또는 │
  │                           │   pulsemetry install --invite <code>
  │                           │ ◀─ POST /v1/enroll {invite}│
  │                           │ ── {installation_id, token, manifest} ─▶
```

- **코드**: 사람이 입력하는 짧은 값(예: `WXYZ-1234`). 브루트포스 방지 위해 **짧은 만료 + rate limit + 낮은 max_uses**.
- **링크**: 고엔트로피 비밀을 담은 URL(예: `.../invite/inv_<random>`). 클릭 시 설치 안내 또는 코드 자동 채움.
- SSO·device-code 폴링이 없어 **헤드리스/MDM 배포에 그대로 맞는다.**

### 3.4 후속: SSO/OIDC · device-code

전용 IdP(Okta/Entra)나 Google/GitHub OIDC 를 붙이면 개인 신원을 자동 확정할 수 있고, 브라우저가 필요한
경우 device-code(RFC 8628)로 확장한다. **MVP 범위 밖.**

---

## 4. API 엔드포인트

공통:

- 모든 요청/응답 `application/json`, HTTPS 필수 **[문서 §4.5]**.
- 인증이 필요한 엔드포인트는 `Authorization: Bearer <installation_token>`.
- 에러 응답 공통 형태:

```json
{ "error": "invalid_token", "message": "installation token 이 유효하지 않습니다" }
```

### 4.1 `POST /v1/enroll` — 초대로 등록 (인증: 초대 자체)

요청:

```json
{
  "invite": "WXYZ-1234",                  // 초대 코드 또는 링크의 토큰
  "installer_version": "0.1.0",
  "operating_environment": "windows",     // "windows" | "wsl" | "linux" | "darwin"
  "device_id": "dev_...",                 // 설치 스크립트가 생성·보관하는 장치 식별자(선택)
  "tools_detected": ["claude", "codex"]
}
```

응답 `200` (성공) — **installation_token 과 manifest 를 최초 1회 발급**:

```json
{
  "installation_id": "ins_01JABC",
  "installation_token": "inst_xxxxxxxxxxxx",
  "manifest": { /* §5 manifest, enrollment-manifest.schema.json 준수 */ }
}
```

에러:

```json
{ "error": "invite_invalid" }             // 없음 / 폐기됨
{ "error": "invite_expired" }
{ "error": "invite_exhausted" }           // max_uses 초과
```

- 성공 시 org 초대는 `used_count` 증가, user 초대는 소진(1회).
- `installation_token` 은 이 응답에서 **평문으로 단 한 번** 내려간다. 서버는 해시만 저장한다(§6).
- 짧은 코드 브루트포스 방지를 위해 **rate limit 필수** (§6).

### 4.3 `GET /v1/manifest` — 현재 설정 재조회 (Bearer 필요)

update/repair·drift 확인 시 사용. 로컬 `config_revision` 과 비교 **[문서 §5.3]**.

요청 헤더: `Authorization: Bearer <installation_token>`
쿼리(선택): `?current_revision=12`

- 로컬이 최신이면 `304 Not Modified`
- 갱신 필요하면 `200` + 최신 manifest (단, `installation_token` 필드는 재발급하지 않고 기존 유지)

### 4.4 `POST /v1/installations/{id}/heartbeat` — 상태 보고 (Bearer 필요)

요청:

```json
{
  "config_revision": 12,
  "installer_version": "0.1.0",
  "client_versions": { "claude_code": "1.2.3", "codex": "0.9.0" },
  "operating_environment": "windows",
  "health": "connected"                   // §7 상태값
}
```

응답 `200`:

```json
{
  "latest_config_revision": 13,
  "action": "update"                      // "none" | "update" | "repair"
}
```

### 4.5 관리자 엔드포인트 (별도 인증 — 관리자 세션, installation_token 아님)

| 엔드포인트 | 역할 |
|---|---|
| `POST /v1/admin/invites` | 초대 생성 → `{code, link, type, max_uses, expires_at}` 반환 |
| `GET /v1/admin/invites?tenant={id}` | 초대 목록·사용 현황 조회 |
| `POST /v1/admin/invites/{id}/revoke` | 초대 폐기 |
| `POST /v1/admin/installations/{id}/revoke` | 토큰 폐기 + installation `Revoked` 전환 **[문서 §4.5]** |
| `GET /v1/admin/installations?tenant={id}` | tenant 별 설치 목록·상태 조회 |
| `PUT /v1/admin/tenants/{id}/config` | 회사 설정 변경 → `config_revision` 증가 |

> installation_token 으로는 관리자 API 를 **절대 호출할 수 없다** — 권한 경계 §6.

---

## 5. Manifest 응답 스펙

**응답 본문 = `contracts/enrollment-manifest.schema.json`** (서버·installer·파이프라인 공유 진실원).
서버는 이 스키마를 벗어난 필드를 내려선 안 되며, installer 는 알 수 없는 필드를 거부한다.

manifest = **회사 파트(tenant_config)** + **설치 파트** 의 합성:

| 필드 | 출처 | 범위 |
|---|---|---|
| `schema_version` | 서버 빌드 | — |
| `config_revision` | tenant_config | 회사 |
| `installation_id` | installation | 설치 |
| `installation_token` | 발급 시 | 설치 (enroll/poll 에서만) |
| `otlp.*` | tenant_config | 회사 |
| `signals.*` | tenant_config | 회사 |
| `privacy.*` | tenant_config (기본 전부 false **[문서 §4.6]**) | 회사 |
| `repository_allowlist` | tenant_config | 회사 |
| `resource_attributes` | tenant_config | 회사 |

예시는 [설치 아키텍처 §3.3](installation-architecture.md) 참조.

---

## 6. 토큰 스펙 [문서 §3.4 · §4.5]

**권한 (딱 두 가지만 허용):**

```
허용:  OTLP 데이터 전송 (otlp:write)
       자기 설치 상태 갱신 (installation:heartbeat)
금지:  대시보드 조회 · 조직 설정 변경 · 다른 설치/사용자 조회
```

**형식 [제안]:** MVP 는 **opaque 랜덤 토큰**(예: `inst_` + 32바이트 base62)을 권장한다.

- 장점: 폐기가 단순(DB 에서 삭제/무효화), 클레임 노출 없음.
- 저장: **평문 저장 금지.** `token_hash`(예: SHA-256)만 저장하고, 검증 시 해시 비교.
- 대안(JWT): 자체 검증 가능하나 즉시 폐기가 어려움 → MVP 부적합.

**수명/회전:**

- 장수명 + 폐기 가능. 재설치 시 **rotation** **[문서 §4.5]**.
- 관리자 즉시 폐기 지원.

**운영 규칙:** HTTPS 필수, **로그 출력 금지**, rate limit, 요청 크기 제한 **[문서 §4.5 · §5.4]**.

---

## 7. Installation 상태 머신 [문서 §4.4]

```
Never Seen ──(첫 telemetry 수신)──▶ Connected
Connected ──(config_revision 뒤처짐)──▶ Outdated
Connected ──(로컬 설정 변조 감지)──▶ Drifted
Connected ──(설정 오류)──▶ Misconfigured
any ──(관리자 폐기)──▶ Revoked
any ──(미지원 client 버전)──▶ Unsupported Client Version
```

| 상태 | 의미 | 판정 근거 |
|---|---|---|
| `Never Seen` | 등록됐으나 telemetry 미수신 | Ingest 측 최초 수신 이벤트 없음 |
| `Connected` | 정상 | heartbeat + telemetry 수신 |
| `Outdated` | 설정 리비전 뒤처짐 | `config_revision < latest` |
| `Misconfigured` | 설정 오류 | heartbeat health=`misconfigured` |
| `Drifted` | 사용자가 로컬 설정 변경 | heartbeat health=`drifted` |
| `Revoked` | 폐기됨 | 관리자 조치 |
| `Unsupported Client Version` | client 버전 미지원 | `client_versions` 대조 **[문서 §5.10]** |

> `Never Seen` / `Connected` 판정은 telemetry 실수신 정보가 필요하므로 **Ingest 파이프라인과 연동**해야 한다.

---

## 8. 저장 모델 (PostgreSQL 스케치) [제안]

```sql
tenants(id, name, plan, created_at)
users(id, tenant_id, email, sso_subject, created_at)
invites(
  id, tenant_id, type,                    -- 'org' | 'user'
  code_hash, link_secret_hash, email,     -- 코드·링크 비밀은 해시만 저장
  max_uses, used_count, expires_at,
  created_by, created_at, revoked_at
)
installations(
  id, tenant_id, user_id, device_id,
  config_revision, installer_version, operating_environment,
  status, last_verified_at, created_at, revoked_at
)
installation_tokens(id, installation_id, token_hash, scopes, created_at, revoked_at)
tenant_configs(
  tenant_id, config_revision,
  otlp jsonb, signals jsonb, privacy jsonb,
  repository_allowlist jsonb, resource_attributes jsonb,
  updated_at
)
audit_log(id, tenant_id, actor, action, target, metadata jsonb, at)  -- [문서 §5.12]
```

`audit_log` 기록 항목 **[문서 §5.12]**: 설치자·장치·적용 revision·endpoint 변경·토큰 발급/폐기·drift 감지·repair/uninstall 수행자.

---

## 9. Control Plane ↔ Data Plane 경계

| 구분 | Enrollment 서버 (이 문서) | Ingest 파이프라인 (별도) |
|---|---|---|
| 하는 일 | 인증·manifest·토큰 발급·상태 | telemetry 수신·redaction·정규화·저장 |
| 엔드포인트 | `get.your-service.com` (제안) | `manifest.otlp.endpoint` |
| 토큰 사용 | 발급·폐기 | 검증(Auth Gateway) |
| 저장소 | PostgreSQL(메타데이터) | ClickHouse(telemetry) |

두 평면은 **installation_token + installation/tenant 모델**을 공유하지만 **서로 다른 서비스**다.
Auth Gateway 는 Enrollment 서버의 토큰 저장소를 조회(또는 캐시)해 검증한다.

---

## 10. MVP 범위

**반드시 (이게 있어야 pulsemetry 이 실서버로 동작):**

- [ ] `POST /v1/admin/invites` — 관리자 **org 초대** 생성 (링크/코드)
- [ ] `POST /v1/enroll` — 초대 검증 → `installation_id` + ingest-only 토큰 + manifest 발급
- [ ] `GET /v1/manifest` (Bearer, revision 비교)
- [ ] 토큰·초대 **해시 저장** + 폐기(`/revoke`) + **rate limit**
- [ ] `tenant_configs` → manifest 합성
- [ ] 감사 로그 최소본

**이후:**

- [ ] **user 초대**(개인 attribution) → 이어서 SSO/OIDC·device-code
- [ ] `heartbeat` 기반 상태 머신 전체 + drift 감지
- [ ] Ingest 연동(Never Seen/Connected 판정)
- [ ] repair/update 자동화, client 버전 호환성 정책
- [ ] SaaS/셀프호스트 endpoint 자동 해석, custom CA

---

## 11. 미해결 결정 사항

1. **토큰 형식** — opaque(권장) vs JWT. 즉시 폐기 요구가 강하면 opaque 확정.
2. **관리자 인증 부트스트랩** — 최초 tenant·관리자는 어떻게 만드나? MVP 는 벤더가 **수동 프로비저닝**(tenant 생성 + 관리자 접근 부여)으로 두고, 셀프서비스 가입은 후속.
3. **초대 코드 정책** — org 다회 초대의 만료·`max_uses`·유출 대응. 유출 시 blast radius 는 토큰이 **ingest-only + tenant scope** 라 제한적이나(대시보드·타 설치 접근 불가), 스팸·데이터 오염은 가능 → **폐기 + rate limit + 짧은 만료** 필수.
4. **endpoint 해석** — SaaS 공통 endpoint vs 셀프호스트 전용을 manifest 로만 내릴지, 별도 discovery 를 둘지 **[문서 §3.4]**.
5. **repository_allowlist 집행 지점** — 클라이언트는 신뢰 불가 → Collector/Adapter 측 집행이 원칙 **[문서 §4.3]**. Enrollment 는 목록 배포만.
6. **Windows/WSL 동일 설치 처리** — org 초대는 개인 식별을 안 하므로 각 설치를 **device 단위**로 구분. 동일인 묶기는 user 초대·SSO 도입 시 **[문서 §5.1]**.
```
