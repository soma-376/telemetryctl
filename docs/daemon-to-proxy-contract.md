# 홉 1: 데몬 → 프록시

> 이 문서는 **`telemetryctl` 데몬이 auth-proxy 로 보내는 구간**만 다룬다.
> 토폴로지 전체와 두 홉을 가로지르는 내용은 [상위 전달 계약](telemetry-egress-contract.md)에 있다 —
> **불변식 체크리스트는 그 문서 3절, 관측·진단은 4절, 갭 표는 5절**이다.
> 본문의 `(Gn)` 은 그 갭 표의 번호이고, `§5.4` 같은 표기는
> [설치 아키텍처](installation-architecture.md)의 절 번호다.

전송 코드는 `internal/forward/forward.go:415-501`, 처분 판정은 `internal/forward/retry.go` 다.

---

## 1. 대상 주소

```text
{manifest.otlp.endpoint 의 뒤 슬래시 제거} + /v1/{metrics|logs|traces}
```

- `endpoint` 는 회사 manifest 에서 온다. **데몬은 자기 마음대로 주소를 만들지 않는다.**
- 검증은 두 겹이다. `contract.validOTLPEndpoint`(`internal/contract/manifest.go:83-89`)가
  **https 만 허용하되 `http` 는 리터럴 호스트 `localhost` 에만** 허용하고,
  `forward.normalizeEndpoint` 가 scheme·host 를 한 번 더 본다.
- 따라서 프록시를 평문 HTTP 로 붙이려면 주소가 반드시 `http://localhost:4316` 형태여야 한다.
  `http://127.0.0.1:4316` 은 **manifest 검증 단계에서 거부**되므로 개발 환경에서도 쓸 수 없다.
- 오류 메시지에 endpoint 를 넣지 않는다. `https://user:secret@…` 형태면 그 자체가 자격증명 유출이다.

## 2. 요청 헤더 — 데몬이 보내는 것의 전부

| 헤더 | 값 | 비고 |
|---|---|---|
| `Content-Type` | `application/x-protobuf` 또는 `application/json` | **벤더에게서 받은 인코딩을 그대로 미러링한다** |
| `Authorization` | `Bearer <telemetry_token>` | 7절 |

**이 둘이 전부다.** 커스텀 헤더도, `Content-Encoding` 도, `X-Pulsemetry-Local` 도 붙지 않는다
(`X-Pulsemetry-Local` 은 loopback 수신기 전용이다). 프록시가 이외의 헤더를 요구하기 시작하면
**모든 배치가 401 로 폐기**된다 — 데몬은 4xx 를 재시도하지 않으므로 영구 손실이다.

`Content-Encoding` 이 없다는 것은 **본문이 비압축**이라는 뜻이다(G6). 데몬이 벤더에게 받은 gzip 은
수신 단계에서 이미 풀렸고(`receiver/body.go`), 포워더는 다시 압축하지 않는다. 이 성질이
[프록시 → 파이프라인](proxy-to-pipeline-contract.md) 4절의 본문 상한 비교를 성립시킨다.

## 3. 직렬화 — 프록시는 본문을 해석하지 않는다

- `manifest.otlp.protocol` 은 `http/protobuf` 또는 `http/json` 이어야 한다. `grpc` 는
  `forward.ErrGRPCUnsupported` 로 **거부**하고 데몬 기동이 실패한다(조용히 HTTP 로 보내지 않는다).
- 주의: **프로토콜 값이 나가는 인코딩을 정하지 않는다.** 나가는 인코딩은 언제나 벤더가 보낸 것을
  따른다. `manifest` 가 `http/protobuf` 인데 벤더가 JSON 을 보냈다면 JSON 이 그대로 나간다.
- 프록시는 본문을 `Buffer` 로 모으기만 하고 파싱하지 않는다(`proxy/otlp.routes.ts:16-26`). 따라서
  이 홉에서 **직렬화 형식은 실질적으로 agnostic** 하다 — 계약상 의미 있는 것은 `Content-Type` 헤더가
  본문과 일치한다는 것뿐이고, 그 일치는 파이프라인 끝단에서 처음 검증된다.
- 그러므로 **프록시는 `Content-Type` 을 보고 분기하는 로직을 만들면 안 된다.** 만드는 순간 이 홉이
  인코딩에 종속되고, 벤더가 JSON 을 보내는 환경에서만 깨지는 버그가 된다.

## 4. 프라이버시 집행 지점

전송 **직전**에 `otlpdecode.Scrub` 이 한 번 돈다. 기준은 `otlpdecode.PolicyFromPrivacy(회사 manifest
원본의 Privacy)` 이고, 포워더에는 제거 규칙이 하나도 하드코딩돼 있지 않다.

