# 상위 전달 계약 — 데몬 → 프록시 → 원격 파이프라인

## 1. 문서 목적 · 독자 · 범위

이 문서는 텔레메트리가 개발자 머신을 떠나 원격 파이프라인에 닿기까지의 HTTP 계약을 못박는 **진입점**이다.
홉별 상세는 자식 문서에 있고, 여기에는 **토폴로지와 두 홉을 가로지르는 것**만 둔다.

구현이 **서로 다른 저장소**에 있어서, 한쪽이 헤더 목록·상태코드 의미·상한값을 바꾸면 다른 쪽이
조용히 깨진다. 텔레메트리 손실은 사용자 화면에 아무 증상도 만들지 않으므로 발견도 늦다.

독자는 **양쪽 구현자**다 — `internal/forward` 를 고치는 사람과 auth-proxy 를 고치는 사람.
그래서 모든 절이 값의 나열이 아니라 **"한쪽이 이걸 바꾸면 다른 쪽이 어떻게 깨지는가"** 를 함께 적는다.

| 범위 | 다루는 곳 |
|---|---|
| 벤더(Claude Code·Codex) → 데몬 loopback ingest | **이 계약 밖.** [로컬 파이프라인](local-pipeline.md) §7.1·§7.2 |
| **홉 1: 데몬 → 프록시** | **[데몬 → 프록시 계약](daemon-to-proxy-contract.md)** — 주소·헤더·상태코드 처분·재시도 예산·토큰 수명주기 |
| **홉 2: 프록시 → 원격 파이프라인** | **[프록시 → 파이프라인 계약](proxy-to-pipeline-contract.md)** — 헤더 allowlist·신뢰 경계·응답 반환·본문 상한 |
| 두 홉 공통 (불변식 · 관측 · 갭) | 이 문서 3·4·5절 |
| 무엇을 지우고 보내는가 (프라이버시) | [ADR 0003](adr/0003-원문과-tool-details를-로컬에만-보관.md), 집행 지점은 [홉 1](daemon-to-proxy-contract.md) 4절 |
| 왜 인라인 프록시 토폴로지인가 | [ADR 0001](adr/0001-로컬-OTLP-수신기-인라인-프록시-토폴로지.md) |

`§5.4` 같은 표기는 [설치 아키텍처](installation-architecture.md)의 절 번호다. 이 계약 전체를 지배하는
제약이 그 §5.4 — **"Collector 장애가 개발을 방해하면 안 된다"** — 이고, 홉 1 의 예산은 전부 거기서 나온다.

> **⚠ 배지에 대하여.** `⚠ 미반영 (Gn)` 이 붙은 항목은 **목표 계약**이고 현재 코드는 아직 따르지 않는다.
> 무엇이 어긋나 있는지는 5절 갭 표에서 같은 번호로 찾는다. 배지가 없는 항목은 현행 동작과 일치한다.

---

## 2. 토폴로지

```text
Claude Code / Codex                       ← 이 구간은 local-pipeline.md §7.2
   │ OTLP/HTTP  protobuf|json, identity|gzip, 최대 4 MiB(해제 후)
   ▼
telemetryctl daemon                       localhost:44318  ⚠ 미반영 (G1)
   │  receiver → pipeline → forward
   │  · 원본 바이트를 그대로 큐에 넣고 (daemon/pipeline.go:214-219)
   │  · 전송 직전 Scrub 으로 원문·tool details 제거 (ADR 0003)
   │
   │ 홉 1 ─ POST {manifest.otlp.endpoint}/v1/{signal}
   │ Content-Type: 수신 인코딩 그대로   (Content-Encoding 없음 — 비압축)
   │ Authorization: Bearer <telemetry_token>
   ▼
auth-proxy                                :4316  (PORT)
   │  · Bearer 토큰 → HMAC-SHA256 → enrollment.telemetry_tokens 조회
   │  · 본문은 불투명 바이트. 버퍼링만 하고 파싱하지 않는다
   │
   │ 홉 2 ─ POST {COLLECTOR_BASE_URL}/v1/{signal}
   │ Authorization 없음 · x-pulsemetry-{token,tenant,installation,member}-id 주입
   ▼
원격 파이프라인 (LB → Collector)
```

| 홉 | 주소 | 프로토콜 | 인증 | 요청 타임아웃 | 본문 상한 |
|---|---|---|---|---|---|
| 벤더 → 데몬 | `http://localhost:44318/v1/{signal}` ⚠ (G1) | http/protobuf·http/json | Bearer + `X-Pulsemetry-Local: 1` | — | 4 MiB (gzip 해제 후) |
| [홉 1](daemon-to-proxy-contract.md) | `{manifest.otlp.endpoint}/v1/{signal}` | http/protobuf (기본) | Bearer `telemetry_token` | 10s 또는 `manifest.otlp.timeout_ms` | 상한 없음 (상류에서 4 MiB 로 제한됨) |
| [홉 2](proxy-to-pipeline-contract.md) | `{COLLECTOR_BASE_URL}/v1/{signal}` | http/protobuf | 신원 헤더 4종 (Bearer 미전달) | 10s (하드코딩) | 10 MiB (`MAX_OTLP_BODY_SIZE`) |

