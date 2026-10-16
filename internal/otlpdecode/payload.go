package otlpdecode

import (
	"encoding/json"
	"errors"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/your-org/pulsemetry/internal/event"
)

// Payload는 같은 디코드 결과의 이벤트에 대응하는 수신 OTLP JSON이다 (ADR 0025).
type Payload struct {
	EventIndex int
	JSON       json.RawMessage
}

const maxBatchPayloadBytes = 4 << 20

var errPayloadTooLarge = errors.New("payload 크기 상한 초과")

// payloadNode는 수신 JSON의 위치를 보관한다. 부모 배열을 선택한 한 항목으로 좁혀
// JSON을 만들므로 resource·scope·미지원 필드가 유지되고 다른 이벤트는 복사되지 않는다.
type payloadNode struct {
	obj    map[string]json.RawMessage
	raw    json.RawMessage
	parent *payloadNode
	field  string
	array  bool
}

func payloadRoot(data []byte, enc Encoding, msg proto.Message, skip bool) *payloadNode {
	if skip {
		return nil
	}
	if enc == EncodingProtobuf {
		var err error
		data, err = protojson.Marshal(msg)
		if err != nil {
			return nil
		}
	}
	return &payloadNode{raw: data}
}

func (n *payloadNode) child(field string) *payloadNode {
	if n == nil {
		return nil
	}
	if n.obj == nil {
		if json.Unmarshal(n.raw, &n.obj) != nil {
			return nil
		}
	}
	if _, ok := n.obj[field]; !ok {
		var snake strings.Builder
		for _, c := range field {
			if c >= 'A' && c <= 'Z' {
				snake.WriteByte('_')
				c += 'a' - 'A'
			}
			snake.WriteRune(c)
		}
		field = snake.String()
	}
	return &payloadNode{raw: n.obj[field], parent: n, field: field}
}

func (n *payloadNode) children(field string) []*payloadNode {
	child := n.child(field)
	if child == nil {
		return nil
	}
	var values []json.RawMessage
	if json.Unmarshal(child.raw, &values) != nil {
		return nil
	}
	out := make([]*payloadNode, len(values))
	for i, raw := range values {
		out[i] = &payloadNode{raw: raw, parent: n, field: child.field, array: true}
	}
	return out
}

func payloadAt(nodes []*payloadNode, i int) *payloadNode {
	if i < len(nodes) {
		return nodes[i]
	}
	return nil
}

func (n *payloadNode) json() ([]byte, error) {
	raw := n.raw
	for n.parent != nil {
		if len(raw) > event.MaxPayloadBytes {
			return nil, errPayloadTooLarge
		}
		if n.array {
			raw = append(append([]byte{'['}, raw...), ']')
		}
		obj := make(map[string]json.RawMessage, len(n.parent.obj))
		for key, value := range n.parent.obj {
			obj[key] = value
		}
		obj[n.field] = raw
		// 큰 resource를 이벤트마다 직렬화하기 전에 생략한다.
		size := 0
		for key, value := range obj {
			size += len(key) + len(value)
		}
		if size > event.MaxPayloadBytes {
			return nil, errPayloadTooLarge
		}
		var err error
		raw, err = json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		n = n.parent
	}
	return raw, nil
}

func (d *decoder) appendPayload(n *payloadNode) {
	if d.opt.SkipPayload {
		return
	}
	if n == nil || d.payloadBytes >= maxBatchPayloadBytes {
		d.payloadsDropped++
		return
	}
	raw, err := n.json()
	if err != nil || len(raw) > event.MaxPayloadBytes || len(raw)+d.payloadBytes > maxBatchPayloadBytes {
		d.payloadsDropped++
		return
	}
	d.payloads = append(d.payloads, Payload{EventIndex: len(d.events) - 1, JSON: raw})
	d.payloadBytes += len(raw)
}
