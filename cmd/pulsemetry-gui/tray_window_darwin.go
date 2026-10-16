package main

/*
#cgo LDFLAGS: -framework AppKit
#include "tray_window_darwin.h"
*/
import "C"

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"unsafe"
)

// WebView와 바인딩은 Wails가 소유하고 실제 표시 창만 네이티브 패널로 교체한다.
type macTrayWindow struct{ *application.WebviewWindow }

func newTrayWindow(quick *application.WebviewWindow) application.Window {
	return &macTrayWindow{WebviewWindow: quick}
}

func (w *macTrayWindow) NativeWindow() unsafe.Pointer {
	return application.InvokeSyncWithResult(func() unsafe.Pointer {
		return C.pulsemetryTrayPanel(w.WebviewWindow.NativeWindow())
	})
}

func (w *macTrayWindow) IsVisible() bool {
	return application.InvokeSyncWithResult(func() bool {
		return bool(C.pulsemetryTrayVisible(C.pulsemetryTrayPanel(w.WebviewWindow.NativeWindow())))
	})
}

func (w *macTrayWindow) Show() application.Window {
	application.InvokeSync(func() { C.pulsemetryShowTray(C.pulsemetryTrayPanel(w.WebviewWindow.NativeWindow())) })
	w.EmitEvent("tray:shown")
	return w
}

func (w *macTrayWindow) Hide() application.Window {
	application.InvokeSync(func() { C.pulsemetryHideTray(C.pulsemetryTrayPanel(w.WebviewWindow.NativeWindow())) })
	w.EmitEvent("tray:hidden")
	return w
}

// NSPanel은 앱을 활성화하지 않고 키보드 포커스를 받을 수 있다.
func (w *macTrayWindow) Focus() {
	application.InvokeSync(func() { C.pulsemetryShowTray(C.pulsemetryTrayPanel(w.WebviewWindow.NativeWindow())) })
}