`{signal}` 은 `metrics`·`logs`·`traces` 셋뿐이다. 데몬은 `otlpdecode.PayloadKind.Path()`
(`internal/otlpdecode/otlpdecode.go:92`)로, 프록시는 라우트 배열(`proxy/otlp.routes.ts:34`)로 만든다.
**두 목록이 갈리면 그 시그널만 404 로 조용히 사라진다.**

### 2.1 데몬 프로세스 좌표

전달의 주체가 누구인지 식별하기 위한 배경이다. 상세는 [로컬 파이프라인](local-pipeline.md) §7.1·§7.4.

- 수신 포트 기본값 **44318** ⚠ 미반영 (G1) — 현재 코드는 `4318`(`receiver.DefaultPort`).
- 포트가 사용 중이면 **임의 포트로 폴백**하고, 폴백한 포트로 **벤더 `settings.json` 을 동기화한다**
  ⚠ 미반영 (G2) — 현재는 경고 로그 + `runtime.json` 기록만 하고, 재병합은 `telemetryctl local enable`
  이 수동으로 한다(`daemon/runner.go:408-419`).
- `--listen` 을 명시하면 폴백 없이 하드 실패한다.
- 실제 바인딩 포트의 진실원은 `<data-dir>/runtime.json` 의 `listen_port` 다.

포트가 어디로 폴백하든 **홉 1·2 의 계약은 바뀌지 않는다.** 데몬의 수신 좌표는 상류(벤더)와의 문제이고,
프록시는 데몬이 몇 번 포트에서 듣는지 알 필요가 없다.

---

## 3. 양측이 깨면 안 되는 불변식

각 항목 끝의 `[홉n]` 은 그 규칙이 어느 구간에 걸리는지이고, 괄호는 근거가 있는 자식 문서의 절이다.

- [ ] `/v1/{metrics|logs|traces}` — 세 홉이 같은 경로 어휘를 쓴다. 한쪽이 경로를 늘리거나 이름을
      바꾸면 그 시그널만 404 로 사라진다. **[공통]** (2절)
- [ ] 프록시는 **일시적 실패에 4xx 를 쓰지 않는다.** 5xx 또는 429 여야 재시도된다.
      **[홉1]** ([데몬 → 프록시](daemon-to-proxy-contract.md) 5절)
- [ ] 프록시는 **인증 실패에 401·403 만 쓴다.** 그래야 데몬이 토큰 갱신 경로를 탄다.
      **[홉1]** (같은 문서 5·7절)
- [ ] 프록시는 **`Content-Type` 으로 분기하지 않는다.** 본문은 불투명 바이트다.
      **[홉1]** (같은 문서 3절)
- [ ] 프록시는 데몬이 보내는 두 헤더(`Content-Type`·`Authorization`) 외에 **아무것도 요구하지 않는다.**
      **[홉1]** (같은 문서 2절)
- [ ] `T(데몬 요청) > T(프록시 → 파이프라인) + 여유` **[공통]** (같은 문서 6절)
- [ ] 데몬은 **Scrub 없이 전송하지 않는다.** Scrub 실패는 폐기다(ADR 0003). **[홉1]** (같은 문서 4절)
- [ ] 데몬은 **`telemetry_token` 을 디스크·로그에 남기지 않는다.** **[홉1]** (같은 문서 7절)
- [ ] 프록시는 **파이프라인 상태코드를 변조하지 않는다.**
      **[홉2]** ([프록시 → 파이프라인](proxy-to-pipeline-contract.md) 3절)
- [ ] 프록시 응답 헤더 allowlist 에 **`retry-after` 가 남아 있어야 한다.** **[홉2]** (같은 문서 3절)
- [ ] `MAX_OTLP_BODY_SIZE ≥ 데몬 수신 상한` **[공통]** (같은 문서 4절)
- [ ] 파이프라인 ingress 는 **프록시로 제한된다.** 신원 헤더는 위조 가능하다. **[홉2]** (같은 문서 2절)

---

## 4. 관측 · 진단

**헬스 경로가 서로 다르다**(G9).

| 대상 | 경로 | 인증 | 응답 |
|---|---|---|---|
| 데몬 | `GET /healthz` | 없음 | `{status, service, queue_depth, queue_capacity, stats{…}}` |
| 프록시 | `GET /health` | 없음 | `{status: "ok"}` |