- `local enable` 이 만드는 **로컬 사본이 아니라 회사 manifest 원본**이 기준이다 — 재배선 전후로
  회사에 나가는 데이터가 동일해야 한다([ADR 0003](adr/0003-원문과-tool-details를-로컬에만-보관.md),
  [로컬 파이프라인](local-pipeline.md) §4.2).
- **Scrub 실패는 전송하지 않는다.** 정리되지 않은 본문을 흘려보내느니 버린다(`DroppedScrub`).
- 인코딩은 입력과 동일하게 유지된다.

프록시는 이 정리를 **다시 하지 않는다.** 프록시가 본문을 파싱하지 않는다는 3절의 성질과 같은 이야기다.
원문 제거의 책임은 전적으로 데몬에 있고, 파이프라인 쪽 redaction 이 이중 방어다.

## 5. 응답 상태코드 → 데몬의 처분

`internal/forward/retry.go:30-52` 의 `classify` 가 이 표 그대로다. **프록시가 상태코드를 고르는 것은
데몬의 재시도 동작을 고르는 것과 같다.**

| 프록시 응답 | 데몬 처분 | 결과 |
|---|---|---|
| `2xx` | Done | `Sent++` |
| `401`, `403` | **토큰 갱신 후 1회 재시도** | 캐시 무효화 → 새 토큰으로 즉시 재시도(백오프 없음). 갱신 후에도 거부되면 `Discarded++` |
| `408`, `425`, `429` | 재시도 | 예산 안에서. `Retry-After` 존중 |
| 그 밖의 `4xx` (400·404·413·415·422 …) | **즉시 폐기** | `Discarded++`. **재시도하지 않는다** |
| `5xx` | 재시도 | 예산 안에서 |
| 전송 오류(연결 거부·타임아웃·DNS) | 재시도 | 예산 안에서 |
| `1xx`, `3xx` | 즉시 폐기 | 우리가 모르는 상황이라 재시도할 근거가 없다 |

여기서 **양쪽이 반드시 지켜야 할 규칙**:

- **프록시는 일시적 실패에 4xx 를 쓰면 안 된다.** DB 커넥션 고갈, 업스트림 장애, 부하 제한은 전부
  `5xx` 또는 `429` 여야 한다. `400`·`422` 로 답하는 순간 그 배치는 재시도 없이 사라진다.
- **인증 실패는 반드시 `401`(또는 `403`)이어야 한다.** 다른 코드로 답하면 데몬이 토큰 갱신 경로를
  타지 않고, 만료된 토큰인 채로 모든 배치를 폐기한다.
- **데몬은 응답 본문을 읽지 않는다.** 연결 재사용을 위해 최대 4 KiB 를 읽어 버릴 뿐 파싱하지 않는다
  (`forward.go:496-500`). 즉 프록시가 본문에 무엇을 적든 데몬의 동작은 상태코드만으로 결정된다.
  OTLP 의 `partial_success` 도 여기서 소실된다(G3).
- 프록시의 오류 본문은 `{"error": "<code>", "message": "<사람이 읽는 설명>"}` 형태다
  (`shared/errors/error-handler.ts`). OTLP 스펙이 권장하는 `google.rpc.Status` 가 아니다(G7).
- 프록시는 `/v1/*` 에 `POST` 라우트만 등록한다. 다른 메서드는 라우터를 통과해 Express 기본 404 로
  떨어진다 — 데몬은 `POST` 만 쓰므로 이 경로를 밟지 않는다.

## 6. 재시도 · 타임아웃 · 큐 예산

**재시도**(`forward.go:52-56`, `retry.go:54-105`)

- 총 **3회 시도**(최초 포함). 대기는 `500ms → 1s → …`, 상한 `15s`.
- **equal jitter** — 실제 대기는 `[d/2, d)` 에서 균등 추출. 지터가 없으면 프록시가 복구되는 순간
  모든 개발자 머신이 같은 간격으로 동시에 재시도해 방금 살아난 상위를 다시 밀어 버린다.
- `Retry-After` 는 초 단위와 HTTP-date 를 모두 해석하고 **15s 로 클램프**한다. 프록시가
  `Retry-After: 3600` 을 주더라도 워커가 한 시간 잠기지는 않는다 — 그동안의 텔레메트리가 전부 큐에서
  버려지는 편이 더 나쁘다. 실제 대기는 `max(백오프, Retry-After)` 다.
- `401`·`403` 뒤의 재시도만 **백오프 없이 즉시** 나간다(토큰 문제는 시간이 해결하지 않는다).

**타임아웃 — 값이 아니라 부등식이 계약이다**

```text
T(데몬 요청) > T(프록시 → 파이프라인) + 프록시 처리 여유(인증 DB 조회 + 본문 버퍼링)
```

