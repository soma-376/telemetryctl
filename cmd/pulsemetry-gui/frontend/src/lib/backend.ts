export interface AppInfo {
  name: string;
  version: string;
}

declare global {
  interface Window {
    go?: { main?: { App?: { GetAppInfo(): Promise<AppInfo> } } };
  }
}

export async function getAppInfo(): Promise<AppInfo> {
  const call = window.go?.main?.App?.GetAppInfo;
  return call ? call() : { name: "Pulsemetry", version: "browser preview" };
}
