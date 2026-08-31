package dashboard

// Wails 바인딩 최신성의 **검증 가능한 부분** (PROJ-97).
//
// # 여기서 검증할 수 없는 것부터 밝힌다
//
// 티켓의 인수조건은 "GUI 모듈 빌드와 Wails 바인딩 최신성 검사가 통과한다" 이다.
// 그 검사는 `wails3 generate bindings` 의 산출물이 Go 타입과 일치하는지 보는 것이고,
// **이 레포에서는 실행할 수 없다** — GUI 모듈(gui/, 별도 go.mod)이 이 브랜치에 없고
// go.mod 에 Wails 의존도 없다 (ADR 0004 는 GUI 를 별도 모듈로 두기로 했다).
// 없는 것을 스텁으로 흉내 내면 그 검사는 통과하는 순간부터 아무것도 지키지 않는다.
//
// 그래서 여기서는 **바인딩이 최신이 아니면 반드시 깨질 성질** 만 고정한다. 세 가지다.
//
//  1. GUI 가 닿는 모든 응답 타입의 공개 필드에 snake_case json 태그가 있다.
//     태그가 곧 TS 필드명이라, 빠지면 프런트엔드가 조용히 undefined 를 읽는다 (ADR 0004).
//  2. 그 표면 어디에도 비밀로 보이는 필드가 없다. 바인딩은 구조체를 통째로 TS 로 옮기므로
//     필드 하나가 늘면 그 값이 화면·스크린샷·개발자도구로 그대로 나간다.
//  3. Service 가 Reader 의 모든 조회를 감싼다 (service_test.go 의 같은 이름 테스트).
//     감싸지 않은 질의는 바인딩에 나타나지 않아 GUI 가 그 화면을 만들 수 없다.
//
// 1번이 기존 TestPublicTypesUseSnakeCaseTags 와 다른 점은 **목록을 손으로 적지 않는다**
// 는 것이다. 손으로 적은 목록은 타입이 늘 때 갱신을 잊는 순간 조용히 구멍이 난다.
// 여기서는 Service·Classifier 의 메서드 시그니처에서 타입을 **재귀로** 끌어모은다 —
// 그 집합이 곧 바인딩이 만들어 낼 TS 타입 집합이다.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// bindingRoots 는 GUI 가 부르는 진입점 전부다.
//
// Service 는 Wails 서비스가 그대로 감싸는 타입이고 (ADR 0004), Classifier 는 Service 에
// 메서드로 붙어 있지 않지만 Service.Reader() 로 만들어 쓰는 것이 정해진 경로다.
// 분류 결과도 화면에 그려지므로 같은 규칙을 받아야 한다.
func bindingRoots() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(&Service{}),
		reflect.TypeOf(&Classifier{}),
	}
}

// guiFacingTypes 는 진입점의 시그니처에서 닿을 수 있는 구조체 타입 전부를 모은다.
//
// context.Context 와 error 는 바인딩이 다루지 않으므로 건너뛴다. 이 레포 밖의 타입도
// 재귀 대상이지만 태그 규칙은 우리 패키지의 것에만 적용한다 (time.Time 같은 표준
// 라이브러리 타입에 snake_case 를 요구할 수는 없다).
func guiFacingTypes(t *testing.T) map[reflect.Type]bool {
	t.Helper()
	found := map[reflect.Type]bool{}
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	errType := reflect.TypeOf((*error)(nil)).Elem()

	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice ||
			rt.Kind() == reflect.Array || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || found[rt] {
			return
		}
		if rt == reflect.TypeOf(reflect.Value{}) {
			return
		}
		found[rt] = true
		for i := range rt.NumField() {
			walk(rt.Field(i).Type)
		}
	}

	for _, root := range bindingRoots() {
		for i := range root.NumMethod() {
			sig := root.Method(i).Type
			for j := range sig.NumIn() {
				in := sig.In(j)
				if in == ctxType || in == root {
					continue
				}
				walk(in)
			}
			for j := range sig.NumOut() {
				out := sig.Out(j)
				if out == errType {
					continue
				}
				walk(out)
			}
		}
	}
	return found
}

