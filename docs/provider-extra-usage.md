# 프로바이더별 추가 사용량 응답 조사

## 핵심 결론

Claude Code와 Codex는 플랜 한도를 넘긴 뒤의 사용을 같은 방식으로 표현하지 않는다.

- Claude의 `extra_usage`는 **추가 사용량 기능의 활성화 여부, 월 지출 한도로 보이는 값, 누적
  사용량으로 보이는 값**을 한 객체에 담는 것으로 관측됐다. 이 응답은 비공개 API에서 반환되므로
  필드 계약과 단위는 공식 문서로 보장되지 않는다. 관측한 표본의 `utilization`은 모두 `null`이었다.
- Codex의 `account/rateLimits/read`는 **크레딧 잔액, 무제한 여부, 개인 지출 제어, 한도 도달 원인,
  다중 한도 버킷과 일회성 한도 재설정 크레딧**을 서로 다른 필드로 제공한다.
- 현재 `ExtraAllowance`는 `Supported`, `Enabled`, `UsedRatio`만 가진 최소 공통 모델이다. Claude에서는
  금액과 비활성화 사유를 버리고, Codex에서는 잔액·무제한·지출 제어를 `Enabled` 하나로 합친다.

따라서 `ExtraAllowance.Enabled=true`만으로는 "얼마나 더 쓸 수 있는가", "상한이 없는가", "지출
제어에 걸렸는가"에 답할 수 없다. `UsedRatio=0`만으로도 실제 0%와 미확인을 구분하지 못한다.

## 근거 수준

이 문서는 정책과 응답 형식의 근거를 다음 다섯 가지 수준으로 나눠 기록한다.

| 수준 | 뜻 | 사용 범위 |
|---|---|---|
| 공식 정책 | 프로바이더가 도움말에서 설명한 상품·과금 동작 | 추가 사용량이 적용되는 시점과 지출 제한의 의미 |
| 공개 스키마 | 프로바이더가 공개 문서에 명시한 요청·응답 필드 | Codex App Server의 공개 응답 구조 |
| 버전 한정 관측 | 설치된 CLI가 생성한 스키마, 바이너리 문자열, 공개 이슈에 실린 응답 예시 | 공개 계약에 없는 필드의 존재 확인 |
| 현재 코드 | `telemetryctl`이 실제로 파싱·정규화·저장·표시하는 동작 | 현재 보존되는 정보와 손실되는 정보 |
| 미확인 | 공개 계약이나 여러 계정 상태로 검증하지 못한 내용 | 필드 단위, `null`·누락의 의미, 플랜별 차이 |

`버전 한정 관측`은 공식 API 계약이 아니다. CLI나 비공개 API가 바뀌면 다시 확인해야 한다.

## Claude

### 결론

Claude의 `usage credits`는 구독에 포함된 사용 한도를 소진한 뒤 표준 API 요율로 계속 사용하기 위한
종량제 수단이다. 사용자는 월 지출 상한을 설정하거나 `unlimited`로 지정할 수 있다. 여기서
`unlimited`는 **포함 사용량이 무제한이라는 뜻이 아니라 월 지출 상한을 두지 않는다는 뜻**이다.

공식 정책은 과금 동작을 설명하지만 `GET /api/oauth/usage`의 `extra_usage` JSON 계약은 공개하지 않는다.
아래 표는 공식 정책, 현재 코드, 설치된 Claude Code `2.1.263`, Anthropic의 공개 저장소에 접수된 응답
예시를 근거 수준에 따라 구분한다.

### 공식 정책

Pro·Max 플랜의 개인 사용자는 `usage credits`를 활성화해 포함 한도를 소진한 뒤에도 계속 사용할 수
있다. 월 지출 상한이나 상한 없음 여부를 정하고 선불 충전과 자동 충전을 설정할 수 있으며, 이후
사용은 표준 API 요율로 과금된다. `usage credits`는 Claude Code 사용에도 적용된다. 근거는 [C1]이다.

Team과 좌석 기반 Enterprise 플랜에서는 조직 단위와 사용자 단위로 지출 상한을 둘 수 있다. 좌석
단위(Standard·Premium 좌석) 상한은 좌석 기반 Enterprise에서만 설정할 수 있다. 사용자 단위 한도를
`unlimited`로 설정해도 조직 또는 좌석 단위 상한은 계속 적용된다. 근거는 [C2]다.

