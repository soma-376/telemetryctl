package otlpdecode

import (
	"encoding/json"
	"testing"
)

func TestEncodingFromContentType(t *testing.T) {
	tests := []struct {
		contentType string
		want        Encoding
		wantOK      bool
	}{
		{"application/x-protobuf", EncodingProtobuf, true},
		{"application/protobuf", EncodingProtobuf, true},
		{"application/json", EncodingJSON, true},
		{"application/json; charset=utf-8", EncodingJSON, true},
		{"APPLICATION/JSON", EncodingJSON, true},
		{"  application/json  ", EncodingJSON, true},
		{"text/plain", EncodingProtobuf, false},
		{"", EncodingProtobuf, false},
		{"application/grpc", EncodingProtobuf, false},
	}
	for _, tc := range tests {
		t.Run(tc.contentType, func(t *testing.T) {
			got, ok := EncodingFromContentType(tc.contentType)
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Errorf("EncodingFromContentType(%q) = (%v, %v), want (%v, %v)",
					tc.contentType, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestEncodingContentTypeMirrors(t *testing.T) {
	for _, enc := range []Encoding{EncodingProtobuf, EncodingJSON} {
		got, ok := EncodingFromContentType(enc.ContentType())
		if !ok || got != enc {
			t.Errorf("%v.ContentType() 을 되읽으면 %v(%v) — 미러링이 깨진다", enc, got, ok)
		}
	}
}

func TestPayloadKindFromPath(t *testing.T) {
	tests := []struct {
		path   string
		want   PayloadKind
		wantOK bool
	}{
		{"/v1/metrics", PayloadMetrics, true},
		{"/v1/logs", PayloadLogs, true},
		{"/v1/traces", PayloadTraces, true},
		{"/v1/logs/", PayloadLogs, true},
		{"/healthz", PayloadMetrics, false},
		{"/v1/profiles", PayloadMetrics, false},
		{"", PayloadMetrics, false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got, ok := PayloadKindFromPath(tc.path)
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Errorf("PayloadKindFromPath(%q) = (%v, %v), want (%v, %v)", tc.path, got, ok, tc.want, tc.wantOK)
			}
			if ok && got.Path() != "/v1/"+got.String() {
				t.Errorf("Path() 왕복이 깨졌다: %q", got.Path())
			}
		})
	}
}

// TestJSONIDRoundTrip 은 OTLP/JSON 의 hex ID 와 protojson 의 base64 사이 변환이
// 정확히 되돌아오는지 본다. 여기가 틀리면 상위로 유효하지 않은 trace_id 가 흘러간다.
func TestJSONIDRoundTrip(t *testing.T) {
	const traceHex = "4bf92f3577b34da6a3ce929d0e0e4736"
	const spanHex = "00f067aa0ba902b7"

	original := []byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[` +
		`{"traceId":"` + traceHex + `","spanId":"` + spanHex + `","body":{"stringValue":"traceId"}}` +
		`]}]}]}`)

	toProto, err := hexIDsToBase64(original)
	if err != nil {
		t.Fatalf("hexIDsToBase64: %v", err)
	}
	if string(toProto) == string(original) {
		t.Fatal("hex ID 가 그대로다 — protojson 이 base64 로 잘못 해독한다")
	}

	back, err := base64IDsToHex(toProto)
	if err != nil {
		t.Fatalf("base64IDsToHex: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(back, &got); err != nil {
		t.Fatalf("결과 파싱: %v", err)
	}
	rec := got["resourceLogs"].([]any)[0].(map[string]any)["scopeLogs"].([]any)[0].(map[string]any)["logRecords"].([]any)[0].(map[string]any)
	if rec["traceId"] != traceHex {
		t.Errorf("traceId = %v, want %s", rec["traceId"], traceHex)
	}
	if rec["spanId"] != spanHex {
		t.Errorf("spanId = %v, want %s", rec["spanId"], spanHex)
	}
	// 키 이름이 traceId 인 값만 건드려야 한다 — 같은 문자열이 본문에 있어도 그대로다.
	body := rec["body"].(map[string]any)
	if body["stringValue"] != "traceId" {
		t.Errorf("본문이 변했다: %v", body["stringValue"])
	}
}

func TestRewriteIDsSkipsPayloadsWithoutIDs(t *testing.T) {
	original := []byte(`{"resourceMetrics":[{"scopeMetrics":[]}]}`)
	got, err := hexIDsToBase64(original)
	if err != nil {
		t.Fatalf("hexIDsToBase64: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("ID 가 없는 페이로드가 재작성됐다: %s", got)
	}
}
