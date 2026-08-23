package main

import (
	"embed"
	"log"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var trayIcon []byte

func main() {
	svc := NewApp()

	app := application.New(application.Options{
		Name:        "Pulsemetry",
		Description: "AI 도구 사용 현황 데스크톱 대시보드",
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Pulsemetry",
		Width:     1080,
		Height:    860,
		MinWidth:  880,
		MinHeight: 640,
		URL:       "/",
	})

	// X 버튼은 종료가 아니라 트레이로 내려간다 (상주 앱). 종료는 트레이에서만.
	// RegisterHook 은 기본 파괴 리스너보다 먼저 동기 실행되므로 Cancel 이 확실히 먹는다.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		win.Hide()
	})

	// 트레이 퀵뷰 — 프레임 없는 팝업. 트레이 클릭으로 토글되고 포커스를 잃으면 닫힌다.
	quick := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:         "Pulsemetry Quick View",
		Width:         392,
		Height:        600,
		Frameless:     true,
		AlwaysOnTop:   true,
		Hidden:        true,
		DisableResize: true,
		URL:           "/?view=tray",
	})
	quick.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		quick.Hide()
	})

	svc.bind(app, win, quick)

	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("Pulsemetry")
	tray.AttachWindow(quick) // 클릭 → 퀵뷰 토글 (포커스 잃으면 자동 숨김)
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
