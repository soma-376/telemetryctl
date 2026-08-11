package receiver

import (
	"bytes"
	"encoding/binary"
	"encoding/json"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

// validPayloadFraming 은 페이로드가 최소한 형식은 갖췄는지만 본다.
//
// # 왜 핸들러에서 완전한 디코드를 하지 않는가
//
// 계획서는 "핸들러는 인증 → 본문 읽기 → 유계 채널 전송 → 응답만" 이라고 못박았다 (§5.4).
// 동시에 "깨진 proto 는 400" 도 요구한다. 둘을 같이 만족시키려면 검사 비용이 응답 지연에
// 보이지 않을 만큼 작아야 한다. 그래서 여기서는 **의미를 보지 않고 골격만** 확인한다.
//
//   - protobuf: 태그·와이어타입·길이 접두만 훑는다. 메시지를 만들지 않으므로 할당이 없고
//     비용은 O(n) 이다. LEN 필드 안으로 재귀하지도 않는다 — 그 안은 문자열일 수도 있다.
//   - JSON: 최상위가 객체인지 확인하고 encoding/json 의 Valid 로 문법만 본다.
//
// 통과했지만 OTLP 메시지가 아닌 페이로드는 200 을 받고 워커에서 조용히 버려진다.
// 이건 의도된 절충이다 — 정확한 판정은 워커의 몫이고, 여기서는 "재시도해도 절대 성공하지
// 않을 명백한 쓰레기" 만 즉시 돌려보낸다.
func validPayloadFraming(b []byte, enc otlpdecode.Encoding) bool {
	if enc == otlpdecode.EncodingJSON {
		return validJSONObject(b)
	}
	return validProtobufFraming(b)
}

// validJSONObject 는 최상위가 JSON 객체인지 본다. OTLP/JSON 의 Export*ServiceRequest 는
// 항상 객체다 — 배열이나 스칼라는 protojson 이 어차피 거부한다.
func validJSONObject(b []byte) bool {
	trimmed := bytes.TrimLeft(b, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	return json.Valid(b)
}

// protobuf 와이어 타입 (protobuf encoding 스펙).
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireLen     = 2
	wireFixed32 = 5
)

// validProtobufFraming 은 protobuf 바이트열의 프레이밍만 검사한다.
//
// SGROUP(3)·EGROUP(4) 은 proto2 의 폐기된 group 문법이고 OTLP 에는 없으므로 거부한다.
// 6·7 은 스펙에 없는 값이라 항상 거부다. 필드 번호 0 도 스펙 위반이다.
func validProtobufFraming(b []byte) bool {
	for i := 0; i < len(b); {
		tag, n := binary.Uvarint(b[i:])
		if n <= 0 {
			return false
		}
		i += n

		if tag>>3 == 0 {
			return false
		}
		switch tag & 7 {
		case wireVarint:
			_, n := binary.Uvarint(b[i:])
			if n <= 0 {
				return false
			}
			i += n
		case wireFixed64:
			if len(b)-i < 8 {
				return false
			}
			i += 8
		case wireLen:
			size, n := binary.Uvarint(b[i:])
			if n <= 0 {
				return false
			}
			i += n
			if size > uint64(len(b)-i) {
				return false
			}
			i += int(size)
		case wireFixed32:
			if len(b)-i < 4 {
				return false
			}
			i += 4
		default:
			return false
		}
	}
	return true
}
