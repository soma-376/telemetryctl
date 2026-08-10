package otlpdecode

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// OTLP/JSON 스펙과 protojson 이 bytes 필드를 다르게 표기한다. 스펙은 trace_id·span_id 를
// **소문자 hex** 로 쓰라고 하고, protojson 은 proto3 JSON 규칙대로 **base64** 로 쓴다.
//
// 이것을 그냥 두면 조용히 망가진다. 32자 hex 문자열은 base64 로도 해독 가능한 문자 집합이고
// 길이도 4의 배수라, protojson 이 에러 없이 24바이트짜리 엉뚱한 trace_id 를 만들어낸다.
// 그 상태로 재인코딩해 상위 Collector 로 보내면 유효하지 않은 ID 가 그대로 흘러간다 —
// ADR 0003 이 경고한 "재인코딩 버그가 곧 데이터 손상"의 정확한 사례다.
//
// 그래서 JSON 경로에서만 ID 필드를 앞뒤로 변환한다. 길이로 형식을 구분할 수 있어 모호하지 않다:
// trace_id 는 hex 32자 / base64 24자, span_id 는 hex 16자 / base64 12자다.
//
// 알려진 한계: 이 왕복은 encoding/json 을 한 번 더 지난다. 그 과정에서 OTLP/JSON 스펙에 없는
// 알 수 없는 **proto 필드**는 protojson 의 DiscardUnknown 에 의해 사라진다(protobuf 경로는
// unknown fields 를 그대로 보존한다). 속성 수준의 보존 — 상위가 우리가 모르는 속성을 받는 것 —
// 은 두 인코딩 모두에서 지켜진다.

var idJSONKeys = map[string]struct{}{
	"traceId":        {},
	"trace_id":       {},
	"spanId":         {},
	"span_id":        {},
	"parentSpanId":   {},
	"parent_span_id": {},
}

// hexIDsToBase64 는 protojson 이 읽을 수 있도록 hex ID 를 base64 로 바꾼다.
// 이미 base64 인 값은 그대로 둔다.
func hexIDsToBase64(data []byte) ([]byte, error) {
	return rewriteIDs(data, hexToBase64)
}

// base64IDsToHex 는 protojson 이 만든 base64 ID 를 OTLP/JSON 스펙 표기로 되돌린다.
func base64IDsToHex(data []byte) ([]byte, error) {
	return rewriteIDs(data, base64ToHex)
}

// containsIDKey 는 ID 필드가 아예 없는 페이로드에서 JSON 을 한 번 더 파싱하지 않게 한다.
// 메트릭 배치에는 보통 ID 가 없고 페이로드는 최대 4 MiB 라 이 검사가 값을 한다.
func containsIDKey(data []byte) bool {
	for key := range idJSONKeys {
		if bytes.Contains(data, []byte(`"`+key+`"`)) {
			return true
		}
	}
	return false
}

func rewriteIDs(data []byte, convert func(string) (string, bool)) ([]byte, error) {
	if !containsIDKey(data) {
		return data, nil
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("otlpdecode: JSON 파싱: %w", err)
	}
	walkIDs(root, convert)
	out, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("otlpdecode: JSON 직렬화: %w", err)
	}
	return out, nil
}

func walkIDs(node any, convert func(string) (string, bool)) {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if s, ok := child.(string); ok {
				if _, isID := idJSONKeys[key]; isID {
					if converted, ok := convert(s); ok {
						v[key] = converted
					}
					continue
				}
			}
			walkIDs(child, convert)
		}
	case []any:
		for _, child := range v {
			walkIDs(child, convert)
		}
	}
}

func hexToBase64(s string) (string, bool) {
	if len(s) != 32 && len(s) != 16 {
		return "", false
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(raw), true
}

func base64ToHex(s string) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || (len(raw) != 16 && len(raw) != 8) {
		return "", false
	}
	return hex.EncodeToString(raw), true
}
