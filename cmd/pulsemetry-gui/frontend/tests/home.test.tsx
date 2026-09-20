import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { HomeSnapshot } from "../src/lib/bindings";
import { makeQueryClient } from "../src/lib/query/client";
import { useHomeQuery } from "../src/lib/query/home";
import { period } from "../src/lib/domain/period";
import Home from "../src/pages/home/Home";
import { homeView } from "../src/pages/home/adapter";
import { homeFixture } from "./home-fixture";
import Header from "../src/lib/components/shell/Header";
import { todayRange } from "../src/lib/domain/period";

const api = vi.hoisted(() => ({
  Home: vi.fn(),
  listeners: new Map<string, Set<() => void>>(),
}));

vi.mock("$lib/bindings", () => ({ Dashboard: api }));

vi.mock("@wailsio/runtime", () => ({
  Events: {
    On: (name: string, callback: () => void) => {
      const listeners = api.listeners.get(name) ?? new Set();

      listeners.add(callback);
      api.listeners.set(name, listeners);

      return () => listeners.delete(callback);
    },
  },
}));

function provider() {
  const client = makeQueryClient();

  return {
    client,
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  api.listeners.clear();
  period.value = { start: "2026-09-20", end: "2026-09-20" };
  api.Home.mockResolvedValue(homeFixture());
});

afterEach(() => vi.useRealTimers());

it("관측값과 세션 생애 합계를 구분하고 캐시·추론을 중복 가산하지 않는다", () => {
  const view = homeView(homeFixture());

  expect(view.hero.totalTokens).toBe("1.2k");
  expect(view.hero.bars.reduce((sum, b) => sum + b.totalValue, 0)).toBe(1200);
  expect(view.hero.avgValue).toBe("1.2k");
  expect(view.hero.totalTime).toBe("2m");
  expect(view.hero.totalCost).toBe("$1.25 이상");
  expect(view.hero.costNote).toContain("1건");
  expect(view.vendors).toHaveLength(1);
  expect(view.vendors[0]?.share).toBe("100%");
  expect(view.vendors[0]?.topModel).toBe("gpt-test · 1.2k");
  expect(view.activity.total).toBe(9);
  expect(view.activity.rows[0]?.tokens).toBe("2.4k");
  expect(view.activity.rows[0]?.time).toBe("00:00");
});

it("산정 불가와 사용량 없음은 구분한다", () => {
  const snapshot = homeFixture();

  snapshot.usage.cost.unavailable = snapshot.usage.cost.calls;
  expect(homeView(snapshot).hero.totalCost).toBe("산정 불가");
  snapshot.usage.totals.input_tokens = 0;
  snapshot.usage.totals.output_tokens = 0;
  snapshot.usage.cost.calls = 0;
  snapshot.usage.cost.unavailable = 0;
  snapshot.usage.cost.total.usd = 0;
  snapshot.usage.windows = [];
  snapshot.usage.vendors = [];
  snapshot.usage.peak.found = false;
  expect(homeView(snapshot).hero.totalCost).toBe("$0.00");
  expect(homeView(snapshot).hero.peakNote).toBe("");
  expect(homeView(snapshot).vendors).toEqual([]);
});

it("초기 실패를 빈 사용량으로 그리지 않고 갱신 실패에는 마지막 응답을 남긴다", async () => {
  const { wrapper, client } = provider();

  api.Home.mockRejectedValueOnce(new Error("offline"));
  const view = render(<Home />, { wrapper });

  await screen.findByRole("alert");
  expect(screen.queryByText("이 기간에 기록된 사용량이 없어요")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "새로고침" }));
  await screen.findByText("실제 세션");
  api.Home.mockRejectedValueOnce(new Error("offline"));
  fireEvent.click(screen.getByRole("button", { name: "새로고침" }));
  await screen.findByText(/마지막으로 받은 데이터/);
  expect(screen.getByText("실제 세션")).toBeTruthy();
  view.unmount();
  client.clear();
});

it("기간을 바꾸면 이전 값을 숨기고 늦은 응답이 새 기간을 덮지 않는다", async () => {
  const { wrapper, client } = provider();
  const view = render(<Home />, { wrapper });

  await screen.findByText("실제 세션");
  let resolve!: (value: HomeSnapshot) => void;

  api.Home.mockReturnValueOnce(
    new Promise<HomeSnapshot>((done) => {
      resolve = done;
    }),
  );

  act(() => {
    period.value = { start: "2026-09-19", end: "2026-09-19" };
  });

  await screen.findByRole("status");
  expect(screen.queryByText("실제 세션")).toBeNull();
  await waitFor(() => expect(api.Home).toHaveBeenCalledTimes(2));

  act(() => {
    period.value = { start: "2026-09-18", end: "2026-09-18" };
  });

  await screen.findByText("실제 세션");
  const old = homeFixture();

  old.recent![0]!.title = "늦은 이전 응답";

  await act(async () => {
    resolve(old);
  });

  expect(screen.queryByText("늦은 이전 응답")).toBeNull();
  view.unmount();
  client.clear();
});

it("창을 숨기면 폴링을 멈추고 다시 열면 조회한다", async () => {
  vi.useFakeTimers();
  const { wrapper, client } = provider();
  const query = { tz: "UTC", start: "2026-09-20", end: "2026-09-20" };
  const view = renderHook(() => useHomeQuery(query), { wrapper });

  await act(async () => {
    await vi.advanceTimersByTimeAsync(10);
  });

  expect(api.Home).toHaveBeenCalledTimes(1);

  act(() => {
    api.listeners.get("main:hidden")?.forEach((listener) => listener());
  });

  await act(async () => {
    await vi.advanceTimersByTimeAsync(90_000);
  });

  expect(api.Home).toHaveBeenCalledTimes(1);

  await act(async () => {
    api.listeners.get("main:shown")?.forEach((listener) => listener());
    await vi.advanceTimersByTimeAsync(10);
  });

  expect(api.Home).toHaveBeenCalledTimes(2);
  view.unmount();

  expect(
    [...api.listeners.values()].every((listeners) => listeners.size === 0),
  ).toBe(true);

  client.clear();
});

it("헤더와 홈은 오늘 조회를 공유하고 기간 변경 후에도 헤더는 오늘을 조회한다", async () => {
  const { wrapper, client } = provider();

  period.value = todayRange();
  const today = period.value.start;
  const view = render(
    <>
      <Header />
      <Home />
    </>,
    { wrapper },
  );

  await screen.findByText("실제 세션");
  expect(api.Home).toHaveBeenCalledTimes(1);

  act(() => {
    api.listeners.get("main:hidden")?.forEach((listener) => listener());
  });

  await act(async () => {
    api.listeners.get("main:shown")?.forEach((listener) => listener());
  });

  expect(api.Home).toHaveBeenCalledTimes(2);

  act(() => {
    period.value = { start: "2025-09-20", end: "2025-09-20" };
  });

  await waitFor(() => expect(api.Home).toHaveBeenCalledTimes(3));

  expect(api.Home.mock.calls[0]?.[0]).toMatchObject({
    start: today,
    end: today,
  });

  expect(api.Home.mock.calls[2]?.[0]).toMatchObject({
    start: "2025-09-20",
    end: "2025-09-20",
  });

  view.unmount();
  client.clear();
});
