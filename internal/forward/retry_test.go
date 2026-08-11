package forward

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	netErr := errors.New("connection refused")
	tests := []struct {
		name   string
		status int
		err    error
		want   disposition
	}{
		{"200 성공", http.StatusOK, nil, dispositionDone},
		{"202 성공", http.StatusAccepted, nil, dispositionDone},
		{"전송 오류는 재시도", 0, netErr, dispositionRetry},
		{"500 재시도", http.StatusInternalServerError, nil, dispositionRetry},
		{"502 재시도", http.StatusBadGateway, nil, dispositionRetry},
		{"503 재시도", http.StatusServiceUnavailable, nil, dispositionRetry},
		{"429 재시도", http.StatusTooManyRequests, nil, dispositionRetry},
		{"408 재시도", http.StatusRequestTimeout, nil, dispositionRetry},
		{"425 재시도", http.StatusTooEarly, nil, dispositionRetry},
		{"401 은 토큰 갱신", http.StatusUnauthorized, nil, dispositionAuth},
		{"403 은 토큰 갱신", http.StatusForbidden, nil, dispositionAuth},
		{"400 즉시 폐기", http.StatusBadRequest, nil, dispositionDiscard},
		{"404 즉시 폐기", http.StatusNotFound, nil, dispositionDiscard},
		{"413 즉시 폐기", http.StatusRequestEntityTooLarge, nil, dispositionDiscard},
		{"415 즉시 폐기", http.StatusUnsupportedMediaType, nil, dispositionDiscard},
		{"422 즉시 폐기", http.StatusUnprocessableEntity, nil, dispositionDiscard},
		{"3xx 는 알 수 없으므로 폐기", http.StatusFound, nil, dispositionDiscard},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classify(tc.status, tc.err); got != tc.want {
				t.Fatalf("classify(%d, %v) = %d, want %d", tc.status, tc.err, got, tc.want)
			}
		})
	}
}

// 백오프는 지수적으로 늘되 상한을 넘지 않고, 매번 같은 값이 나오지 않아야 한다(지터).
func TestBackoffBoundsAndJitter(t *testing.T) {
	t.Parallel()
	const (
		base = 100 * time.Millisecond
		max  = 2 * time.Second
	)
	for attempt := 0; attempt <= 8; attempt++ {
		nominal := base
		for i := 1; i < attempt && nominal < max; i++ {
			nominal *= 2
		}
		if nominal > max {
			nominal = max
		}
		for i := 0; i < 50; i++ {
			got := backoff(attempt, base, max)
			if got < nominal/2 || got >= nominal {
				t.Fatalf("backoff(%d) = %s, want [%s, %s)", attempt, got, nominal/2, nominal)
			}
			if got > max {
				t.Fatalf("backoff(%d) = %s, 상한 %s 초과", attempt, got, max)
			}
		}
	}
}

func TestBackoffIsJittered(t *testing.T) {
	t.Parallel()
	seen := map[time.Duration]bool{}
	for i := 0; i < 100; i++ {
		seen[backoff(3, 100*time.Millisecond, time.Second)] = true
	}
	if len(seen) < 5 {
		t.Fatalf("서로 다른 값이 %d 개뿐 — 지터가 없다", len(seen))
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	const max = 30 * time.Second
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"빈 헤더", "", 0},
		{"초 단위", "5", 5 * time.Second},
		{"0 초", "0", 0},
		{"음수는 무시", "-10", 0},
		{"상한으로 자른다", "3600", max},
		{"HTTP 날짜", now.Add(7 * time.Second).UTC().Format(http.TimeFormat), 7 * time.Second},
		{"과거 날짜는 0", now.Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
		{"먼 미래 날짜도 자른다", now.Add(time.Hour).UTC().Format(http.TimeFormat), max},
		{"해석 불가", "곧", 0},
		{"공백만", "   ", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseRetryAfter(tc.header, now, max); got != tc.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", tc.header, got, tc.want)
			}
		})
	}
}
