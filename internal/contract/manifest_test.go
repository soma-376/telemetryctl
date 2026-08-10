package contract

import (
	"strings"
	"testing"
)

// TestValidOTLPEndpoint 는 http 허용 범위를 못박는다.
//
// 이 테스트의 존재 이유는 PROJ-36 이다. 로컬 재배선은 벤더 설정에 loopback 주소를 쓰는데,
// 여기가 http:// 를 **리터럴 호스트 localhost** 에만 허용한다. 127.0.0.1 은 같은 곳을
// 가리키지만 거부된다 — 이 규칙은 서버 저장소와 공유하는 계약(contracts/
// enrollment-manifest.schema.json)이라 클라이언트가 임의로 넓힐 수 없다.
//
// "127.0.0.1 로 써도 되는 것 아닌가" 는 이 코드를 처음 보는 사람이 반드시 하는 생각이고,
// 그 한 줄 수정이 manifest 검증을 조용히 통과시킨 뒤 서버 쪽 스키마 검증에서만 깨지게
// 만든다. 그래서 거부를 테스트로 고정한다 (계획서 「테스트 전략」).
func TestValidOTLPEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     bool
	}{
		{name: "https 는 어느 호스트든 허용", endpoint: "https://collector.example.com", want: true},
		{name: "https 포트 포함", endpoint: "https://collector.example.com:4318", want: true},
		{name: "http + localhost 는 허용", endpoint: "http://localhost:4318", want: true},
		{name: "http + localhost 포트 없음", endpoint: "http://localhost", want: true},

		// 여기부터가 회귀 방어선이다.
		{name: "http + 127.0.0.1 은 거부", endpoint: "http://127.0.0.1:4318", want: false},
		{name: "http + 127.0.0.1 포트 없음도 거부", endpoint: "http://127.0.0.1", want: false},
		{name: "http + IPv6 loopback 도 거부", endpoint: "http://[::1]:4318", want: false},
		{name: "http + localhost 하위 도메인은 거부", endpoint: "http://localhost.evil.com:4318", want: false},
		{name: "http + 외부 호스트는 거부", endpoint: "http://collector.example.com", want: false},

		{name: "스킴 없음", endpoint: "collector.example.com", want: false},
		{name: "호스트 없음", endpoint: "https://", want: false},
		{name: "빈 문자열", endpoint: "", want: false},
		{name: "지원하지 않는 스킴", endpoint: "grpc://collector.example.com", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validOTLPEndpoint(tt.endpoint); got != tt.want {
				t.Errorf("validOTLPEndpoint(%q) = %v, want %v", tt.endpoint, got, tt.want)
			}
		})
	}
}

// TestManifestValidateRejectsLoopbackIP 는 위 규칙이 Manifest.Validate 까지 올라오는지,
// 그리고 오류 문구가 원인을 설명하는지 확인한다.
func TestManifestValidateRejectsLoopbackIP(t *testing.T) {
	m := Manifest{
		SchemaVersion:  1,
		ConfigRevision: 1,
		OTLP:           OTLP{Endpoint: "http://127.0.0.1:4318", Protocol: "http/protobuf"},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("http://127.0.0.1:4318 이 통과했다 — 서버 스키마와 어긋난다")
	}
	if !strings.Contains(err.Error(), "localhost") {
		t.Errorf("오류 = %v, want localhost 제약을 설명하는 문구", err)
	}

	m.OTLP.Endpoint = "http://localhost:4318"
	if err := m.Validate(); err != nil {
		t.Errorf("http://localhost:4318 이 거부됐다: %v", err)
	}
}