이 정책만으로는 `extra_usage`의 숫자 단위, `null`과 누락의 차이, `disabled_reason`의 값 목록을
확정할 수 없다.

### 관측된 응답

다음 JSON은 관측된 필드 조합을 보여 주기 위한 **합성 예시**다. 숫자는 실제 계정 값이 아니다. 특히
`utilization: 12.5`는 관측값이 아니다. 관측한 표본은 [C3]의 `is_enabled: true, used_credits: 0.0,
utilization: null`과 `internal/vendorlimit/claude.go` 주석의 `is_enabled: false, utilization: null` 둘뿐이며,
둘 다 `utilization`이 `null`이다.

```json
{
  "extra_usage": {
    "is_enabled": true,
    "monthly_limit": 10000,
    "used_credits": 1250.0,
    "utilization": 12.5,
    "currency": "USD",
    "disabled_reason": null
  }
}
```

| 필드 | 관측된 의미 | 합성 예시 | 현재 코드 반영 | 근거 수준 |
|---|---|---:|---|---|
| `is_enabled` | 추가 사용량 기능의 활성화 상태 | `true` | `Enabled`로 보존 | 버전 한정 관측, 현재 코드 |
| `monthly_limit` | 월 한도로 보이는 값 | `10000` | 버림 | 버전 한정 관측, 단위 미확인 |
| `used_credits` | 월 누적 사용량으로 보이는 값 | `1250.0` | 버림 | 버전 한정 관측, 단위·기간 미확인 |
| `utilization` | 월 한도 소진율로 보이는 값. 관측한 표본은 모두 `null`이며 퍼센트 단위는 코드의 가정 | `12.5` | 100으로 나눠 `UsedRatio`로 보존 | 버전 한정 관측(`null`만), 현재 코드, 단위 미확인 |
| `currency` | 금액 통화로 보이는 문자열 | `"USD"` | 버림 | 버전 한정 관측, 형식 미확인 |
| `disabled_reason` | 기능이 꺼진 이유로 보이는 값. `null`일 수 있음 | `null` | 버림 | 버전 한정 관측, 값 목록 미확인 |

Anthropic의 Claude Code 공개 저장소 이슈 [C3]에는 위 여섯 필드를 모두 포함한 사용자 제보 응답이
실려 있다. 이 기능 요청은 #50847의 중복으로 닫혔으며, #50847에는 관측 응답이 아닌 제안 필드만 있다.
설치된 `2.1.263` 바이너리 문자열에도 여섯 필드 이름이 모두 존재한다. 다만 `currency`와 `utilization`은
다른 용도로도 쓰이는 일반 단어이므로 `extra_usage` 전용 근거로 볼 수 없다. 이 근거들은 필드의
존재를 뒷받침하지만 서버 계약이나 모든 플랜의 응답을 보장하지 않는다.

현재 코드는 과거 필드명인 `enabled`도 함께 받는다. `enabled || is_enabled`를 `Enabled`로 옮기므로 둘 중
하나가 참이면 활성 상태가 된다. 현재 테스트의 모의 응답은 `enabled: true`, `utilization: 10.0`만
검증한다. 나머지 필드와 `is_enabled`의 `null`·누락 조합은 검증하지 않는다.

### 현재 코드의 손실

`internal/vendorlimit/claude.go`의 `claudeUsageResponse.ExtraUsage`와 `extra()`가 현재 파싱·정규화
경계다.

- `extra_usage`가 없으면 `ExtraAllowance{}`가 되어 `Supported=false`다.
- `extra_usage` 객체가 있으면 `Supported=true`다.
- `enabled || is_enabled`만 `Enabled`로 보존한다.
- `utilization`을 100으로 나눠 `UsedRatio`로 보존한다.
- `monthly_limit`, `used_credits`, `currency`, `disabled_reason`은 구조체에 없어 JSON 디코딩 과정에서
  버린다.
- `utilization: null`은 포인터가 아닌 `float64`의 영값 `0`이 된다. `ratioFromPercent()`는 `NaN`·`Inf`·
  음수도 모두 `0`으로 축약한다. 이 과정에서 미확인과 실제 0%가 합쳐진다.

정규화된 `ExtraAllowance`는 SQLite의 `extra_json`에 저장된 뒤 트레이 스냅샷으로 다시 읽힌다. 그러나
현재 프런트엔드의 `toVendor()`는 `Extra`를 표시 모델로 옮기지 않으므로 사용자는 추가 사용량 상태를
볼 수 없다.

