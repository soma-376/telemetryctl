import { App } from "../../bindings/github.com/your-org/pulsemetry/cmd/pulsemetry-gui";
import type { AppInfo } from "../../bindings/github.com/your-org/pulsemetry/cmd/pulsemetry-gui";

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

// 트레이 퀵뷰 → 메인 창 제어. 브라우저 프리뷰에서는 조용히 무시된다.
export async function openMainWindow(): Promise<void> {
  try {
    await App.OpenMainWindow();
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
