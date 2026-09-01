import { App, type AppInfo } from "$lib/bindings";
import { Window } from "@wailsio/runtime";

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

// 현재 창(트레이 퀵뷰)만 숨긴다. 앱 프로세스는 계속 실행된다.
//
// **Application.Hide 를 쓰면 안 된다.** 그건 앱 레벨이라 열려 있는 창을 전부 숨기고
// (메인 창까지) 포커스를 다음 애플리케이션으로 넘긴다 — Wails 의 windowsApp.hide 가
// 명시적으로 그렇게 한다. X 를 눌렀을 때 뒤에 있던 다른 프로그램이 앞으로 나오던 원인이다.
//
// main.go 가 OS 의 창 닫기를 가로챌 때도 창 단위(quick.Hide)로 숨긴다. 두 경로가 같아야 한다.
export async function hideCurrentWindow(): Promise<void> {
  try {
    await Window.Hide();
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
