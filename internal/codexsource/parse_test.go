package codexsource

import (
	"errors"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestJSONLParserBoundsStructureAndFourMiBHeap(t *testing.T) {
	tooMany := []byte(`{"type":"event_msg","payload":{"items":[` + strings.Repeat(`{},`, 20_000) + `{}]}}`)
	if len(tooMany) >= maxJSONLLine {
		t.Fatal("구조 한도 입력이 행 한도보다 크다")
	}
	if _, err := ParseLine(tooMany, "owner", "", `"cli"`); !errors.Is(err, ErrJSONLStructureLimit) {
		t.Fatalf("작은 객체 다수 = %v", err)
	}
	deep := []byte(`{"type":"event_msg","payload":` + strings.Repeat(`[`, maxJSONLDepth+1) + `0` + strings.Repeat(`]`, maxJSONLDepth+1) + `}`)
	if _, err := ParseLine(deep, "owner", "", `"cli"`); !errors.Is(err, ErrJSONLStructureLimit) {
		t.Fatalf("깊은 중첩 = %v", err)
	}

	// 한 행에 가까운 정상 단일 본문도 원문과 파서의 전체 증가분이 64 MiB 이내다.
	large := []byte(`{"type":"event_msg","payload":{"type":"user_message","message":"` + strings.Repeat("x", maxJSONLLine-256) + `"}}`)
	if len(large) > maxJSONLLine {
		t.Fatal("큰 정상 입력이 행 한도를 넘는다")
	}
	runtime.GC()
	oldGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(oldGC)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	r, err := ParseLine(large, "owner", "", `"cli"`)
	runtime.ReadMemStats(&after)
	if err != nil || len(r.Body) != maxJSONLLine-256 {
		t.Fatalf("큰 정상 본문 처리 = %d, %v", len(r.Body), err)
	}
	if added := after.Alloc - before.Alloc; added > 64<<20 {
		t.Fatalf("4 MiB 행 파서 heap 증가 = %d", added)
	}
}
