package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadClaudeTokenRoundTrip 은 MergeClaude 가 쓴 토큰을 되읽을 수 있는지 본다.
// 이 왕복이 깨지면 `local disable` 이 회사 설정을 정확히 되돌리지 못한다.
func TestReadClaudeTokenRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := MergeClaude(path, companyManifest(), "company-telemetry-token", false); err != nil {
		t.Fatalf("MergeClaude: %v", err)
	}
	got, err := ReadClaudeToken(path)
	if err != nil {
		t.Fatalf("ReadClaudeToken: %v", err)
	}
	if got != "company-telemetry-token" {
		t.Errorf("token = %q, want company-telemetry-token", got)
	}
}

func TestReadCodexTokenRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
	}{
		{name: "http/protobuf 는 otlp-http", protocol: "http/protobuf"},
		{name: "http/json 도 otlp-http", protocol: "http/json"},
		{name: "grpc 는 otlp-grpc", protocol: "grpc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			m := companyManifest()
			m.OTLP.Protocol = tt.protocol
			if _, err := MergeCodex(path, m, "company-telemetry-token", false); err != nil {
				t.Fatalf("MergeCodex: %v", err)
			}
			got, err := ReadCodexToken(path)
			if err != nil {
				t.Fatalf("ReadCodexToken: %v", err)
			}
			if got != "company-telemetry-token" {
				t.Errorf("token = %q, want company-telemetry-token", got)
			}
		})
	}
}

// TestReadTokenAbsent 는 "없음"이 오류가 아님을 못박는다. 호출자가 할 수 있는 일이
// 없는 상황을 오류로 만들면 재배선 흐름 전체가 사소한 이유로 멈춘다.
func TestReadTokenAbsent(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name   string
		file   string
		body   string
		create bool
		read   func(string) (string, error)
	}{
		{name: "claude 파일 없음", file: "missing.json", read: ReadClaudeToken},
		{name: "claude 빈 파일", file: "empty.json", create: true, read: ReadClaudeToken},
		{name: "claude env 없음", file: "noenv.json", create: true,
			body: `{"model":"opus"}`, read: ReadClaudeToken},
		{name: "claude 헤더 없음", file: "nohdr.json", create: true,
			body: `{"env":{"MY":"1"}}`, read: ReadClaudeToken},
		{name: "claude bearer 아님", file: "nobearer.json", create: true,
			body: `{"env":{"OTEL_EXPORTER_OTLP_HEADERS":"Authorization=Basic abc"}}`, read: ReadClaudeToken},
		{name: "codex 파일 없음", file: "missing.toml", read: ReadCodexToken},
		{name: "codex otel 없음", file: "noOtel.toml", create: true,
			body: "model = \"gpt\"\n", read: ReadCodexToken},
		{name: "codex 헤더 없음", file: "nohdr.toml", create: true,
			body: "[otel.exporter.otlp-http]\nendpoint = \"https://x\"\n", read: ReadCodexToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.file)
			if tt.create {
				if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			got, err := tt.read(path)
			if err != nil {
				t.Fatalf("읽기 오류: %v", err)
			}
			if got != "" {
				t.Errorf("token = %q, want 빈 문자열", got)
			}
		})
	}
}

// TestBearerFromHeaderList 는 헤더 목록 파싱의 경계를 본다.
func TestBearerFromHeaderList(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "단일 항목", in: "Authorization=Bearer abc", want: "abc"},
		{name: "여러 항목", in: "X-Team=core,Authorization=Bearer abc", want: "abc"},
		{name: "공백 허용", in: " Authorization = Bearer  abc ", want: "abc"},
		{name: "스킴 대소문자 무시", in: "authorization=bearer abc", want: "abc"},
		{name: "다른 스킴은 무시", in: "Authorization=Basic abc", want: ""},
		{name: "빈 값", in: "", want: ""},
		{name: "등호 없음", in: "Authorization", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bearerFromHeaderList(tt.in); got != tt.want {
				t.Errorf("bearerFromHeaderList(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
