package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/your-org/pulsemetry/internal/desktopinstall"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// 릴리스 검증은 창이나 데몬을 시작하지 않고 주입된 버전만 확인한다.
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("pulsemetry-gui %s\n", Version)
		return
	}
	if removalCommand() {
		return
	}
	uninstallMode := desktopinstall.IsUninstaller() || (len(os.Args) == 2 && os.Args[1] == "--uninstall")

	svc := NewApp()
	dash := NewDashboard()
	removal := &Removal{enabled: uninstallMode}

	app := application.New(application.Options{
		Name:        "Pulsemetry",
		Description: "AI 도구 사용 현황 데스크톱 대시보드",
		Services: []application.Service{
			application.NewService(svc),
			application.NewService(dash),
			application.NewService(removal),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	removal.app = app
	if uninstallMode {
		win := app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "Pulsemetry 제거", Width: 620, Height: 680, MinWidth: 520, MinHeight: 540, URL: "/?view=uninstall"})
		win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			removal.mu.Lock()
			defer removal.mu.Unlock()
			if removal.busy && !removal.done {
				e.Cancel()
			}
		})
		svc.bind(app, win, nil)
		if err := app.Run(); err != nil {
			log.Fatal(err)
		}
		return
	}
	installed, err := desktopinstall.RegisterGUI(false)
	if err != nil {
		log.Printf("제거 도구 등록 실패: %v", err)
	}
	if installed {
		home, _ := os.UserHomeDir()
		lease, err := desktopinstall.AcquireGUI(home)
		if err != nil {
			log.Print(err)
			return
		}
		defer lease.Close()
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { go desktopinstall.WatchRemoval(app.Context(), home, app.Quit) })
	}

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Pulsemetry",
		Width:     1080,
		Height:    860,
		MinWidth:  920,
		MinHeight: 640,
		URL:       "/",
	})

	// X 버튼은 종료가 아니라 트레이로 내려간다 (상주 앱). 종료는 트레이에서만.
	// RegisterHook 은 기본 파괴 리스너보다 먼저 동기 실행되므로 Cancel 이 확실히 먹는다.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		win.Hide()
	})

	win.RegisterHook(events.Common.WindowShow, func(*application.WindowEvent) { win.EmitEvent("main:shown") })
	win.RegisterHook(events.Common.WindowHide, func(*application.WindowEvent) { win.EmitEvent("main:hidden") })

	// 트레이 퀵뷰는 포커스를 잃어도 유지한다.
	// macOS에서는 열 때 현재 Space로 이동하고, 다른 앱의 전체 화면에도 함께 표시한다.
	quick := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:         "Pulsemetry Quick View",
		Width:         392,
		Height:        600,
		Frameless:     true,
		Hidden:        true,
		DisableResize: true,
		URL:           "/?view=tray",
		Mac: application.MacWindow{
			CollectionBehavior: application.MacWindowCollectionBehaviorMoveToActiveSpace |
				application.MacWindowCollectionBehaviorFullScreenAuxiliary,
		},
	})
	quick.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		quick.Hide()
	})
	// WebView의 document.visibilityState는 네이티브 창 Hide/Show를 보장해서 반영하지
	// 않는다. 퀵뷰가 실제로 보이는 동안만 프런트가 조회하도록 명시적인 이벤트를 보낸다.
	quick.RegisterHook(events.Common.WindowShow, func(*application.WindowEvent) {
		quick.EmitEvent("tray:shown")
	})
	quick.RegisterHook(events.Common.WindowHide, func(*application.WindowEvent) {
		quick.EmitEvent("tray:hidden")
	})

	svc.bind(app, win, quick)

	tray := app.SystemTray.New()
	// 아이콘은 연결된 벤더 한도 사용률의 최댓값을 링 게이지로 보여준다 (internal/trayicon 이
	// 런타임에 그린다). 시작 직후에는 빈 링을 걸어 두고, 앱이 뜬 뒤부터 데몬 스냅샷으로 갱신한다.
	icons := newTrayIconUpdater(tray, dash, app.Env.IsDarkMode)
	icons.setInitial()
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go icons.run(app.Context())
	})
	app.Event.OnApplicationEvent(events.Common.ThemeChanged, func(*application.ApplicationEvent) {
		go icons.themeChanged()
	})
	tray.AttachWindow(quick) // 클릭 → 퀵뷰 토글
	tray.WindowOffset(8)
	tray.WindowDebounce(200 * time.Millisecond)
	tray.OnDoubleClick(func() { svc.OpenMainWindow() })

	menu := application.NewMenu()
	menu.Add("열기").OnClick(func(*application.Context) { svc.OpenMainWindow() })
	menu.AddSeparator()
	menu.Add("종료").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
