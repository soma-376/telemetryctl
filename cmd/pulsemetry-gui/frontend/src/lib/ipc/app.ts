import { App, type AppInfo } from "$lib/bindings";

export type { AppInfo };

// Wails 밖(브라우저 프리뷰, vite dev 단독)에서는 런타임 호출이 실패하므로
// 폴백을 유지한다.
export async function getAppInfo(): Promise<AppInfo> {
  try {
    return await App.GetAppInfo();
  } catch {
    return { name: "Pulsemetry", version: "browser preview" };
  }
}

// WebView가 절전 복귀 등으로 다시 만들어져도 네이티브 퀵뷰의 현재 상태를 복구한다.
export async function isTrayVisible(): Promise<boolean> {
  try {
    return await App.IsTrayVisible();
  } catch {
    return false;
  }
}

// 트레이 퀵뷰 → 메인 창 제어. 브라우저 프리뷰에서는 조용히 무시된다.
export async function openMainWindow(): Promise<void> {
  try {
    await App.OpenMainWindow();
  } catch {
    /* browser preview */
  }
}

// 실제 퀵뷰만 숨긴다. macOS는 숨은 Wails 호스트 대신 네이티브 패널을 닫는다.
export async function hideCurrentWindow(): Promise<void> {
  try {
    await App.HideTrayWindow();
  } catch {
    /* browser preview */
  }
}

export async function openMainSettings(): Promise<void> {
  try {
    await App.OpenMainSettings();
  } catch {
    /* browser preview */
  }
}

export async function quitApp(): Promise<void> {
  try {
    await App.Quit();
  } catch {
    /* browser preview */
  }
}