바깥(데몬)이 먼저 끊기면 데몬은 응답을 못 본 채 재시도하고, 그때 프록시는 **여전히 상위로 보내는
중**이라 같은 배치가 파이프라인에 두 번 들어간다. 안쪽(프록시)이 먼저 끊겨야 프록시가 `5xx` 를 돌려주고
데몬이 그것을 보고 재시도 여부를 판단할 수 있다.

현행 값(사실 기록):

| 구간 | 값 | 출처 |
|---|---|---|
| 데몬 요청 | `manifest.otlp.timeout_ms`, 없으면 `10s` | `forward.go:58,180-186` |
| 프록시 → 파이프라인 | `10s` (하드코딩, env 없음) | `proxy/collector.client.ts:35` |

**둘이 같아서 현재 이 부등식은 만족되지 않는다** → G4.

**큐 예산**(`forward.go:47-50`, `276-303`)

- 항목 수 **64건** AND 바이트 총량 **32 MiB**. 둘 중 하나라도 넘으면 새 배치를 **버린다**.
  항목 수만으로는 부족하다 — 64 × 4 MiB 면 256 MiB 가 된다.
- `Enqueue` 는 **절대 블로킹하지 않는다.** 여기서 기다리면 역압이 수신기를 지나 벤더 exporter 까지
  번져 Claude Code 가 느려진다(§5.4). 텔레메트리 손실은 허용, 개발 도구 지연은 불허다.
- 워커는 **하나**다. 순서를 보존하고, 상위가 죽었을 때 동시 재시도로 몰려가지 않게 한다.
- 종료 시 큐를 비울 기회는 전체 종료 예산(15s)의 1/3 이다. 넘기면 남은 것을 버린다
  (`DroppedShutdown`).

## 7. `telemetry_token` 수명주기

```text
enroll  ──→ installation_token (OS 키링, 영구)
              │  설치 자격증명을 telemetry 자격증명으로 교환
              ▼
           telemetry_token  ── 프로세스 메모리에만 존재 ──→ Authorization 헤더
```

- 토큰은 **디스크에 쓰이지 않는다.** `state.json` 에도 `runtime.json` 에도 로그에도 없다
  (`forward/token.go:22-31`, §4.5).
- 데몬은 15분마다 토큰 확보 가능 여부만 확인한다(`DefaultTokenInterval`). 값은 버린다 — 첫 배치가
  아니라 **지금** 실패를 발견하기 위한 틱이다.
- 프록시가 `401`·`403` 을 주면 `Invalidate(stale)` 로 캐시를 버리고 재발급받아 한 번만 다시 시도한다.
  `Invalidate` 는 캐시가 그 토큰과 같을 때만 동작한다 — 다른 시도가 이미 갱신했으면 아무 일도 하지 않는다.
- 재발급 호출에는 10s 상한이 걸려 있다(`token.go:20`). 서버가 응답하지 않으면 워커가 영원히 잠기고
  그 뒤 텔레메트리가 전부 버려지기 때문이다.
- 재발급 오류 메시지에서 설치 토큰 문자열을 `<redacted>` 로 치환한다(`stripSecret`). 서버 응답 본문이
  오류에 섞여 오는 경로까지 막는다.

## 8. 프록시 측 검증 — 토큰이 통과하는 조건

`auth/auth.middleware.ts` → `auth/credential.service.ts` → `auth/credential.repository.ts`.

1. `Authorization` 이 `/^Bearer\s+([^\s]+)$/i` 에 맞아야 한다. 아니면 `401 unauthorized`.
   (스킴 대소문자는 가리지 않는다. 데몬은 `Bearer ` 로 보낸다.)
2. 토큰을 `HMAC-SHA256(token, TOKEN_HASH_SECRET)` 의 **hex** 로 해시한다. 평문은 저장·조회 어디에도
   쓰이지 않는다.
3. `enrollment.telemetry_tokens` 를 해시로 조회하되 **아래 조건이 모두** 참이어야 한다.

   | 조건 | 의미 |
   |---|---|
   | `tt.revoked_at IS NULL` | 토큰이 폐기되지 않음 |
   | `i.revoked_at IS NULL AND i.status = 'active'` | 설치가 살아 있음 |
   | `m.status = 'active'` | 구성원이 활성 |
   | `t.deleted_at IS NULL AND t.status = 'active'` | 테넌트가 활성 |

4. 통과하면 `{tokenId, tenantId, installationId, memberId}` 를 요청에 붙인다. 이 네 값이
   [프록시 → 파이프라인](proxy-to-pipeline-contract.md) 2절의 신원 헤더가 된다.

**`TOKEN_HASH_SECRET` 이 바뀌면 발급된 모든 토큰이 즉시 무효**가 된다. 데몬 쪽에서는 `401` → 갱신 →
다시 `401` → 전량 폐기로 나타난다. 시크릿 교체는 토큰 재발급과 짝을 이뤄야 한다.