**데몬 쪽 카운터** — `forward.Stats`. 종료 시 "전달 요약" 한 줄로 찍히고 `telemetryctl status` 에도 나온다.

| 카운터 | 읽는 법 |
|---|---|
| `Sent` | 프록시가 2xx 로 받은 수 |
| `Discarded` | 4xx 로 **재시도 없이** 버린 수 — 계약 위반의 1차 신호 |
| `Failed` | 재시도 예산을 다 쓰고 실패 — 프록시·파이프라인 장애 |
| `Retries` | 재시도 대기 횟수 |
| `TokenErrors` | 토큰 확보 실패 — enroll 상태나 서버 문제 |
| `DroppedQueueFull` | 큐 포화 (프록시가 느릴 때 여기부터 오른다) |
| `DroppedScrub` | 정리 실패로 미전송 — 프라이버시 방어선이 작동한 것 |
| `AttributesRemoved`·`BodiesCleared` | Scrub 이 실제로 지운 양. 정책이 도는지 확인하는 유일한 지점 |

**프록시 쪽 로그** — `LOG_REQUEST_HEADERS=true` 일 때만 요청당 한 줄 JSON 이 찍힌다
(`shared/request-logger.ts`). 필드는 `requestId`·`method`·`path`·`status`·`durationMs`·
관측 헤더 6종·`auth{tokenId,tenantId,installationId,memberId}` 다. **토큰 평문은 어디에도 없다.**

진단 순서: 데몬 `Discarded`·`Failed` 가 오른다 → 프록시 로그에서 같은 시각 `status` 확인 →

| 상태 | 볼 곳 |
|---|---|
| `401` | [데몬 → 프록시](daemon-to-proxy-contract.md) 8절의 토큰 통과 조건 |
| `413` | [프록시 → 파이프라인](proxy-to-pipeline-contract.md) 4절의 본문 상한 |
| `5xx` | 파이프라인 쪽. 프록시 로그의 `durationMs` 가 10s 에 붙어 있으면 업스트림 타임아웃이다 |

---

## 5. 미해결 갭과 후속 작업

| # | 홉 | 갭 | 영향 | 근거 |
|---|---|---|---|---|
| G1 | 상류 | 수신 포트 44318 미반영 — 코드는 `4318` | 목표 계약과 현행 동작 불일치 | `receiver.DefaultPort`, `installer.DefaultLocalPort`, 문서 3곳 |
| G2 | 상류 | 폴백 시 `settings.json` 자동 동기화 미구현 | 폴백 후 `local enable` 을 다시 돌리기 전까지 텔레메트리가 아무 데도 도달하지 않는다 | `daemon/runner.go:408-419` |
| G3 | 홉1 | 데몬이 상위 응답의 `partial_success` 를 읽지 않는다 | 파이프라인이 일부를 거부해도 데몬은 성공으로 집계 — 부분 손실이 무관측 | `forward.go:496-500` |
| G4 | 공통 | 타임아웃 부등식 위반 — 데몬 10s == 프록시 업스트림 10s | 데몬이 먼저 끊고 재시도 → 같은 배치 중복 유입. 프록시 값이 env 가 아니라 하드코딩이라 운영 조정도 불가 | `forward.go:58`, `collector.client.ts:35` |
| G5 | 공통 | 본문 상한의 진실원이 두 저장소에 갈려 있다 | 현재는 부등식 만족이나 한쪽만 바뀌면 깨진다. 깨지면 큰 배치가 413 → 재시도 없이 소실 | `receiver.go:48`, `env.ts:30` |
| G6 | 홉1 | 데몬이 비압축으로 전송 (`Content-Encoding` 미설정) | 상위 구간 대역폭. gzip 재압축이나 pass-through 가 후속 과제 | `forward.go:488-489` — 설정하는 헤더가 이 둘뿐이다 |
| G7 | 홉1 | 프록시 오류 본문이 `{error, message}` JSON | OTLP 스펙은 `google.rpc.Status` 를 권장. 스펙 준수 클라이언트가 붙으면 파싱 실패 | `error-handler.ts` |
| G8 | 홉2 | 페이로드 안의 `installation_id` 와 토큰 신원을 대조하지 않는다 | 유효 토큰 하나로 남의 설치를 자칭하는 본문 주입 가능 | `collector.client.ts:26-29` |
| G9 | 공통 | 헬스 경로 명명 불일치 `/healthz` vs `/health` | 운영·모니터링 설정 혼선 | `receiver.HealthPath`, `health.routes.ts` |

우선순위 제안: **G4 → G2 → G3 → G5** 순. G4 는 지금도 중복 데이터를 만들 수 있고, G2 는 조용한 전량
손실이며, G3 는 그 손실을 보이게 만드는 전제 조건이다. G5 는 값을 바꾸는 순간 터지는 지뢰라 이 계약이
문서로 있는 동안에는 예방이 가능하다.