// isOursType 은 이 레포가 소유한 타입인지 본다. 태그 규칙은 우리 것에만 건다.
func isOursType(rt reflect.Type) bool {
	return strings.Contains(rt.PkgPath(), "your-org/pulsemetry")
}

// TestWailsBinding_EveryGUIFacingTypeUsesSnakeCaseTags 는 GUI 표면 전체를 훑는다.
//
// 손으로 적은 목록과 달리 여기서는 타입을 추가하는 것만으로 자동으로 검사 대상이 된다.
// 태그를 빠뜨린 필드가 하나라도 있으면 그 필드는 TS 에서 Go 이름 그대로 나가고,
// 프런트엔드는 어디서도 실패하지 않은 채 undefined 를 읽는다.
func TestWailsBinding_EveryGUIFacingTypeUsesSnakeCaseTags(t *testing.T) {
	types := guiFacingTypes(t)
	if len(types) < 20 {
		t.Fatalf("GUI 표면에서 찾은 타입이 %d개뿐이다 — 수집기가 시그니처를 못 훑고 있다", len(types))
	}

	var checked int
	for rt := range types {
		if !isOursType(rt) {
			continue
		}
		checked++
		for i := range rt.NumField() {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			if f.Anonymous {
				// 임베드는 JSON 에서 평평하게 펼쳐지므로 태그를 갖지 않는다. 그 타입 자체가
				// 이미 수집돼 있어 필드들은 자기 자리에서 검사된다.
				continue
			}
			tag, ok := f.Tag.Lookup("json")
			if !ok {
				t.Errorf("%s.%s 에 json 태그가 없다 — Go 필드 이름이 그대로 TS 로 나간다",
					rt.Name(), f.Name)
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue // 의도적으로 내보내지 않는 필드다.
			}
			if !isSnakeCase(name) {
				t.Errorf("%s.%s 의 json 태그 %q 가 snake_case 가 아니다", rt.Name(), f.Name, name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("우리 타입을 하나도 검사하지 않았다")
	}

	// 화면별 대표 타입이 실제로 수집됐는지 확인한다. 수집기가 조용히 좁아지면 이 테스트
	// 전체가 무의미하게 통과한다.
	for _, want := range []any{
		HomeSummary{}, HomeBreakdown{}, ActivityPage{}, ActivityRow{},
		SessionDetail{}, SessionMetrics{}, SessionFileChanges{},
		SessionClassification{}, Status{}, TodaySummary{}, Row{}, Hit{},
		VendorStatus{}, MCPRow{}, WorkspaceFolder{},
	} {
		rt := reflect.TypeOf(want)
		if !types[rt] {
			t.Errorf("%s 가 GUI 표면 수집에서 빠졌다 — 이 화면의 계약이 검사되지 않는다", rt.Name())
		}
	}
}

// secretSegments 는 snake_case 태그의 **한 조각과 정확히 같으면** 비밀로 보는 단어다.
//
// 부분 문자열로 보면 쓸모가 없다 — "token" 이 input_tokens 를 잡고 "refresh" 가
// refreshed_at 을 잡는다. 조각 단위 비교라야 "토큰 수" 와 "토큰 값" 이 갈린다.
var secretSegments = []string{
	"token", "secret", "password", "passwd",
	"credential", "credentials", "bearer", "authorization",
}

// secretSubstrings 는 두 조각 이상으로 이루어진 비밀 이름이다.
var secretSubstrings = []string{
	"api_key", "access_key", "private_key", "secret_key",
	"access_token", "refresh_token", "id_token",
}

// secretish 는 snake_case 이름이 비밀로 보이는지 판정한다. 걸린 단어를 함께 돌려준다.
func secretish(tag string) (string, bool) {
	for _, sub := range secretSubstrings {
		if strings.Contains(tag, sub) {
			return sub, true
		}
	}
	for _, seg := range strings.Split(tag, "_") {
		for _, want := range secretSegments {
			if seg == want {
				return want, true
			}
		}
	}
	return "", false
}

// TestWailsBinding_NoSecretLookingFieldsOnTheGUISurface 는 바인딩이 TS 로 옮길 표면에
// 비밀로 보이는 필드가 없는지 본다.
//
// 로컬 저장이 관대해진 것(ADR 0010)은 식별 정보에 한한 결정이고 토큰에는 적용되지 않는다.
// 그 규칙을 사람의 기억이 아니라 테스트가 지키게 한다.
func TestWailsBinding_NoSecretLookingFieldsOnTheGUISurface(t *testing.T) {
	// 이름에 비밀 단어가 들어가지만 비밀이 아닌 필드다. 예외는 여기 명시적으로만 둔다 —
	// 목록이 늘어나기 시작하면 그것이 곧 이름 규칙을 다시 볼 신호다.
	allowed := map[string]bool{
		// 토큰 **점유율** 이다. 값이 아니라 비율이라 유출될 비밀이 없다.
		"VendorUsage.TokenSharePermille": true,
	}

	var checked int
	for rt := range guiFacingTypes(t) {
		if !isOursType(rt) {
			continue
		}
		for i := range rt.NumField() {
			f := rt.Field(i)
			if !f.IsExported() || f.Anonymous {
				continue
			}
			qualified := rt.Name() + "." + f.Name
			if allowed[qualified] {
				continue
			}
			checked++
			tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if tag == "" || tag == "-" {
				continue
			}
			if word, bad := secretish(tag); bad {
				t.Errorf("%s (json %q) 가 비밀로 보이는 이름이다 (%q) — 바인딩은 이 값을 그대로 화면으로 옮긴다",
					qualified, tag, word)
			}
		}
	}
	if checked == 0 {
		t.Fatal("검사한 필드가 없다")
	}
}

// TestWailsBinding_SecretNameMatcherIsCalibrated 는 위 판정이 실제로 무언가를 잡고
// 정상 필드를 오인하지 않는지 본다. 어느 쪽으로 무너져도 그 검사는 장식이 된다.
func TestWailsBinding_SecretNameMatcherIsCalibrated(t *testing.T) {
	cases := []struct {
		tag  string
		want bool
	}{
		{"ingest_token", true},
		{"api_key", true},
		{"refresh_token", true},
		{"authorization", true},
		{"user_password", true},
		{"client_secret", true},
		{"oauth_credentials", true},

		{"input_tokens", false},
		{"cache_read_tokens", false},
		{"session_key", false},
		{"turn_key", false},
		{"workspace_path", false},
		{"cost_usd", false},
		{"refreshed_at", false},
		{"last_flush_at", false},
	}
	for _, tc := range cases {
		word, got := secretish(tc.tag)
		if got != tc.want {
			t.Errorf("secretish(%q) = %v (%q), want %v", tc.tag, got, word, tc.want)
		}
	}
}

// TestWailsBinding_ServiceCoversEveryScreen 은 화면 목록과 Service 메서드를 대조한다.
//
// service_test.go 의 TestServiceWrapsEveryQueryMethod 는 Reader → Service 방향이다.
// 여기는 반대 방향 — **계획서의 화면 목록** 에 대응하는 메서드가 실제로 있는지 본다.
// 두 방향이 다 있어야 "Reader 에 없어서 Service 에도 없는" 구멍이 드러난다.
func TestWailsBinding_ServiceCoversEveryScreen(t *testing.T) {
	screens := []struct {
		screen string
		method string
	}{
		{"Home", "Home"},
		{"Home 사용량 분해", "HomeBreakdown"},
		{"Today(레거시 카드)", "Today"},
		{"Activity 목록", "Activity"},
		{"Activity 세션 상세", "Session"},
		{"세션 지표", "SessionMetrics"},
		{"세션 파일 변경", "FileChanges"},
		{"통합 검색", "Search"},
		{"Insights 집계", "Breakdown"},
		{"Insights MCP 카드", "MCPUsage"},
		{"Settings 연결 상태", "Vendors"},
		{"Settings 상태", "Status"},
		{"작업 폴더 열기", "OpenWorkspace"},
		{"작업 폴더 판정", "WorkspaceFolder"},
	}
	svc := reflect.TypeOf(&Service{})
	for _, s := range screens {
		if _, ok := svc.MethodByName(s.method); !ok {
			t.Errorf("화면 %q 의 Service.%s 가 없다 — 바인딩에 나타나지 않아 GUI 가 이 화면을 만들 수 없다",
				s.screen, s.method)
		}
	}

	// 분류는 Service 메서드가 아니다. GUI 는 Service.Reader() 로 만들어 쓴다 —
	// 그 경로가 살아 있는지 여기서 붙들어 둔다 (classify_query.go 머리말의 결정).
	if _, ok := svc.MethodByName("Reader"); !ok {
		t.Error("Service.Reader 가 없다 — 분류기를 만들 경로가 사라진다")
	}
	if _, ok := reflect.TypeOf(&Classifier{}).MethodByName("Session"); !ok {
		t.Error("Classifier.Session 이 없다")
	}
}

// TestWailsBinding_ScreenResponsesRoundTripThroughJSON 은 화면 응답이 실제로
// 직렬화되는지 본다. 바인딩은 JSON 을 거치므로 여기서 실패하는 값은 화면에 닿지 못한다.
//
// 특히 NaN·Inf 가 섞이면 encoding/json 이 통째로 실패해 화면 하나가 아니라 조회 전체가
// 죽는다 (Card.HasBaseline 주석의 같은 이유).
func TestWailsBinding_ScreenResponsesRoundTripThroughJSON(t *testing.T) {
	f := newFixture(t)
	seedCleanDay(t, f)
	ctx := context.Background()
	id := f.sessionID(vendorClaude, "cs-a")

	responses := []struct {
		name string
		call func() (any, error)
	}{
		{"Home", func() (any, error) { return f.reader.Home(ctx, HomeQuery{TZ: seoul, Date: crossDay}) }},
		{"HomeBreakdown", func() (any, error) {
			return f.reader.HomeBreakdown(ctx, HomeBreakdownQuery{TZ: seoul, Date: crossDay})
		}},
		{"Today", func() (any, error) { return f.reader.Today(ctx, seoul) }},
		{"Activity", func() (any, error) { return f.reader.Activity(ctx, ActivityQuery{}) }},
		{"Session", func() (any, error) { return f.reader.Session(ctx, id) }},
		{"SessionMetrics", func() (any, error) {
			return f.reader.SessionMetrics(ctx, SessionMetricsQuery{SessionID: id})
		}},
		{"FileChanges", func() (any, error) { return f.reader.FileChanges(ctx, id) }},
		{"Search", func() (any, error) { return f.reader.Search(ctx, SearchQuery{Text: "인증"}) }},
		{"Breakdown", func() (any, error) {
			return f.reader.Breakdown(ctx, BreakdownQuery{Dim: DimVendor, TZ: seoul})
		}},
		{"Vendors", func() (any, error) { return f.reader.Vendors(ctx) }},
		{"MCPUsage", func() (any, error) { return f.reader.MCPUsage(ctx, 14) }},
		{"Status", func() (any, error) { return f.reader.Status(ctx) }},
		{"Classification", func() (any, error) { return NewClassifier(f.reader).Session(ctx, id) }},
		{"WorkspaceFolder", func() (any, error) { return f.reader.WorkspaceFolder(ctx, id) }},
	}
	for _, tc := range responses {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.call()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			assertJSONSerializable(t, tc.name, got)
		})
	}
}

// assertJSONSerializable 은 응답이 JSON 왕복을 견디는지 본다.
//
// Marshal 만 보면 부족하다 — nil 슬라이스가 null 로 나가는 것은 Marshal 이 성공하는
// 실패이고, 프런트엔드는 그 null 에 .map 을 걸어 터진다. 그래서 결과 문자열에 최상위
// null 이 없는 것도 함께 본다.
func assertJSONSerializable(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s 직렬화 실패: %v — 이 응답은 화면에 닿지 못한다", name, err)
	}
	if string(raw) == "null" {
		t.Fatalf("%s 가 통째로 null 이다", name)
	}
	// 왕복이 성립해야 GUI 가 돌려보낸 값을 다시 읽을 수 있다.
	out := reflect.New(reflect.TypeOf(v))
	if err := json.Unmarshal(raw, out.Interface()); err != nil {
		t.Fatalf("%s 역직렬화 실패: %v", name, err)
	}
}
