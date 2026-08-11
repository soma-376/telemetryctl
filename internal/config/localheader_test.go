package config

import (
	"testing"

	"github.com/your-org/pulsemetry/internal/receiver"
)

// TestLocalIngestHeaderMatchesReceiver 는 복제한 상수가 수신기의 것과 같은지 고정한다.
//
// localheader.go 가 receiver 를 import 하지 않는 대가로 생긴 중복이라, 어긋나면
// 재배선된 설정이 다시 전량 401 을 맞는다 — 이 파일이 존재하는 이유 그 자체다.
// installer/local_test.go 의 TestLocalEndpointMatchesReceiverFormat 과 같은 역할이고,
// 여기서만 receiver 를 import 한다 (테스트 전용 의존이라 배포 바이너리에 영향이 없다).
func TestLocalIngestHeaderMatchesReceiver(t *testing.T) {
	if LocalIngestHeader != receiver.LocalHeader {
		t.Errorf("LocalIngestHeader = %q, receiver.LocalHeader = %q — 벤더 설정이 401 을 맞는다",
			LocalIngestHeader, receiver.LocalHeader)
	}
	if LocalIngestHeaderValue != receiver.LocalHeaderValue {
		t.Errorf("LocalIngestHeaderValue = %q, receiver.LocalHeaderValue = %q",
			LocalIngestHeaderValue, receiver.LocalHeaderValue)
	}
}

// TestIsLocalEndpoint 는 "http + localhost ⟺ 로컬 수신기" 규칙의 경계를 본다.
//
// 이 규칙은 contract.validOTLPEndpoint 가 http 를 localhost 에만 허용하는 데서 나온다.
// 회사 endpoint 에 헤더가 붙으면 무의미한 헤더가 하나 나가는 데 그치지만, 로컬
// endpoint 에 안 붙으면 텔레메트리가 전량 사라진다.
func TestIsLocalEndpoint(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "로컬 기본 포트", in: "http://localhost:4318", want: true},
		{name: "로컬 폴백 포트", in: "http://localhost:53412", want: true},
		{name: "포트 없는 로컬", in: "http://localhost", want: true},
		{name: "경로가 붙어도 로컬", in: "http://localhost:4318/v1", want: true},
		{name: "회사 https", in: "https://collector.example.com", want: false},
		// https://localhost 는 회사가 로컬 TLS Collector 를 운영하는 경우다.
		// 우리 수신기는 평문 http 로만 뜨므로 로컬로 보지 않는다.
		{name: "https 는 localhost 라도 아님", in: "https://localhost:4318", want: false},
		// 127.0.0.1 은 계약이 애초에 거부하는 표기다 (계획서 제약 §1).
		{name: "127.0.0.1 은 아님", in: "http://127.0.0.1:4318", want: false},
		{name: "다른 호스트의 http", in: "http://collector.internal:4318", want: false},
		{name: "빈 문자열", in: "", want: false},
		{name: "파싱 불가", in: "://nope", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLocalEndpoint(tt.in); got != tt.want {
				t.Errorf("isLocalEndpoint(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