## Codex

### 결론

Codex는 추가 사용 가능성을 `credits` 하나로만 표현하지 않는다. 현재 App Server 응답은 포함 사용량
창, 추가 크레딧, 개인 지출 제어, 한도 도달 원인, 여러 계량 버킷, 일회성 한도 재설정 권한을 각각
표현할 수 있다.

Plus와 Pro 사용자는 포함 사용 한도에 도달한 뒤 추가 크레딧을 구매해 계속 사용할 수 있다. Business,
Edu와 Enterprise의 `flexible pricing`에서도 워크스페이스 크레딧을 추가할 수 있다. 크레딧은 포함 한도
이후의 대상 사용량(`eligible usage`)에 쓰이는 단위다. 근거는 [O2]다.

### 공개 응답과 설치 버전 스키마

공식 App Server 문서 [O1]에는 `account/rateLimits/read`와 다음 응답 구조가 공개돼 있다.

- `rateLimits`: 이전 클라이언트를 위한 단일 버킷 뷰
- `rateLimitsByLimitId`: `limitId`별 다중 버킷 뷰
- `credits`: 서버가 제공하는 경우에 포함되는 워크스페이스 잔여 크레딧 정보
- `rateLimitReachedType`: 서버가 분류한 한도 도달 원인
- `rateLimitResetCredits`: 부여받은 일회성 한도 재설정 권한

설치된 Codex CLI `0.153.4`가 생성한 JSON Schema에서는 `credits.balance`, `credits.hasCredits`,
`credits.unlimited`, `individualLimit`, `spendControlReached`의 구체적인 형식도 확인할 수 있다. 이 추가
정보는 버전 한정 관측으로 취급한다.

다음 JSON은 공개 문서와 설치 스키마의 필드를 한 번에 보여 주기 위한 **합성 예시**다. 식별자, 값,
시각은 실제 계정 데이터가 아니다.

```json
{
  "rateLimits": {
    "limitId": "codex",
    "limitName": null,
    "planType": "plus",
    "primary": {
      "usedPercent": 25,
      "windowDurationMins": 300,
      "resetsAt": 1893456000
    },
    "secondary": null,
    "credits": {
      "hasCredits": true,
      "unlimited": false,
      "balance": "42.50"
    },
    "individualLimit": {
      "limit": "100.00",
      "used": "25.00",
      "remainingPercent": 75,
      "resetsAt": 1896134400
    },
    "spendControlReached": false,
    "rateLimitReachedType": null
  },
  "rateLimitsByLimitId": {
    "codex": {
      "limitId": "codex",
      "primary": {
        "usedPercent": 25,
        "windowDurationMins": 300,
        "resetsAt": 1893456000
      }
    }
  },
  "rateLimitResetCredits": {
    "availableCount": 1,
    "credits": [
      {
        "id": "synthetic-reset-credit",
        "resetType": "codexRateLimits",
        "status": "available",
        "grantedAt": 1890864000,
        "expiresAt": 1896134400,
        "title": "Rate-limit reset",
        "description": "Reset an eligible Codex rate-limit window."
      }
    ]
  }
}
```

| 필드 | 의미 | 합성 예시 | 현재 코드 반영 | 근거 수준 |
|---|---|---:|---|---|
| `credits.hasCredits` | 사용할 크레딧이 있는지 나타내는 상태 | `true` | `Enabled` 계산에 사용 | 버전 한정 관측, 현재 코드 |
| `credits.unlimited` | 크레딧 사용이 무제한 상태인지 나타내는 값 | `false` | `Enabled` 계산에 사용하되 별도 의미는 버림 | 버전 한정 관측, 현재 코드 |
| `credits.balance` | `null`일 수 있는 `decimal` 문자열 잔액 | `"42.50"` | 파싱 후 `ExtraAllowance`에서 버림 | 버전 한정 관측, 현재 코드, 단위 미확인 |
| `individualLimit.limit` | 개인 지출 한도 문자열 | `"100.00"` | 응답 타입에서 파싱하지 않음 | 버전 한정 관측, 단위 미확인 |
| `individualLimit.used` | 개인 지출 사용량 문자열 | `"25.00"` | 응답 타입에서 파싱하지 않음 | 버전 한정 관측, 단위 미확인 |
| `individualLimit.remainingPercent` | 개인 지출 한도의 잔여 퍼센트 | `75` | 응답 타입에서 파싱하지 않음 | 버전 한정 관측 |
| `individualLimit.resetsAt` | 개인 지출 한도 초기화 Unix 시각 | `1896134400` | 응답 타입에서 파싱하지 않음 | 버전 한정 관측 |
| `spendControlReached` | 서버가 보고한 지출 제어 도달 상태. `null`은 미확인을 뜻함 | `false` | 응답 타입에서 파싱하지 않음 | 버전 한정 관측 |
| `rateLimitReachedType` | 사용 중단 원인을 구분하는 서버 분류 | `null` | 응답 타입에서 파싱하지 않음 | 공개 스키마, 버전 한정 관측 |
| `rateLimitsByLimitId` | 계량 대상별 한도 버킷 | `{"codex": ...}` | 최상위 응답에서 파싱하지 않음 | 공개 스키마 |
| `rateLimitResetCredits` | 한도 창을 즉시 재설정할 수 있는 별도 권리 | `availableCount: 1` | 최상위 응답에서 파싱하지 않음 | 공개 스키마 |

