package dashboard

import (
	"context"
	"time"
)

// Service 는 Wails v3 데스크탑 서비스가 그대로 감싸는 조회 서비스다 (ADR 0004).
//
// # 왜 Reader 위에 한 겹이 더 있는가
//
// ADR 0004 는 GUI 가 `internal/dashboard` 를 직접 import 해 `application.NewService` 로
// 등록하도록 정했다. 그 서비스가 만족해야 하는 것이 두 가지 있는데 둘 다 Reader 하나로는
// 되지 않는다.
//
//  1. **ServiceStartup 이 error 를 반환하면 앱 기동 자체가 중단된다.** 아직 enroll 하지
//     않았거나 데몬을 한 번도 켜지 않은 사용자에게는 DB 파일이 없다. Start 는 그 상태를
//     성공으로 처리한다.
//  2. **데몬이 나중에 DB 를 만들면 앱 재시작 없이 붙어야 한다.** Reader.Reopen 이 그
//     동작을 갖고 있지만 누군가 매 조회마다 불러 줘야 한다. 그 "누군가" 가 여기다 —
//     GUI 쪽에 두면 화면마다 잊을 수 있고, Reader 안에 두면 CLI 의 단발 조회까지
//     매번 파일 시스템을 두드린다.
//
// # Wails 를 import 하지 않는다
//
// 이 타입에는 Wails 의존이 없다. GUI 모듈의 서비스는 이 타입을 필드로 들고 자기
// `ServiceStartup`·`ServiceShutdown` 에서 Start·Stop 을 부르고, 조회 메서드는 그대로
// 위임하면 된다. 그래야 표준 `go test` 로 화면 쿼리를 검증할 수 있다 (ADR 0004).
//
// # 비어 있는 결과가 정상이다
//
// 모든 조회는 DB 가 없어도 성공하고 빈 결과를 준다. 화면은 Status().Available 로
// "아직 데이터 없음" 안내를 그린다.
type Service struct {
	reader *Reader
	// initErr 는 생성 시점에 발견한 경로 오류다. 생성자는 실패할 수 없으므로 (GUI 가
	// 필드 초기화에서 서비스를 만든다) 여기 담아 두고 Start 가 보고한다.
	initErr error

	// tray 는 트레이 스냅샷의 갱신 주기와 마지막 정상값을 들고 있다 (tray.go).
	// 서비스 하나에 하나여야 한다 — 호출마다 새로 만들면 "마지막 정상 스냅샷" 이
	// 매번 사라져 실패가 곧 빈 화면이 된다.
	tray *TrayMonitor

	// opener 는 작업 폴더를 여는 자리다 (openfolder.go). 운영 경로는 exec 로 OS 파일
	// 관리자를 argv 호출한다. 비공개 필드라 GUI 가 바꿔치기할 수 없고, 패키지 안의
	// 테스트만 진짜 실행을 대신할 수 있다.
	opener FolderOpener
}

// NewService 는 dbPath 를 보는 조회 서비스를 만든다. 아직 DB 를 열지는 않는다.
//
// 실패하지 않는다. 경로가 잘못됐다는 사실은 Start 가 보고한다 — GUI 는 서비스를
// 필드 초기화에서 만들고 기동은 나중에 하므로 생성자에 error 를 둘 자리가 없다.
func NewService(dbPath string) *Service {
	r, err := newReader(dbPath)
	if err != nil {
		// 조회는 전부 "미설치" 로 동작해야 한다. 경로가 없어도 now 가 살아 있는 Reader 를
		// 둔다 — nil 을 두면 Start 를 건너뛴 호출자가 그 자리에서 터진다.
		r = &Reader{now: time.Now}
		return newServiceFor(r, err)
	}
	return newServiceFor(r, nil)
}

func newServiceFor(r *Reader, initErr error) *Service {
	return &Service{reader: r, initErr: initErr, tray: NewTrayMonitor(r), opener: execOpener{}}
}

// Start 는 GUI 의 ServiceStartup 자리다. DB 부재는 실패가 아니다.
//
// 경로가 비었거나 그 자리에 디렉터리가 있는 것처럼 **호출자 버그와 진짜 고장**만 error 다.
func (s *Service) Start() error {
	if s.initErr != nil {
		return s.initErr
	}
	return s.reader.attach()
}

// Stop 은 GUI 의 ServiceShutdown 자리다. 열린 적이 없어도 안전하다.
func (s *Service) Stop() error { return s.reader.Close() }

// Reader 는 하위 조회 핸들이다. 이 서비스가 아직 감싸지 않은 질의가 필요한 호출자를 위한 것이다.
func (s *Service) Reader() *Reader { return s.reader }

