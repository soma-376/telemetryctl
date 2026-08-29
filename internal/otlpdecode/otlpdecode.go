// Package otlpdecode 는 OTLP/HTTP 페이로드를 다루는 두 가지 일을 한다.
//
//  1. 디코드 — 수신한 ResourceMetrics·ResourceLogs 를 event.Event 로 정규화한다.
//  2. 제거 재인코딩 — 상위 Collector 로 전달할 페이로드에서 manifest Privacy 가 금지한
//     원문·tool details 를 지우고 받은 인코딩 그대로 다시 만든다 (ADR 0003).
//
// 이 패키지는 파이프라인에서 protobuf 의존성을 격리하는 층이다. receiver·session·store·
// forward 중 어느 것도 proto 타입을 보지 않는다 — 전부 event.Event 와 Content 만 주고받는다.
// 그래서 여기서 새 의존성을 늘리면 격리가 깨진다.
//
// 프라이버시 규칙이 두 벌이라는 점을 계속 의식해야 한다 (ADR 0003 Negative).
//   - 로컬 저장 규칙: Attributes 는 allowlist 다. 여기 필드가 없는 속성은 버린다.
//     v3 스키마가 요구하는 값 — 작업 경로 원문 · user.email · user.id/account_uuid — 은
//     **로컬 저장 전용 필드로** allowlist 에 있다 (ADR 0010). 경로는 해시+basename 과
//     원경로를 나란히 산출하고, 상위 전달과 관련된 코드는 해시 쪽만 쓴다.
//     organization.id 는 여전히 자리가 없다 — 그것을 요구하는 v3 컬럼이 없다.
//   - 상위 전달 규칙: Scrub 은 denylist 다. 회사 Privacy 가 금지한 것만 지우고 나머지는 보존한다.
//     상위 Collector 는 우리가 모르는 필드도 받을 수 있어야 하므로 여기서 allowlist 를 쓰면 안 된다.
//
// **allowlist 를 넓히는 것은 상위 전달을 넓히지 않는다.** 두 경로는 코드상 분리돼 있고
// (`daemon/pipeline.go` 의 Consume 이 원본 바이트를 디코드 전에 포워더로 넘긴다),
// privacy_test.go 가 같은 픽스처로 "로컬에는 있고 상위에는 없음" 을 양방향 단언한다.
package otlpdecode

import (
	"fmt"
	"mime"
	"strings"
)

// Encoding 은 OTLP/HTTP 페이로드의 직렬화 형식이다.
//
// Claude Code·Codex 중 어느 쪽이 무엇을 쓸지 고정할 수 없어 양쪽을 다 받는다.
// 전달 시 입력 인코딩을 보존하는 것이 중요하다 — 상위 Collector 가 Content-Type 과
// 실제 본문이 어긋난 요청을 받으면 진단 불가능한 조용한 실패가 난다.
type Encoding uint8

const (
	// EncodingProtobuf 는 application/x-protobuf 다.
	EncodingProtobuf Encoding = iota
	// EncodingJSON 은 application/json (OTLP/JSON) 이다.
	EncodingJSON
)

func (e Encoding) String() string {
	if e == EncodingJSON {
		return "json"
	}
	return "protobuf"
}

// ContentType 은 이 인코딩으로 응답·전달할 때 쓸 Content-Type 이다.
func (e Encoding) ContentType() string {
	if e == EncodingJSON {
		return "application/json"
	}
	return "application/x-protobuf"
}

// EncodingFromContentType 은 요청 Content-Type 을 Encoding 으로 옮긴다.
// 파라미터(charset 등)는 무시하고 미디어 타입만 본다. 알 수 없으면 ok=false —
// 수신기는 이 경우 415 로 답해야 한다.
func EncodingFromContentType(contentType string) (Encoding, bool) {
	mediaType := contentType
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
		mediaType = parsed
	}
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "application/x-protobuf", "application/protobuf":
		return EncodingProtobuf, true
	case "application/json":
		return EncodingJSON, true
	}
	return EncodingProtobuf, false
}

// PayloadKind 는 OTLP/HTTP 의 시그널별 엔드포인트다.
type PayloadKind uint8

const (
	PayloadMetrics PayloadKind = iota
	PayloadLogs
	PayloadTraces
)

func (k PayloadKind) String() string {
	switch k {
	case PayloadLogs:
		return "logs"
	case PayloadTraces:
		return "traces"
	default:
		return "metrics"
	}
}

// Path 는 이 시그널의 OTLP/HTTP 경로다.
func (k PayloadKind) Path() string { return "/v1/" + k.String() }

// PayloadKindFromPath 는 요청 경로를 PayloadKind 로 옮긴다. 그 외 경로는 ok=false 다.
func PayloadKindFromPath(path string) (PayloadKind, bool) {
	switch strings.TrimSuffix(path, "/") {
	case "/v1/metrics":
		return PayloadMetrics, true
	case "/v1/logs":
		return PayloadLogs, true
	case "/v1/traces":
		return PayloadTraces, true
	}
	return PayloadMetrics, false
}

func unsupportedKind(k PayloadKind) error {
	return fmt.Errorf("otlpdecode: 알 수 없는 payload kind %d", uint8(k))
}
