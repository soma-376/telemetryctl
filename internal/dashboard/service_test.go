package dashboard

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// Wails v3 서비스의 ServiceStartup 이 error 를 반환하면 앱 기동이 통째로 중단된다.
// DB 가 없는 상태(미설치·데몬 첫 실행 전)에서 Start 는 성공해야 한다 (ADR 0004).
func TestServiceStartsWithoutDatabase(t *testing.T) {
	path := store.PathIn(t.TempDir())
	svc := NewService(path)

	if err := svc.Start(); err != nil {
		t.Fatalf("DB 가 없다고 Start 가 실패했다: %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리

	if svc.Available() {
		t.Error("Available = true — 파일이 없는데 열렸다고 한다")
	}
	if svc.DatabasePath() != path {
		t.Errorf("DatabasePath = %q, want %q", svc.DatabasePath(), path)
	}

	ctx := context.Background()
	st, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Available {
		t.Error("Status.Available = true")
	}
	if rows, err := svc.Sessions(ctx, SessionQuery{}); err != nil || len(rows) != 0 {
		t.Errorf("Sessions = %v, %v — 미설치는 빈 결과여야 한다", rows, err)
	}
}

// 경로 자체가 잘못된 것은 미설치가 아니라 호출자 버그다. 생성자는 실패할 수 없으므로
// Start 가 보고한다.
func TestServiceStartReportsBadPath(t *testing.T) {
	svc := NewService("   ")
	if err := svc.Start(); err == nil {
		t.Error("빈 경로인데 Start 가 성공했다")
	}
	// 그래도 조회는 터지지 않아야 한다 — GUI 가 화면을 그릴 수는 있어야 한다.
	if _, err := svc.Status(context.Background()); err != nil {
		t.Errorf("Status: %v", err)
	}
}

// 인수조건: 데몬이 나중에 DB 를 만들면 **앱 재시작 없이** 재연결된다.
func TestServiceReconnectsWhenDaemonCreatesDatabase(t *testing.T) {
	dir := t.TempDir()
	path := store.PathIn(dir)
	ctx := context.Background()

	svc := NewService(path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	if svc.Available() {
		t.Fatal("Available = true — 아직 DB 가 없다")
	}

	// 여기서 데몬이 처음 뜬다.
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close() //nolint:errcheck // 테스트 정리
	if _, err := db.Write(ctx, store.Batch{
		Sessions: []session.Session{newSession("s-late", testNow.Add(-time.Hour))},
	}); err != nil {
		t.Fatalf("store.Write: %v", err)
	}

	// Start 를 다시 부르지 않는다. 다음 조회가 알아서 붙어야 한다.
	rows, err := svc.Sessions(ctx, SessionQuery{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(rows) != 1 || rows[0].SessionKey != "s-late" {
		t.Fatalf("세션 = %+v, want s-late 한 건 — 재연결하지 않았다", rows)
	}
	if !svc.Available() {
		t.Error("Available = false — 재연결 뒤에도 미설치로 보고한다")
	}
}

// 인수조건: Wails 와 CLI 의 동일 질의 결과가 일치한다.
//
// 둘 다 같은 Reader 를 쓰므로 사실은 같은 함수다. 그 사실이 깨지지 않았는지 —
// 서비스가 중간에서 값을 바꾸거나 걸러 내지 않는지 — 를 확인한다.
func TestServiceMatchesReaderResults(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-parity", at)},
		Events: []store.EventRecord{
			promptRecord("s-parity", "t-parity", at, 1, "인증 토큰 검증"),
			llmRecord("s-parity", "t-parity", at, 2, llmSpec{Cost: 2, Input: 50, Output: 10}),
		},
	})

	svc := NewService(f.path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	// 두 경로가 같은 "지금" 을 보게 맞춘다. 벽시계 차이는 계약이 아니다.
	svc.Reader().now = func() time.Time { return testNow }

	ctx := context.Background()
	id := f.sessionID(vendorClaude, "s-parity")

	pairs := []struct {
		name string
		cli  func() (any, error)
		gui  func() (any, error)
	}{
		{
			name: "Today",
			cli:  func() (any, error) { return f.reader.Today(ctx, seoul) },
			gui:  func() (any, error) { return svc.Today(ctx, seoul) },
		},
		{
			name: "Sessions",
			cli:  func() (any, error) { return f.reader.Sessions(ctx, SessionQuery{}) },
			gui:  func() (any, error) { return svc.Sessions(ctx, SessionQuery{}) },
		},
		{
			name: "Session",
			cli:  func() (any, error) { return f.reader.Session(ctx, id) },
			gui:  func() (any, error) { return svc.Session(ctx, id) },
		},
		{
			name: "Breakdown",
			cli: func() (any, error) {
				return f.reader.Breakdown(ctx, BreakdownQuery{Dim: DimVendor, TZ: seoul})
			},
			gui: func() (any, error) {
				return svc.Breakdown(ctx, BreakdownQuery{Dim: DimVendor, TZ: seoul})
			},
		},
		{
			name: "Search",
			cli:  func() (any, error) { return f.reader.Search(ctx, SearchQuery{Text: "인증"}) },
			gui:  func() (any, error) { return svc.Search(ctx, SearchQuery{Text: "인증"}) },
		},
		{
			name: "Vendors",
			cli:  func() (any, error) { return f.reader.Vendors(ctx) },
			gui:  func() (any, error) { return svc.Vendors(ctx) },
		},
		{
			name: "MCPUsage",
			cli:  func() (any, error) { return f.reader.MCPUsage(ctx, 14) },
			gui:  func() (any, error) { return svc.MCPUsage(ctx, 14) },
		},
	}
	for _, tc := range pairs {
		t.Run(tc.name, func(t *testing.T) {
			want, err := tc.cli()
			if err != nil {
				t.Fatalf("Reader.%s: %v", tc.name, err)
			}
			got, err := tc.gui()
			if err != nil {
				t.Fatalf("Service.%s: %v", tc.name, err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("%s 결과가 다르다:\nReader  = %+v\nService = %+v", tc.name, want, got)
			}
		})
	}
}

// Service 가 Reader 의 조회 메서드를 하나라도 빠뜨리면 GUI 는 그 화면을 만들 수 없다.
func TestServiceWrapsEveryQueryMethod(t *testing.T) {
	// Reader 에만 있고 서비스가 감쌀 이유가 없는 생명주기 메서드다.
	lifecycle := map[string]bool{"Reopen": true, "Close": true, "Path": true, "DataDir": true}

	svcType := reflect.TypeOf(&Service{})
	readerType := reflect.TypeOf(&Reader{})
	for i := range readerType.NumMethod() {
		name := readerType.Method(i).Name
		if lifecycle[name] {
			continue
		}
		if _, ok := svcType.MethodByName(name); !ok {
			t.Errorf("Service 가 Reader.%s 를 감싸지 않는다 — GUI 가 그 화면을 만들 수 없다", name)
		}
	}
}
