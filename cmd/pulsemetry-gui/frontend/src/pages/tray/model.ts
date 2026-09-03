import type { NonEmpty } from "$lib/utils/array";
import type { TrayLimitWindow } from "./types";

// 대표 창. 입력이 비어 있지 않다고 타입이 보장하므로 [0] 폴백이 undefined 가 될 수 없다.
export function headOf(windows: NonEmpty<TrayLimitWindow>): TrayLimitWindow {
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
