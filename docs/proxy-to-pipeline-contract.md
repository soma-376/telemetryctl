# 홉 2: 프록시 → 원격 파이프라인

> 이 문서는 **auth-proxy 가 원격 파이프라인(LB → Collector)으로 보내는 구간**만 다룬다.
> 토폴로지 전체와 두 홉을 가로지르는 내용은 [상위 전달 계약](telemetry-egress-contract.md)에 있다 —
> **불변식 체크리스트는 그 문서 3절, 관측·진단은 4절, 갭 표는 5절**이다.
> 본문의 `(Gn)` 은 그 갭 표의 번호다.

구현은 `proxy/otlp.controller.ts` 와 `proxy/collector.client.ts` 다.

---

## 1. 대상 주소

```text
{COLLECTOR_BASE_URL 의 뒤 슬래시 제거} + {요청 경로 그대로}
```

경로를 다시 만들지 않고 `request.path` 를 그대로 이어 붙인다. 데몬이 `/v1/logs` 로 보냈으면
파이프라인에도 `/v1/logs` 로 간다. 즉 **세 홉이 같은 경로 어휘를 공유**한다.

## 2. 헤더 계약 — 여기가 신뢰 경계다

**전달하는 요청 헤더는 allowlist 3종뿐이다**(`collector.client.ts:4-8`).

```text
content-type · content-encoding · accept
```

**그 밖의 모든 헤더는 버려진다. `Authorization` 도 마찬가지다.** 대신 프록시가 네 개를 주입한다.

| 헤더 | 출처 |
|---|---|
| `x-pulsemetry-token-id` | `telemetry_tokens.id` |
| `x-pulsemetry-tenant-id` | `installations.tenant_id` |
| `x-pulsemetry-installation-id` | `installations.id` |
| `x-pulsemetry-member-id` | `installations.member_id` |

네 값의 출처는 [데몬 → 프록시](daemon-to-proxy-contract.md) 8절의 토큰 검증이다.

이 구조가 뜻하는 것:

- **파이프라인에게 이 네 헤더는 유일한 신원 근거다.** 원본 Bearer 토큰은 도달하지 않으므로 파이프라인이
  독자적으로 재검증할 방법이 없다.
- 따라서 **파이프라인은 프록시를 거치지 않은 트래픽을 받아서는 안 된다.** 헤더는 누구나 만들 수 있고,
  프록시를 우회할 수 있으면 테넌트 위조가 헤더 한 줄로 가능해진다. 네트워크 수준에서 파이프라인의
  ingress 를 프록시로 제한하는 것이 이 계약의 전제다.
- **프록시는 클라이언트가 보낸 `x-pulsemetry-*` 를 무시하고 항상 덮어쓴다**(`headers.set`). 데몬이
  이 헤더를 보내지 않는 것도 계약이다 — 보내기 시작하면 위 성질을 지키는지 검증할 테스트가 필요해진다.
- 프록시는 페이로드 **안**의 `installation_id` 와 토큰의 신원을 대조하지 않는다(G8). 즉 유효한 토큰
  하나로 남의 `installation_id` 를 자칭하는 본문을 밀어 넣을 수 있다.

## 3. 응답 반환 계약

- **상태코드는 파이프라인의 것을 그대로** 데몬에게 돌려준다(`otlp.controller.ts:22`). 프록시가 여기서
  코드를 바꾸면 [데몬 → 프록시](daemon-to-proxy-contract.md) 5절의 처분 표가 통째로 어긋난다.
- 응답 헤더는 allowlist 3종만 돌려준다: `content-type` · `content-encoding` · **`retry-after`**.
  `Retry-After` 가 이 목록에 있어야 파이프라인의 부하 제한이 데몬의 백오프에 반영된다
  ([데몬 → 프록시](daemon-to-proxy-contract.md) 6절). 빼면 데몬은 자기 지수 백오프로만 재시도해
  상위 복구를 지연시킨다.
- 응답 본문은 바이트 그대로 전달된다. 다만 데몬이 읽지 않으므로(같은 문서 5절) 실질적으로 소비되지 않는다.
- 파이프라인 호출이 예외로 끝나면(네트워크 오류·`AbortSignal` 타임아웃) `AppError` 가 아니므로
  `errorHandler` 가 `500 internal_error` 로 답한다 → 데몬은 재시도한다. 올바른 방향이다.

## 4. 본문 상한 · 업스트림 타임아웃

**상한도 값이 아니라 부등식이 계약이다.**

```text
MAX_OTLP_BODY_SIZE(프록시) ≥ 데몬 수신 상한(gzip 해제 후)
```

비교가 성립하는 근거는 [데몬 → 프록시](daemon-to-proxy-contract.md) 2절이다 — 데몬이 **비압축**으로
보내므로 프록시가 재는 크기는 데몬이 잰 해제 후 크기와 같은 단위다(사과-사과 비교).

위반 시 증상: 프록시가 `413` → 데몬 `classify` 가 "그 밖의 4xx" 로 보고 **재시도 없이 폐기**
(`retry.go:44-46`) → **큰 배치만 골라 조용히 영구 손실**된다. 카운터(`Discarded`)만 오르고 화면에는
아무 증상이 없다.

현행 값(사실 기록):

| 구간 | 값 | 출처 |
|---|---|---|
| 데몬 수신 (해제 후) | 4 MiB | `receiver.DefaultMaxBodyBytes` |
| 프록시 | 10 MiB | `MAX_OTLP_BODY_SIZE` 기본값 |

현재 부등식은 만족한다(여유 2.5배). 다만 두 값이 서로를 참조하지 않아 한쪽만 바뀌면 깨진다(G5).

프록시의 크기 검사는 **이중**이다(`otlp.routes.ts:11-25`).

1. `content-length` 헤더 선판정 — 헤더가 없으면 `0` 으로 취급해 통과한다.
2. 스트리밍 누적 검사 — 청크를 모으며 초과하는 순간 `413`.

헤더가 없거나 거짓이어도 (2)가 잡으므로 안전하다. 데몬은 `bytes.NewReader` 로 보내 `Content-Length`
가 언제나 정확하므로 실제로는 (1)에서 판정된다.

업스트림 타임아웃(`AbortSignal.timeout`)은 [데몬 → 프록시](daemon-to-proxy-contract.md) 6절의
부등식에 종속된다.
