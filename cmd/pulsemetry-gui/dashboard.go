package main

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/hostenv"
	"github.com/your-org/pulsemetry/internal/store"
)

// Dashboard 는 로컬 조회를 프런트엔드에 노출하는 경계다.
//
// dashboard.Service 를 그대로 바인딩하지 않는 이유가 둘 있다. 첫째, Wails 는 공개 메서드를
// 전부 바인딩하므로 Reader()·Start()·Stop() 까지 프런트에 나간다 — Reader 는 sync.Mutex 를
// 품은 내부 타입이라 TS 모델로 나갈 값이 아니다. 둘째, Wails 의 수명주기 훅 이름은
// ServiceStartup/ServiceShutdown 이라 Service 의 Start/Stop 이 저절로 불리지 않는다.
// 이 타입이 그 이름을 맞추고 노출면을 화면이 실제로 쓰는 것만으로 좁힌다.
//
// 반면 **반환 타입은 dashboard 패키지 것을 그대로 쓴다.** 여기서 GUI 전용 DTO 를 새로 만들면
// 필드가 늘 때마다 두 곳을 고쳐야 하고, 바인딩 생성기가 원본 구조체에서 TS 모델을 뽑는다는
// 전제(ADR 0004)도 깨진다.
type Dashboard struct {
	svc *dashboard.Service
}

// NewDashboard 는 기본 DB 경로를 보는 조회 서비스를 만든다. 실패하지 않는다.
func NewDashboard() *Dashboard {
	env, err := hostenv.Detect()
	if err != nil {
		// 홈 디렉터리를 못 찾는 것은 화면 입장에서 "미설치" 와 같은 처지다. 여기서 죽으면
		// GUI 가 아예 뜨지 않는다 — 미설치는 오류가 아니라 빈 결과다 (ADR 0004).
		return &Dashboard{svc: dashboard.NewService("")}
	}
	return &Dashboard{svc: dashboard.NewService(store.DefaultPath(env))}
}

func (d *Dashboard) ServiceName() string { return "Dashboard" }

// ServiceStartup 은 DB 를 연다. 열지 못해도 error 를 올리지 않는다 — 그 사실은 조회 결과의
// monitoring.state 가 말하고, 앱은 떠야 한다.
func (d *Dashboard) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	_ = d.svc.Start()
	return nil
}

func (d *Dashboard) ServiceShutdown() error { return d.svc.Stop() }

// Tray 는 트레이 퀵뷰 한 장에 필요한 전부다. 갱신 주기 안이면 캐시를 그대로 준다.
func (d *Dashboard) Tray(ctx context.Context, q dashboard.TrayQuery) (dashboard.TraySnapshot, error) {
	return d.svc.Tray(ctx, q)
}

// RefreshTray 는 주기를 무시하고 다시 만든다 (퀵뷰의 수동 새로고침).
func (d *Dashboard) RefreshTray(ctx context.Context, q dashboard.TrayQuery) (dashboard.TraySnapshot, error) {
	return d.svc.RefreshTray(ctx, q)
}
