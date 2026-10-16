//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// Windows·Linux는 Wails의 기본 포커스·디바운스 처리를 사용한다.
func newTrayWindow(quick *application.WebviewWindow) application.Window { return quick }
