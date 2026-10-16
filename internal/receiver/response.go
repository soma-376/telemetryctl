package receiver

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

// OTLP/HTTP 응답 본문을 손으로 직렬화한다.
//
// # 왜 proto 라이브러리를 쓰지 않는가
//
// otlpdecode 의 패키지 주석이 "receiver·session·rollup·store·forward 중 어느 것도 proto
// 타입을 보지 않는다" 를 계약으로 못박았다. 그 격리를 지키려고 여기서 손으로 만든다.
// 만들 메시지가 아주 작아서 가능한 선택이다 — Export*ServiceResponse 는 필드가 하나고,
// 그 안의 Export*PartialSuccess 도 int64 하나와 string 하나뿐이다.
//
//	message ExportLogsServiceResponse { ExportLogsPartialSuccess partial_success = 1; }
//	message ExportLogsPartialSuccess  { int64 rejected_log_records = 1; string error_message = 2; }
//
// 세 시그널의 모양이 완전히 같고 rejected 필드 번호도 전부 1 이다. 시그널마다 다른 것은
// JSON 필드 **이름** 뿐이라 kind 는 JSON 경로에서만 쓰인다.
//
// 손으로 만든 바이트가 진짜 OTLP 메시지인지는 response_test.go 가 실제 proto 정의로
// 역직렬화해 확인한다. 프로덕션 코드에는 proto import 가 없다.
//
// # 성공 응답의 모양
//
// 성공은 partial_success 가 없는 빈 메시지다. protobuf 로는 **0 바이트**, JSON 으로는 `{}` 다.
// 실제 OTLP Collector 가 돌려주는 것과 같다.

// contentTypeHeader 는 응답에 붙일 Content-Type 을 요청 인코딩에서 그대로 미러링한다.
//
// 미러링하지 않으면 진단 불가능한 조용한 실패가 난다. protobuf 로 보냈는데 JSON 으로 답하면
// exporter 는 파싱에 실패하고, 대부분의 SDK 는 그것을 "전송 실패" 로 처리해 재시도한다 —
// 우리는 이미 데이터를 받아 큐에 넣었는데 클라이언트는 실패로 알고 다시 보낸다.
func contentTypeHeader(w http.ResponseWriter, enc otlpdecode.Encoding) {
	w.Header().Set("Content-Type", enc.ContentType())
	// 이 응답은 캐시될 이유가 없다. 중간 프록시는 없지만 명시해 둔다.
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// writeSuccess 는 200 + 빈 성공 응답이다.
func writeSuccess(w http.ResponseWriter, enc otlpdecode.Encoding) {
	contentTypeHeader(w, enc)
	w.WriteHeader(http.StatusOK)
	if enc == otlpdecode.EncodingJSON {
		_, _ = w.Write([]byte("{}"))
	}
	// protobuf 성공은 빈 메시지 = 0 바이트다.
}

// writePartialSuccess 는 200 + PartialSuccess 다. 큐 포화 시 이 경로로 답한다 (§5.4).
//
// rejected 를 0 으로 두고 message 만 채우는 형태는 OTLP 스펙이 정의한 "경고" 다.
// 큐에 넣기 전에 버렸으므로 배치 안 데이터포인트 수를 알 수 없다 — 아는 척하는 것보다
// 0 으로 두고 사람이 읽을 사유를 남기는 편이 정직하다.
func writePartialSuccess(w http.ResponseWriter, kind otlpdecode.PayloadKind, enc otlpdecode.Encoding, rejected int64, message string) {
	contentTypeHeader(w, enc)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(partialSuccessBody(kind, enc, rejected, message))
}

func partialSuccessBody(kind otlpdecode.PayloadKind, enc otlpdecode.Encoding, rejected int64, message string) []byte {
	if enc == otlpdecode.EncodingJSON {
		return partialSuccessJSON(kind, rejected, message)
	}
	return partialSuccessProto(rejected, message)
}

// partialSuccessProto 는 Export*ServiceResponse{partial_success} 를 protobuf 로 만든다.
// protojson 과 마찬가지로 제로값 필드는 넣지 않는다.
func partialSuccessProto(rejected int64, message string) []byte {
	var inner []byte
	if rejected != 0 {
		inner = appendTag(inner, 1, wireVarint)
		inner = binary.AppendUvarint(inner, uint64(rejected))
	}
	if message != "" {
		inner = appendTag(inner, 2, wireLen)
		inner = binary.AppendUvarint(inner, uint64(len(message)))
		inner = append(inner, message...)
	}
	if len(inner) == 0 {
		return nil
	}
	out := appendTag(nil, 1, wireLen)
	out = binary.AppendUvarint(out, uint64(len(inner)))
	return append(out, inner...)
}

func appendTag(b []byte, field, wire int) []byte {
	return binary.AppendUvarint(b, uint64(field)<<3|uint64(wire))
}

// jsonPartialSuccess 는 protojson 출력과 같은 필드 이름·순서를 쓴다.
// int64 는 protojson 규약대로 문자열로 나간다.
type jsonPartialSuccess struct {
	RejectedDataPoints string `json:"rejectedDataPoints,omitempty"`
	RejectedLogRecords string `json:"rejectedLogRecords,omitempty"`
	RejectedSpans      string `json:"rejectedSpans,omitempty"`
	ErrorMessage       string `json:"errorMessage,omitempty"`
}

type jsonExportResponse struct {
	PartialSuccess *jsonPartialSuccess `json:"partialSuccess,omitempty"`
}

func partialSuccessJSON(kind otlpdecode.PayloadKind, rejected int64, message string) []byte {
	ps := jsonPartialSuccess{ErrorMessage: message}
	if rejected != 0 {
		n := strconv.FormatInt(rejected, 10)
		switch kind {
		case otlpdecode.PayloadLogs:
			ps.RejectedLogRecords = n
		case otlpdecode.PayloadTraces:
			ps.RejectedSpans = n
		default:
			ps.RejectedDataPoints = n
		}
	}
	if ps == (jsonPartialSuccess{}) {
		return []byte("{}")
	}
	b, err := json.Marshal(jsonExportResponse{PartialSuccess: &ps})
	if err != nil {
		// 필드가 전부 문자열이라 도달할 수 없다. 그래도 빈 성공으로 떨어뜨린다.
		return []byte("{}")
	}
	return b
}

// writeError 는 4xx·5xx 응답이다.
//
// 본문은 요청에서 온 어떤 값도 반영하지 않는 고정 문자열이다. 헤더나 본문을 되비추면
// 토큰이 그대로 응답에 실려 나갈 수 있다 — 401 응답에 제시된 토큰을 적어 주는 실수는
// 흔하고, 그 응답은 프록시 로그에 남는다.
//
// OTLP 스펙은 오류 본문에 google.rpc.Status 를 권장하지만 text/plain 도 허용한다.
// proto 격리를 깨면서까지 Status 를 만들 이유가 없어 text/plain 을 쓴다.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message + "\n"))
}

// healthBody 는 /healthz 응답이다. 좌표와 카운터만 담는다.
type healthBody struct {
	Status        string `json:"status"`
	Service       string `json:"service"`
	QueueDepth    int    `json:"queue_depth"`
	QueueCapacity int    `json:"queue_capacity"`
	Stats         Stats  `json:"stats"`
}

func writeHealth(w http.ResponseWriter, body healthBody) {
	b, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
