package main

import "testing"

// --listen 은 두 가지를 동시에 뜻한다 — 포트 지정과 "폴백하지 마라". 파싱이 틀리면
// 사용자가 지정한 포트를 조용히 무시하고 4318 로 뜨는, 가장 알아채기 어려운 실패가 난다.
func TestParseListen(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantPort  int
		wantFixed bool
		wantErr   bool
	}{
		{name: "미지정이면 폴백 허용", in: "", wantPort: 0, wantFixed: false},
		{name: "포트만", in: "4318", wantPort: 4318, wantFixed: true},
		{name: "localhost:포트", in: "localhost:4318", wantPort: 4318, wantFixed: true},
		{name: "127.0.0.1:포트", in: "127.0.0.1:4318", wantPort: 4318, wantFixed: true},
		{name: "IPv6 loopback", in: "[::1]:4318", wantPort: 4318, wantFixed: true},
		{name: "공백은 다듬는다", in: "  4318 ", wantPort: 4318, wantFixed: true},

		// 수신기는 loopback 만 바인딩한다. 다른 호스트를 받아 조용히 무시하면
		// 사용자는 왜 연결이 안 되는지 알 수 없다.
		{name: "외부 주소는 거부", in: "0.0.0.0:4318", wantErr: true},
		{name: "다른 호스트는 거부", in: "example.com:4318", wantErr: true},
		{name: "포트가 숫자가 아니면 거부", in: "localhost:http", wantErr: true},
		{name: "범위 밖 포트는 거부", in: "70000", wantErr: true},
		{name: "0 은 거부", in: "0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, fixed, err := parseListen(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseListen(%q) = (%d, %t, nil), want 오류", tt.in, port, fixed)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseListen(%q): %v", tt.in, err)
			}
			if port != tt.wantPort || fixed != tt.wantFixed {
				t.Errorf("parseListen(%q) = (%d, %t), want (%d, %t)",
					tt.in, port, fixed, tt.wantPort, tt.wantFixed)
			}
		})
	}
}