`0.153.4` 스키마의 `rateLimitReachedType`에는 일반 한도 도달, 워크스페이스 소유자·구성원의 크레딧
소진, 워크스페이스 소유자·구성원의 `usage limit` 도달이 서로 다른 값으로 정의돼 있다. 이 정보는
단순한 `Enabled=false`보다 사용자가 취할 수 있는 조치를 더 정확히 알려 준다.

`rateLimitResetCredits`는 잔액형 사용 크레딧과 이름만 비슷하다. 공식 문서상 이 값은 대상 한도
창(`eligible rate-limit window`)을 한 번 재설정할 수 있도록 부여된 권한이다. `availableCount`가 사용
가능 횟수의 정본이며, 상세 `credits` 배열은 없거나 일부만 올 수 있다. 따라서 이를 `credits.balance`나
추가 사용량 잔액과 합치면 안 된다.

### 현재 코드의 손실

`internal/codexapp/types.go`는 `rateLimits`의 단일 버킷과 그 안의 `CreditsSnapshot`만 파싱한다.
`CreditsSnapshot`은 `HasCredits`, `Unlimited`, `Balance`를 보존하지만,
`internal/vendorlimit/codex.go`의 `codexExtra()`에서 이 정보를 다시 축약한다.

- `credits`가 없으면 `ExtraAllowance{}`가 되어 `Supported=false`다.
- `credits`가 있으면 `Supported=true`다.
- `hasCredits || unlimited`를 `Enabled` 하나로 합친다.
- `balance`를 버리고 `UsedRatio`는 영값 `0`으로 둔다.
- `individualLimit`, `spendControlReached`, `rateLimitReachedType`을 Go 응답 타입에서 파싱하지 않는다.
- `rateLimitsByLimitId`와 `rateLimitResetCredits`를 최상위 Go 응답 타입에서 파싱하지 않는다.

현재 테스트는 `hasCredits: true`인 경우와 `credits` 자체가 없는 경우만 검증한다. `unlimited=true`, 잔액
`0`·양수·`null`, 지출 제어 도달, 여러 버킷, 한도 재설정 크레딧 상태는 현재 모델의 테스트 범위
밖이다.

## 현재 모델과의 차이

### 공통 모델이 답할 수 있는 것

| 질문 | Claude | Codex |
|---|---|---|
| 프로바이더가 추가 한도 정보를 제공했는가 | `extra_usage` 존재 여부로 판별 | `credits` 존재 여부로 판별 |
| 추가 사용이 활성화됐는가 | `enabled` 또는 `is_enabled`로 판별 | `hasCredits` 또는 `unlimited`로 판별 |
| 추가 한도를 얼마나 소진했는가 | `utilization / 100`으로 계산 | 판별할 수 없음. `UsedRatio=0` |

### 공통 모델에서 손실되는 정보

| 의미 | Claude 원본 | Codex 원본 | 현재 결과 |
|---|---|---|---|
| 월 또는 개인 지출 상한 | `monthly_limit` | `individualLimit.limit` | 없음 |
| 누적 사용량 | `used_credits` | `individualLimit.used` | 없음 |
| 잔여 크레딧 | 응답 필드로 확인하지 못함 | `credits.balance` | 없음 |
| 무제한 상태 | 정책에는 있으나 응답 표현 미확인 | `credits.unlimited` | `Enabled`에 합쳐짐 |
| 비활성화·중단 원인 | `disabled_reason` | `rateLimitReachedType`, `spendControlReached` | 없음 |
| 값의 통화·단위 | `currency` | 별도 통화 필드 없음 | 없음 |
| 다중 계량 버킷 | 해당 응답에서 확인하지 못함 | `rateLimitsByLimitId` | 단일 버킷만 보존 |
| 일회성 한도 재설정 | 해당 응답에서 확인하지 못함 | `rateLimitResetCredits` | 없음 |

