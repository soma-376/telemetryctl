package main

import (
	"context"

	"github.com/your-org/pulsemetry/internal/dashboard/tray"
	"github.com/your-org/pulsemetry/internal/hostenv"
	"github.com/your-org/pulsemetry/internal/localapi"
	"github.com/your-org/pulsemetry/internal/store"
)

// Dashboard 는 프런트엔드가 부를 수 있는 Go 함수의 목록이다. Wails 가 공개 메서드를
// 전부 TS 로 뽑으므로, 화면이 실제로 쓰는 것만 여기 둔다.
//
// GUI 는 SQLite 를 열지 않는다. 데몬이 읽어서 내려준 것을 받는다 (internal/localapi).
type Dashboard struct {
	// tray 는 앱당 하나여야 한다. 호출마다 새로 만들면 마지막 정상 스냅샷이 사라진다.
	tray *tray.Cache
}

// NewDashboard 는 데몬을 보는 조회 경계를 만든다. 실패하지 않는다.
//
// 여는 자원이 없어서 ServiceStartup·ServiceShutdown 도 없다 (둘 다 Wails 의 선택
// 인터페이스다). 데몬 주소는 조회할 때마다 runtime.json 에서 읽는다 — GUI 보다 데몬이
// 늦게 뜨거나 중간에 재시작되는 것이 정상이라 기동 시점에 붙잡아 둘 수 없다.
func NewDashboard() *Dashboard {
	// 홈 디렉터리를 못 찾는 것은 화면 입장에서 "미설치" 와 같은 처지다. 여기서 죽으면
	// GUI 가 아예 뜨지 않는다 — 미설치는 오류가 아니라 빈 결과다 (ADR 0004).
	dataDir := ""
	if env, err := hostenv.Detect(); err == nil {
		dataDir = store.DefaultDataDir(env)
	}
	return &Dashboard{tray: tray.New(localapi.NewClient(dataDir))}
}

// ServiceName 은 Wails 가 로그에 쓰는 이름이다. 없으면 타입 이름으로 짓는다.
func (d *Dashboard) ServiceName() string { return "Dashboard" }

// Tray 는 트레이 퀵뷰 한 장에 필요한 전부다. 갱신 주기 안이면 캐시를 그대로 준다.
func (d *Dashboard) Tray(ctx context.Context, q tray.Query) (tray.Snapshot, error) {
	return d.tray.Current(ctx, q)
}

// RefreshTray 는 데몬에 갱신을 명령하고 그 결과를 다시 받는다 (퀵뷰의 수동 새로고침).
func (d *Dashboard) RefreshTray(ctx context.Context, q tray.Query) (tray.Snapshot, error) {
	return d.tray.Refresh(ctx, q)
}
