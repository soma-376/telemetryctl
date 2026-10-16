package forward

import (
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// disposition 은 한 번의 전송 결과를 어떻게 다룰지다.
type disposition int

const (
	// dispositionDone — 상위가 받았다.
	dispositionDone disposition = iota
	// dispositionRetry — 지금은 안 되지만 나중엔 될 수 있다. 예산 안에서 다시 시도한다.
	dispositionRetry
	// dispositionDiscard — 같은 본문을 다시 보내도 같은 답이 온다. 즉시 버린다.
	dispositionDiscard
	// dispositionAuth — 토큰 문제. 갱신 후 한 번만 다시 시도한다.
	dispositionAuth
)

// classify 는 상태 코드와 전송 오류를 처분으로 옮긴다.
//
// 원칙은 하나다: **4xx 는 즉시 폐기한다.** 본문이 잘못됐거나 권한이 없는 요청을 반복하면
// 큐만 막고 상위에 불필요한 부하만 준다. 시간이 해결해 줄 수 있는 것 — 5xx, 429(레이트 리밋),
// 408·425(타이밍), 그리고 네트워크 오류 — 만 재시도한다.
func classify(status int, err error) disposition {
	if err != nil {
		// 연결 거부·타임아웃·DNS. Collector 가 재기동 중일 수 있다.
		return dispositionRetry
	}
	switch {
	case status >= 200 && status < 300:
		return dispositionDone
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return dispositionAuth
	case status == http.StatusRequestTimeout,
		status == http.StatusTooEarly,
		status == http.StatusTooManyRequests:
		return dispositionRetry
	case status >= 400 && status < 500:
		return dispositionDiscard
	case status >= 500:
		return dispositionRetry
	default:
		// 1xx·3xx 가 여기까지 왔다면 우리가 모르는 상황이다. 재시도해도 나아질 근거가 없다.
		return dispositionDiscard
	}
}

// backoff 는 attempt 번째 시도 뒤의 대기 시간이다. 지수 증가에 **지터**를 섞는다.
//
// 지터가 없으면 Collector 가 복구되는 순간 모든 개발자 머신이 같은 간격으로 동시에 재시도해
// 방금 살아난 상위를 다시 밀어 버린다. equal jitter — [d/2, d) 구간에서 고른다 — 를 쓴다.
// 하한이 있어 지연이 무의미하게 0 에 붙지 않고, 상한이 있어 예측 가능하다.
func backoff(attempt int, base, max time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := base
	for i := 1; i < attempt && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	half := d / 2
	if half <= 0 {
		return d
	}
	// rand/v2 의 최상위 함수는 동시 호출에 안전하다.
	return half + time.Duration(rand.Int64N(int64(half)))
}

// parseRetryAfter 는 429·503 응답의 Retry-After 를 대기 시간으로 옮긴다.
//
// 상위가 "천천히 오라"고 말했으면 그대로 따르는 것이 예의지만, 무제한으로 따르지는 않는다.
// Retry-After: 3600 하나에 워커가 한 시간 잠기면 그 뒤의 텔레메트리가 전부 큐에서 버려진다.
// max 로 자르고, 넘치면 어차피 버려질 뿐이다 — 텔레메트리 손실은 허용된다 (§5.4).
func parseRetryAfter(header string, now time.Time, max time.Duration) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		return clampDelay(time.Duration(secs)*time.Second, max)
	}
	if when, err := http.ParseTime(header); err == nil {
		return clampDelay(when.Sub(now), max)
	}
	return 0
}

func clampDelay(d, max time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if d > max {
		return max
	}
	return d
}
