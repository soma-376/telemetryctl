package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var trayIcon []byte

func main() {
	app := application.New(application.Options{
		Name:        "Pulsemetry",
		Description: "AI 도구 사용 현황 데스크톱 대시보드",
		Services: []application.Service{
			application.NewService(NewApp()),
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

	// X 버튼은 종료가 아니라 트레이로 내려간다 (상주 앱). 종료는 트레이 메뉴에서만.
	// RegisterHook 은 기본 파괴 리스너보다 먼저 동기 실행되므로 Cancel 이 확실히 먹는다.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		win.Hide()
	})

	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("Pulsemetry")
	tray.AttachWindow(win) // 트레이 클릭·더블클릭 → 창 토글

	menu := application.NewMenu()
	menu.Add("열기").OnClick(func(*application.Context) {
		win.Show()
		win.Focus()
	})
	menu.AddSeparator()
	menu.Add("종료").OnClick(func(*application.Context) {
		app.Quit()
	})
	tray.SetMenu(menu)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
