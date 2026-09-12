// Package codexapp 은 Codex App Server 프로세스와 프로토콜을 캡슐화한다.
package codexapp

import "context"

// RateLimitsReader 는 Codex 계정 사용 한도를 읽는 최소 계약이다.
type RateLimitsReader interface {
	RateLimits(context.Context) (RateLimitSnapshot, error)
}

// ThreadReader 는 Codex 스레드의 표시 제목을 읽는 최소 계약이다.
type ThreadReader interface {
	ThreadName(context.Context, string) (string, error)
}

// RateLimitWindow 는 App Server가 정규화한 한도 창이다.
type RateLimitWindow struct {
	UsedPercent        int    `json:"usedPercent"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
	ResetsAt           *int64 `json:"resetsAt"`
}

// CreditsSnapshot 은 계정의 추가 크레딧 상태다.
type CreditsSnapshot struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance"`
}

// RateLimitSnapshot 은 account/rateLimits/read의 단일 버킷 뷰다.
type RateLimitSnapshot struct {
	PlanType  *string          `json:"planType"`
	Primary   *RateLimitWindow `json:"primary"`
	Secondary *RateLimitWindow `json:"secondary"`
	Credits   *CreditsSnapshot `json:"credits"`
}

type getRateLimitsResponse struct {
	RateLimits RateLimitSnapshot `json:"rateLimits"`
}

type threadReadParams struct {
	ThreadID     string `json:"threadId"`
	IncludeTurns bool   `json:"includeTurns"`
}

type threadReadResponse struct {
	Thread thread `json:"thread"`
}

type thread struct {
	Name *string `json:"name"`
}
