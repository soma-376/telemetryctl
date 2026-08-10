package otlpdecode

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"

	logscolpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricscolpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecolpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/your-org/pulsemetry/internal/event"
)

// testOptions 는 디코더가 페이로드에서 알 수 없는 값의 고정 입력이다.
func testOptions() Options {
	return Options{InstallationID: "inst_test_0001", Vendor: "unknown"}
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("픽스처 %s 읽기: %v", name, err)
	}
	return data
}

func requestFor(kind PayloadKind) proto.Message {
	switch kind {
	case PayloadLogs:
		return &logscolpb.ExportLogsServiceRequest{}
	case PayloadTraces:
		return &tracecolpb.ExportTraceServiceRequest{}
	default:
		return &metricscolpb.ExportMetricsServiceRequest{}
	}
}

// toProtobuf 는 골든 픽스처(JSON)를 같은 내용의 protobuf 바이트로 옮긴다.
// 이것이 있어야 "같은 픽스처를 양쪽 경로로" 라는 요구를 한 파일로 만족할 수 있다.
func toProtobuf(t *testing.T, kind PayloadKind, jsonData []byte) []byte {
	t.Helper()
	msg := requestFor(kind)
	if err := unmarshalPayload(jsonData, EncodingJSON, msg); err != nil {
		t.Fatalf("픽스처를 proto 로 변환(unmarshal): %v", err)
	}
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("픽스처를 proto 로 변환(marshal): %v", err)
	}
	return raw
}

// decodeBoth 는 같은 픽스처를 protojson·protobuf 두 경로로 디코드하고 결과가 같은지 확인한 뒤
// 하나를 돌려준다. 개별 테스트가 두 경로를 각각 신경 쓰지 않아도 되게 한다.
func decodeBoth(t *testing.T, kind PayloadKind, name string, opt Options) Result {
	t.Helper()
	jsonData := loadFixture(t, name)
	fromJSON, err := Decode(kind, jsonData, EncodingJSON, opt)
	if err != nil {
		t.Fatalf("%s protojson 디코드: %v", name, err)
	}
	fromProto, err := Decode(kind, toProtobuf(t, kind, jsonData), EncodingProtobuf, opt)
	if err != nil {
		t.Fatalf("%s protobuf 디코드: %v", name, err)
	}
	if !reflect.DeepEqual(fromJSON, fromProto) {
		t.Fatalf("%s: protojson 과 protobuf 결과가 다름\njson  = %+v\nproto = %+v", name, fromJSON, fromProto)
	}
	return fromJSON
}

// payloadIn 은 골든 픽스처를 요청한 인코딩의 바이트로 돌려준다.
func payloadIn(t *testing.T, kind PayloadKind, name string, enc Encoding) []byte {
	t.Helper()
	data := loadFixture(t, name)
	if enc == EncodingJSON {
		return data
	}
	return toProtobuf(t, kind, data)
}

// collectStrings 는 값 안의 모든 문자열을 재귀로 모은다. 비공개 필드도 Kind 만 보고 읽으므로
// event.Opt 의 내부까지 훑는다 — 프라이버시 단언이 "출력 어디에도" 를 문자 그대로 검사할 수 있다.
func collectStrings(v reflect.Value, out *[]string) {
	switch v.Kind() {
	case reflect.String:
		*out = append(*out, v.String())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			collectStrings(v.Field(i), out)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectStrings(v.Index(i), out)
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			collectStrings(v.Elem(), out)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			collectStrings(iter.Key(), out)
			collectStrings(iter.Value(), out)
		}
	}
}

func allStrings(v any) []string {
	var out []string
	collectStrings(reflect.ValueOf(v), &out)
	return out
}

// eventByID 는 event.id 로 이벤트를 집는다. 픽스처의 레코드마다 고유한 event.id 를 넣어 뒀다.
func eventByID(t *testing.T, events []event.Event, id string) event.Event {
	t.Helper()
	for _, e := range events {
		if e.EventID == id {
			return e
		}
	}
	t.Fatalf("event.id=%q 인 이벤트를 찾지 못함", id)
	return event.Event{}
}

func contentsOf(res Result, index int) map[event.ContentKind]Content {
	out := map[event.ContentKind]Content{}
	for _, c := range res.Contents {
		if c.EventIndex == index {
			out[c.Kind] = c
		}
	}
	return out
}

func indexOfEventID(res Result, id string) int {
	for i, e := range res.Events {
		if e.EventID == id {
			return i
		}
	}
	return -1
}
