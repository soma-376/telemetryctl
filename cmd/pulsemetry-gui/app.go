package main

// App is the GUI boundary and intentionally does not import daemon code.
// Wails v3 service: 공개 메서드가 프런트엔드 바인딩으로 노출된다.
type App struct{}

func NewApp() *App                 { return &App{} }
func (a *App) GetAppInfo() AppInfo { return AppInfo{Name: "Pulsemetry", Version: "development"} }
