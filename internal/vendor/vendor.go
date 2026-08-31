// Package vendor 는 벤더 정체성의 단일 출처다.
//
// 정식 ID·별칭·표시 이름을 여기서만 안다. 전에는 이 지식이 otlpdecode·vendorlimit·store·
// daemon·프런트에 흩어져 있었고, 한 곳에 벤더를 추가해도 나머지가 모르는 일이 실제로
// 일어났다 (codex_exec 세션이 화면에서 "Others" 로 떨어졌다).
package vendor

import "strings"

// ID 는 events.vendor·sessions.vendor_id 에 저장되는 정식 표기다.
// 값 문자열이 저장 포맷이자 프런트엔드와의 계약이라 바꾸면 기존 DB 와 어긋난다.
type ID string

const (
	ClaudeCode ID = "claude_code"
	Codex      ID = "codex"
	GeminiCLI  ID = "gemini_cli"
	Cursor     ID = "cursor"
)

// All 은 우리가 아는 벤더 전부다. 한도 조회를 지원하는 벤더와는 다른 목록이다
// (vendorlimit.SupportedVendors).
func All() []ID { return []ID{ClaudeCode, Codex, GeminiCLI, Cursor} }

// aliases 는 관측된 service.name 을 정식 ID 로 옮긴다.
//
// 키는 전부 소문자다. 벤더가 자기 이름을 부르는 방식은 한 도구 안에서도 여러 가지다 —
// Codex 는 실행 경로에 따라 codex·codex_cli_rs·codex_exec 를, Claude Code 는 CLI 와
// 데스크톱 앱이 서로 다른 이름을 쓴다. 같은 대화가 두 벤더로 갈리면 화면에 두 줄로 나온다.
var aliases = map[string]ID{
	"claude":              ClaudeCode,
	"claude-code":         ClaudeCode,
	"claude_code":         ClaudeCode,
	"claude-code-desktop": ClaudeCode,
	"claude_code_desktop": ClaudeCode,
	"codex":               Codex,
	"codex-cli":           Codex,
	"codex_cli":           Codex,
	"codex-cli-rs":        Codex,
	"codex_cli_rs":        Codex,
	"codex-exec":          Codex,
	"codex_exec":          Codex,
	"gemini":              GeminiCLI,
	"gemini-cli":          GeminiCLI,
	"gemini_cli":          GeminiCLI,
	"cursor":              Cursor,
}

// Normalize 는 관측된 이름을 정식 ID 로 옮긴다. ok=false 면 우리가 모르는 벤더다.
//
// 모르는 벤더를 버리지 않는 것은 호출자의 몫이다. 스키마가 벤더를 제약하지 않으므로
// 새 도구의 데이터도 정규화만 거쳐 그대로 저장된다 (Fallback).
func Normalize(raw string) (ID, bool) {
	id, ok := aliases[strings.ToLower(strings.TrimSpace(raw))]
	return id, ok
}

// Fallback 은 모르는 이름을 저장 가능한 모양으로만 다듬는다. 대문자·하이픈이 섞이면
// 같은 도구가 두 벤더로 갈라진다.
func Fallback(raw string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "-", "_")
}
