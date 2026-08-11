package event

import (
	"strings"
	"testing"
)

// "값이 0" 과 "값이 오지 않음" 이 실제로 갈리는지 — 이 구분이 무너지면 rollup 의 분모와
// session 의 오류 카운트가 조용히 틀어진다.
func TestOptDistinguishesZeroFromUnset(t *testing.T) {
	var unset Opt[int64]
	zero := Some(int64(0))

	if unset == zero {
		t.Fatal("미설정과 0 이 같은 값으로 취급됨")
	}
	if v, ok := unset.Get(); ok || v != 0 {
		t.Errorf("미설정 Get() = (%d, %v), want (0, false)", v, ok)
	}
	if v, ok := zero.Get(); !ok || v != 0 {
		t.Errorf("Some(0).Get() = (%d, %v), want (0, true)", v, ok)
	}
	if unset.Valid() || !zero.Valid() {
		t.Error("Valid() 가 미설정과 0 을 구분하지 못함")
	}
	if got := unset.Or(-1); got != -1 {
		t.Errorf("미설정 Or(-1) = %d", got)
	}
	if got := zero.Or(-1); got != 0 {
		t.Errorf("Some(0).Or(-1) = %d, want 0", got)
	}

	// success 는 false 와 미설정이 특히 자주 헷갈린다.
	var unknown Opt[bool]
	if unknown == Some(false) {
		t.Fatal("성공 여부 미상과 실패가 같은 값으로 취급됨")
	}
}

// 값 타입이라 이벤트를 복사해 들고 있어도 나중 이벤트가 앞선 값을 바꾸지 못한다.
// 포인터였다면 조립기가 복사한 Event 를 통해 원본 수치가 흔들릴 수 있다.
func TestOptIsValueSemantics(t *testing.T) {
	original := Event{Measure: Measures{InputTokens: Some(int64(10))}}
	copied := original
	copied.Measure.InputTokens = Some(int64(20))

	if v, _ := original.Measure.InputTokens.Get(); v != 10 {
		t.Fatalf("복사본 변경이 원본에 반영됨: %d", v)
	}
}

func TestAttributesWithProjectDoesNotMutate(t *testing.T) {
	base := Attributes{Model: "claude-opus-5"}
	p := NormalizePath("/Users/jy/dev/telemetryctl")

	got := base.WithProject(p)
	if base.ProjectHash != "" || base.ProjectName != "" {
		t.Fatalf("원본이 변경됨: %+v", base)
	}
	if got.ProjectHash != p.Hash || got.ProjectName != "telemetryctl" {
		t.Fatalf("사본에 값이 안 들어감: %+v", got)
	}
	if strings.Contains(got.ProjectName, "/") || strings.Contains(got.ProjectHash, "/") {
		t.Fatalf("경로가 그대로 들어감: %+v", got)
	}
}

func TestEventValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Event)
		wantErr bool
	}{
		{"정상", func(*Event) {}, false},
		{"vendor 없음", func(e *Event) { e.Vendor = "" }, true},
		{"installation_id 없음", func(e *Event) { e.InstallationID = "" }, true},
		{"signal 없음", func(e *Event) { e.Signal = "" }, true},
		{"알 수 없는 signal", func(e *Event) { e.Signal = "trace" }, true},
		{"name 없음", func(e *Event) { e.Name = "" }, true},
		{"ts 0", func(e *Event) { e.TS = 0 }, true},
		{"ts 음수", func(e *Event) { e.TS = -1 }, true},
		{"sequence 음수", func(e *Event) { e.Sequence = -1 }, true},
		// start_ts 는 검증하지 않는다. events 에 컬럼이 없고 값이 이상해도 rollup 이
		// "모름"으로 다루면 그만이라, 메타데이터 하나 때문에 실제 비용 데이터포인트를
		// 통째로 버리는 쪽이 더 나쁘다.
		{"start_ts 미설정은 허용", func(e *Event) { e.StartTS = 0 }, false},
		{"start_ts 이상값도 이벤트를 죽이지 않는다", func(e *Event) { e.StartTS = -1 }, false},
		{"session_id 없음은 허용", func(e *Event) { e.SessionID = "" }, false},
		{"수치 전부 미설정은 허용", func(e *Event) { e.Measure = Measures{} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := baseEvent()
			tt.mutate(&e)
			err := e.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSignalAndTemporalityVocabulary(t *testing.T) {
	if !SignalMetric.Valid() || !SignalLog.Valid() {
		t.Error("metric·log 는 유효한 signal 이어야 한다")
	}
	if Signal("").Valid() || Signal("trace").Valid() {
		t.Error("정의되지 않은 signal 이 통과함")
	}
	want := map[Temporality]string{
		TemporalityUnspecified: "unspecified",
		TemporalityDelta:       "delta",
		TemporalityCumulative:  "cumulative",
	}
	for temp, s := range want {
		if got := temp.String(); got != s {
			t.Errorf("Temporality(%d).String() = %q, want %q", temp, got, s)
		}
	}
	// 제로값이 Unspecified 여야 한다 — 디코더가 못 읽은 metric 을 delta 로 오인하면
	// cumulative 가 그대로 합산돼 비용이 배로 잡힌다.
	var zero Temporality
	if zero != TemporalityUnspecified {
		t.Error("Temporality 제로값이 Unspecified 가 아님")
	}
}
