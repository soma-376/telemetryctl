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
