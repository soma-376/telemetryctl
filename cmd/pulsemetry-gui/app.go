package main

import "github.com/wailsapp/wails/v3/pkg/application"

// App is the GUI boundary and intentionally does not import daemon code.
// Wails v3 service: 공개 메서드가 프런트엔드 바인딩으로 노출된다.
type App struct {
	app   *application.App
	main  *application.WebviewWindow
	quick *application.WebviewWindow
}

func NewApp() *App { return &App{} }

// bind 는 창 생성 뒤 참조를 주입한다. 바인딩으로 노출되지 않도록 소문자.
func (a *App) bind(app *application.App, main, quick *application.WebviewWindow) {
	a.app, a.main, a.quick = app, main, quick
}

func (a *App) GetAppInfo() AppInfo { return AppInfo{Name: "Pulsemetry", Version: "development"} }

// IsTrayVisible 은 WebView가 다시 만들어졌을 때도 네이티브 퀵뷰의 현재 상태를 복구하게 한다.
// tray:shown/tray:hidden은 상태 변경 알림일 뿐이므로 마운트 시점의 상태 원본이 될 수 없다.
func (a *App) IsTrayVisible() bool {
	return a.quick != nil && a.quick.IsVisible()
}

// OpenMainWindow 는 퀵뷰를 닫고 메인 창을 앞으로 가져온다 (퀵뷰 "Pulsemetry 열기").
func (a *App) OpenMainWindow() {
	if a.quick != nil {
		a.quick.Hide()
	}
	if a.main != nil {
		a.main.Show()
		a.main.Restore()
		a.main.Focus()
	}
}

// OpenMainSettings 는 메인 창을 열고 설정 모달을 띄우라는 이벤트를 보낸다 (퀵뷰 "트레이 설정").
func (a *App) OpenMainSettings() {
	a.OpenMainWindow()
	if a.app != nil {
		a.app.Event.Emit("open-settings")
	}
}

// Quit 은 앱을 완전히 종료한다 (퀵뷰 전원 버튼).
func (a *App) Quit() {
	if a.app != nil {
		a.app.Quit()
	}
}
