package main

import "context"

// App is the GUI boundary and intentionally does not import daemon code.
type App struct{ ctx context.Context }

func NewApp() *App                         { return &App{} }
func (a *App) startup(ctx context.Context) { a.ctx = ctx }
func (a *App) GetAppInfo() AppInfo         { return AppInfo{Name: "Pulsemetry", Version: "development"} }