이 표는 Claude와 Codex가 같은 정책을 사용한다는 뜻이 아니다. HTML을 비롯한 다른 결과물로 내용을
재구성할 때도 **같은 행에 있다는 이유만으로 두 프로바이더의 필드를 동일한 개념으로 단정하면 안
된다.**

## PROJ-136 고려사항

이 절은 후속 모델 보강이 지켜야 할 제약이다. 구체적인 공개 타입은 PROJ-136에서 결정한다.

1. **누락, `null`, `false`를 구분한다.** 응답에 필드가 없는 상태, 서버가 모른다고 한 상태, 기능이
   꺼진 상태는 서로 다르다.
2. **0과 미확인을 구분한다.** `utilization: null`이나 Codex 사용률 미제공을 숫자 0으로 표현하지 않는다.
3. **`unlimited`와 잔액 보유를 구분한다.** `hasCredits=true`와 `unlimited=true`는 같은 상태가 아니다.
4. **`decimal` 문자열을 조기에 `float64`로 바꾸지 않는다.** Codex가 금액·크레딧 값을 문자열로 주는
   이유와 단위가 확정되지 않았다. Claude 숫자 필드도 단위가 확인되기 전에는 원래 정밀도를 보존한다.
5. **금액에 단위를 붙여 추측하지 않는다.** `currency`가 있는 Claude와 별도 통화 필드가 없는 Codex를
   같은 화폐 모델로 묶으려면 추가 근거가 필요하다.
6. **한도와 잔액을 분리한다.** 지출 상한, 누적 사용량, 잔액, 소진율은 서로 대체할 수 없다.
7. **한도 재설정 크레딧을 추가 사용량 잔액과 분리한다.** 재설정 권한은 한도 창을 한 번 초기화하는
   기능이며 종량제 크레딧 잔액이 아니다.
8. **다중 버킷을 잃지 않는다.** Codex의 `rateLimitsByLimitId`를 단일 `rateLimits`로만 축약하면 어떤
   계량 대상이 막혔는지 알 수 없다.
9. **원인 코드를 보존한다.** `disabled_reason`, `rateLimitReachedType`, `spendControlReached`는 사용자
   안내와 복구 동작을 결정할 근거다.
10. **출처와 버전을 추적한다.** 비공개 Claude 응답과 `experimental` Codex App Server는 바뀔 수 있다.

후속 테스트에는 최소한 정보 없음, 명시적 비활성화, 활성화·잔액 없음, 활성화·잔액 있음,
`unlimited`, 실제 0%, `null` 사용률, 지출 제어 도달, 다중 버킷, 한도 재설정 크레딧 없음·개수만
있음·상세 있음 상태가 필요하다.

## 미확인 사항

### Claude

- `utilization`이 `null`이 아닌 숫자로 오는 응답을 관측하지 못했다. 퍼센트(0~100)인지 비율(0~1)인지는
  현재 코드의 가정이다.
- `monthly_limit`와 `used_credits`의 숫자 단위가 최소 통화 단위인지, 별도 크레딧 단위인지 공개 계약으로
  확인하지 못했다.
- `used_credits`가 정확히 어떤 기간과 과금 범위를 합산하는지 확인하지 못했다.
- `monthly_limit: 0`, 필드 누락, `null`이 각각 `unlimited`·미설정·미지원 중 무엇을 뜻하는지 확인하지
  못했다.
- `disabled_reason`의 가능한 값과 안정적인 기계 판독 계약을 확인하지 못했다.
- 개인, Team, Enterprise 플랜에서 활성·비활성·상한 없음 상태의 실제 응답을 모두 표본화하지 못했다.

### Codex

- `balance`, `individualLimit.limit`, `individualLimit.used` 문자열의 단위와 정밀도를 공개 스키마에서
  확인하지 못했다.
- `credits`와 `individualLimit`가 개인·Business·Edu·Enterprise 플랜마다 언제 제공되는지 확인하지
  못했다.
