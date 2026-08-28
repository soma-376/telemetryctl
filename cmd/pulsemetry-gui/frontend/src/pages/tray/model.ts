import type { TrayLimitWindow } from "./types";

export function headOf(windows: TrayLimitWindow[]): TrayLimitWindow {
  return windows.find((window) => window.head) ?? windows[0];
}

export function limitTone(pct: number, accent: string) {
  if (pct <= 15) {
    return {
      bar: "var(--color-danger)",
      value: "var(--color-danger-strong)",
    };
  }
  if (pct <= 35) {
    return { bar: "var(--color-warning)", value: "#8b6b36" };
  }
  return { bar: accent, value: "var(--color-text)" };
}
