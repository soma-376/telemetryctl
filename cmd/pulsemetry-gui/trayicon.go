package main

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/your-org/pulsemetry/internal/dashboard/tray"
	"github.com/your-org/pulsemetry/internal/trayicon"
)

// trayIconInterval 은 트레이 아이콘을 다시 계산하는 주기다. 데몬의 저장된 스냅샷만 읽으므로
// 벤더 API 를 두드리지 않는다 — 한도 갱신 주기는 데몬이 정한다.
const trayIconInterval = 30 * time.Second

// trayIconUpdater 는 데몬 스냅샷을 주기적으로 읽어 트레이 아이콘을 바꾼다.
//
// 주기 갱신과 테마 변경 이벤트가 서로 다른 고루틴에서 들어오므로 상태는 mu 로 지킨다.
type trayIconUpdater struct {
	systray *application.SystemTray
	dash    *Dashboard
	// isDark 는 리눅스에서만 쓴다. 그쪽 Wails 구현은 SetDarkModeIcon 이 SetIcon 과 같아서
	// 두 장을 걸면 늘 다크용이 이긴다 — 테마를 직접 보고 한 장만 건다.
	isDark func() bool

	mu      sync.Mutex
	current tray.Icon
	applied bool
	// reapply 는 macOS 에서 메뉴가 열려 있는 동안의 교체가 화면에 반영되지 않는 문제
	// (wailsapp/wails#6154) 를 덮는다. 바꾼 다음 주기에 같은 그림을 한 번 더 건다.
	reapply bool
}

func newTrayIconUpdater(systray *application.SystemTray, dash *Dashboard, isDark func() bool) *trayIconUpdater {
	return &trayIconUpdater{systray: systray, dash: dash, isDark: isDark}
}

// setInitial 은 앱 실행 전 첫 그림을 건다. 데몬을 아직 묻지 않았으므로 빈 링이다.
// 리눅스는 이 시점에 테마를 물을 수 없어 밝은 테마로 두고, 첫 갱신에서 바로잡는다.
func (u *trayIconUpdater) setInitial() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.apply(tray.Icon{Kind: tray.IconRing}, false)
}

// run 은 ctx 가 끝날 때까지 주기적으로 아이콘을 갱신한다.
func (u *trayIconUpdater) run(ctx context.Context) {
	ticker := time.NewTicker(trayIconInterval)
	defer ticker.Stop()
	for {
		u.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// themeChanged 는 시스템 테마가 바뀌었을 때 지금 그림을 새 테마로 다시 건다.
// Windows·macOS 는 OS 가 알아서 바꾸므로 리눅스에서만 의미가 있다.
func (u *trayIconUpdater) themeChanged() {
	if runtime.GOOS != "linux" {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.applied {
		u.apply(u.current, u.isDark())
	}
}

func (u *trayIconUpdater) refresh(ctx context.Context) {
	// 최근 세션 목록은 쓰지 않지만 0 은 기본 길이로 해석되므로 1 로 줄여 묻는다.
	snap, err := u.dash.Tray(ctx, tray.Query{RecentLimit: 1})
	if ctx.Err() != nil {
		return
	}
	// 조회 실패는 이유와 무관하게 "데몬에 닿지 않음" 으로 본다. 화면이 아는 것은 숫자를
	// 받지 못했다는 사실뿐이고, 옛 숫자를 계속 보여 주면 데몬이 죽은 것을 숨긴다.
	icon := tray.IconFor(err == nil, snap.Limits)

	dark := runtime.GOOS == "linux" && u.isDark()

	u.mu.Lock()
	defer u.mu.Unlock()
	changed := !u.applied || icon != u.current
	if !changed && !u.reapply {
		return
	}
	u.reapply = runtime.GOOS == "darwin" && changed
	u.apply(icon, dark)
}

// apply 는 mu 를 쥔 채로 부른다. dark 는 리눅스에서만 본다.
func (u *trayIconUpdater) apply(icon tray.Icon, dark bool) {
	switch runtime.GOOS {
	case "darwin":
		u.systray.SetTemplateIcon(trayicon.Render(icon, trayicon.Template))
	case "linux":
		theme := trayicon.Light
		if dark {
			theme = trayicon.Dark
		}
		u.systray.SetIcon(trayicon.Render(icon, theme))
	default:
		u.systray.SetIcon(trayicon.Render(icon, trayicon.Light))
		u.systray.SetDarkModeIcon(trayicon.Render(icon, trayicon.Dark))
	}
	u.systray.SetTooltip(trayTooltip(icon))
	u.current, u.applied = icon, true
}

func trayTooltip(icon tray.Icon) string {
	if icon.Kind == tray.IconOffline {
		return "Pulsemetry — 데몬 연결 안 됨"
	}
	return fmt.Sprintf("Pulsemetry — 한도 사용률 %d%%", icon.Percent)
}