// Available 은 DB 파일이 실제로 열려 있는지 알려준다.
func (s *Service) Available() bool { return s.reader.Available() }

// DatabasePath 는 조회 대상 DB 파일의 절대 경로다.
func (s *Service) DatabasePath() string { return s.reader.Path() }

// reconnect 는 아직 붙지 못한 DB 를 다시 열어 본다.
//
// 실패를 삼키는 것이 의도다. 재연결에 실패했다는 사실로 화면이 할 수 있는 일이 없고,
// 그 자리에서 error 를 올리면 "DB 가 아직 없다" 는 정상 상태가 에러 토스트가 된다.
// 진짜 상태는 Status().Available 이 말한다.
func (s *Service) reconnect() {
	if s.reader.Available() {
		return
	}
	_ = s.reader.Reopen() //nolint:errcheck // 위 주석: 재연결 실패는 화면에 올리지 않는다
}

// Today 는 tz 기준 오늘의 요약이다.
func (s *Service) Today(ctx context.Context, tz string) (TodaySummary, error) {
	s.reconnect()
	return s.reader.Today(ctx, tz)
}

// Sessions 는 세션 목록이다.
func (s *Service) Sessions(ctx context.Context, q SessionQuery) ([]SessionRow, error) {
	s.reconnect()
	return s.reader.Sessions(ctx, q)
}

// Session 은 세션 하나의 상세다. id 는 sessions.id 다.
func (s *Service) Session(ctx context.Context, id int64) (SessionDetail, error) {
	s.reconnect()
	return s.reader.Session(ctx, id)
}

// Breakdown 은 축별·시간별 집계다.
func (s *Service) Breakdown(ctx context.Context, q BreakdownQuery) ([]Row, error) {
	s.reconnect()
	return s.reader.Breakdown(ctx, q)
}

// Search 는 제목·파일 경로·원문 통합 검색이다.
func (s *Service) Search(ctx context.Context, q SearchQuery) ([]Hit, error) {
	s.reconnect()
	return s.reader.Search(ctx, q)
}

// Vendors 는 벤더별 연결 상태다.
func (s *Service) Vendors(ctx context.Context) ([]VendorStatus, error) {
	s.reconnect()
	return s.reader.Vendors(ctx)
}

// MCPUsage 는 최근 N개 세션의 MCP 사용 집계다.
func (s *Service) MCPUsage(ctx context.Context, lastNSessions int) ([]MCPRow, error) {
	s.reconnect()
	return s.reader.MCPUsage(ctx, lastNSessions)
}

// Status 는 로컬 파이프라인의 현재 상태다. 화면의 "아직 데이터 없음" 안내가 이것을 본다.
func (s *Service) Status(ctx context.Context) (Status, error) {
	s.reconnect()
	return s.reader.Status(ctx)
}

// Tray 는 트레이 한 장이다 — 모니터링 상태·마지막 갱신 시각·활성/최근 세션·벤더 한도·
// 가장 빠듯한 한도가 한 응답에 들어 있다 (tray.go).
//
// 갱신 주기(DefaultTrayInterval) 안이면 직전 스냅샷을 그대로 준다. 갱신이 실패해도
// 에러가 아니라 마지막 정상 스냅샷 + Stale 이다.
func (s *Service) Tray(ctx context.Context, q TrayQuery) (TraySnapshot, error) {
	s.reconnect()
	return s.tray.Snapshot(ctx, q)
}

// RefreshTray 는 주기를 무시하고 즉시 다시 만든다. 트레이의 "새로고침" 이 부른다.
func (s *Service) RefreshTray(ctx context.Context, q TrayQuery) (TraySnapshot, error) {
	s.reconnect()
	return s.tray.Refresh(ctx, q)
}

// OpenWorkspace 는 세션의 작업 폴더를 OS 파일 관리자로 연다.
//
// **인자가 세션 id 하나다.** 프런트가 임의 경로를 건넬 자리가 타입에 없고, 열 경로는
// 언제나 sessions.workspace_path 에서 우리가 직접 읽은 값이다 (openfolder.go).
func (s *Service) OpenWorkspace(ctx context.Context, sessionID int64) (WorkspaceFolder, error) {
	s.reconnect()
	return openWorkspace(ctx, s.reader, s.opener, sessionID)
}

// WorkspaceFolder 는 열지 않고 열 수 있는지만 판정한다. 화면이 메뉴 항목을 비활성화할 때 쓴다.
func (s *Service) WorkspaceFolder(ctx context.Context, sessionID int64) (WorkspaceFolder, error) {
	s.reconnect()
	return s.reader.WorkspaceFolder(ctx, sessionID)
}