- `rateLimitsByLimitId`의 버킷 종류와 생성 조건은 서버 정책에 따라 달라질 수 있다.
- `rateLimitResetCredits`의 획득 조건은 공개된 응답 문서만으로 확인하지 못했다.
- 이 조사에서는 실제 계정 응답을 문서 근거로 사용하지 않았다. 값과 식별자 유출을 막기 위해 공개
  문서와 로컬 생성 스키마만 기록했다.

## 근거

### 외부 정책과 스키마

- [C1] [Manage usage credits for paid Claude plans](https://support.claude.com/en/articles/12429409-manage-usage-credits-for-paid-claude-plans)
  — 개인 유료 플랜의 포함 한도 이후 과금, 월 지출 상한, 상한 없음, 선불·자동 충전 정책을 설명한다.
- [C2] [Manage usage credits for Team and seat-based Enterprise plans](https://support.claude.com/en/articles/12005970-manage-usage-credits-for-team-and-seat-based-enterprise-plans)
  — 조직·좌석·사용자 단위 지출 상한과 상위 상한의 관계를 설명한다.
- [C3] [anthropics/claude-code#67429](https://github.com/anthropics/claude-code/issues/67429)
  — `extra_usage` 여섯 필드가 포함된 사용자 제보다. 기능 요청 이슈이며 #50847의 중복으로 닫혔다.
  공식 응답 계약이 아니다.
- [O1] [Codex App Server](https://learn.chatgpt.com/docs/app-server)
  — `account/rateLimits/read`, 단일·다중 버킷, `credits`, 도달 원인, 한도 재설정 크레딧을 설명하는
  공개 문서다.
- [O2] [Codex pricing](https://learn.chatgpt.com/docs/pricing)
  — 포함 한도 이후 추가 크레딧과 크레딧의 정책상 의미를 설명한다.

### 현재 저장소

조사한 기준 커밋은 `ad43b11b7d9e8d8a6032463473973a29edee63b6`이다.

- `internal/vendorlimit/types.go`의 `ExtraAllowance` — 공통 모델과 `Supported`의 의미를 정의한다.
- `internal/vendorlimit/claude.go`의 `claudeUsageResponse.ExtraUsage`, `extra()` — Claude 응답을 파싱하고
  정규화한다.
- `internal/vendorlimit/claude_test.go`의 `claudeUsageBody()`, 정상 응답 테스트 — 현재 모의 응답의 검증
  범위를 보여 준다.
- `internal/codexapp/types.go`의 `CreditsSnapshot`, `RateLimitSnapshot`, `getRateLimitsResponse` — 현재
  App Server 응답 타입을 정의한다.
- `internal/vendorlimit/codex.go`의 `codexExtra()` — Codex `credits`를 공통 모델로 축약한다.
- `internal/vendorlimit/codex_test.go`의 `normalCodexSnapshot()`, 모르는 값 테스트 — 현재 모의 응답의
  검증 범위를 보여 준다.
- `internal/store/vendor_limit.go`, `internal/dashboard/tray/limits.go` — `extra_json`을 저장하고 다시 읽는다.
- `cmd/pulsemetry-gui/frontend/src/pages/tray/adapter.ts`의 `toVendor()` — 추가 사용량이 표시 모델로
  전달되지 않는 현재 UI 경계를 보여 준다.
- [ADR 0011](adr/0011-Codex-사용-한도는-App-Server를-통해-조회한다.md) — Codex 인증·전송을 App
  Server에 맡기고 `vendorlimit`이 공통 모델로 정규화하는 책임 경계를 정한다.
- [PR #30](https://github.com/soma-376/telemetryctl/pull/30) — App Server 전환과 실제 한도 조회를 기록한다.
  이 PR은 활성화된 추가 크레딧·무제한·잔액 상태까지 검증했다는 근거는 아니다.

### 버전 한정 재현

조사 환경의 도구 버전은 Codex CLI `0.153.4`, Claude Code `2.1.263`이었다.

Codex 스키마는 인증된 계정 응답을 읽지 않고 다음 명령으로 생성했다.

```bash
codex app-server generate-json-schema --experimental --out <temporary-directory>
```

Claude는 설치 바이너리에 현재 관측 필드 이름이 포함되는지만 확인했다. 바이너리 문자열은 서버 응답
형식이나 필드 의미를 보장하지 않는다.

```bash
strings "$(command -v claude)" \
  | rg -o 'is_enabled|monthly_limit|used_credits|utilization|currency|disabled_reason' \
  | sort -u
```
