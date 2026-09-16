import { act, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import TrayHeader from "../src/pages/tray/components/TrayHeader";
import { observedAtText } from "../src/pages/tray/adapter";

vi.mock("$lib/bindings", () => ({
  TrayState: { StateMonitoring: "monitoring" },
  LimitState: { StateAvailable: "available" },
}));

vi.mock("$lib/ipc/app", () => ({ hideCurrentWindow: vi.fn() }));

afterEach(() => vi.useRealTimers());

it("데몬을 다시 읽어도 벤더 관측 시각은 바뀌지 않는다", () => {
  vi.useFakeTimers();
  const observedAt = "2026-09-15T03:04:05Z";
  const label = `${observedAtText(observedAt)} 조회`;
  const { rerender } = render(<TrayHeader observedAt={observedAt} />);

  expect(screen.getByText(label)).toBeTruthy();

  // 로컬 폴링 완료 시각이 달라져도 저장된 벤더 관측 시각을 유지한다.
  rerender(<TrayHeader observedAt={observedAt} fetching />);
  expect(screen.getByText("조회 중")).toBeTruthy();
  rerender(<TrayHeader observedAt={observedAt} />);
  act(() => vi.advanceTimersByTime(60_000));
  expect(screen.getByText(label)).toBeTruthy();

  const nextObservedAt = "2026-09-15T03:10:00Z";

  rerender(<TrayHeader observedAt={nextObservedAt} />);

  expect(
    screen.getByText(`${observedAtText(nextObservedAt)} 조회`),
  ).toBeTruthy();
});
