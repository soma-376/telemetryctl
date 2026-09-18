import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { makeQueryClient } from "../src/lib/query/client";
import Activity from "../src/pages/activity/Activity";
import DateRangePicker from "../src/lib/components/period/DateRangePicker";
import { period, todayRange, usePeriod } from "../src/lib/domain/period";
import { useActivityQuery } from "../src/lib/query/activity";
import { useTrayQuery, useTrayRefreshMutation } from "../src/lib/query/tray";
import App from "../src/App";
import TrayQuickView from "../src/pages/tray/TrayQuickView";

const api = vi.hoisted(() => ({
  Activity: vi.fn(),
  ActivitySession: vi.fn(),
  Tray: vi.fn(),
  RefreshTray: vi.fn(),
  Updates: vi.fn(),
  GetAppInfo: vi.fn(),
  IsTrayVisible: vi.fn(),
  listeners: new Map<string, () => void>(),
}));

vi.mock("$lib/bindings", () => ({
  Dashboard: api,
  App: api,
  TrayState: {
    StateMonitoring: "monitoring",
    StatePaused: "paused",
    StateNotInstalled: "not_installed",
  },
  LimitState: { StateAvailable: "available" },
}));

vi.mock("@wailsio/runtime", () => ({
  Window: { Hide: vi.fn() },
  Events: {
    On: (name: string, callback: () => void) => {
      api.listeners.set(name, callback);

      return () => api.listeners.delete(name);
    },
  },
}));

const row = (id: number) => ({
  id,
  vendor: "codex",
  status: "completed",
  title: `session-${id}`,
  project_name: "demo",
  workspace_path: "/demo",
  started_at: 1789139543,
  ended_at: 1789139545,
  duration_ms: 2000,
  input_tokens: 10,
  output_tokens: 2,
  api_requests: 1,
  reported_cost_calls: 0,
  cost_usd: 0,
});
const detail = (id: number) => ({
  detail: { found: true, session: row(id), files: [], tools: [] },
  metrics: {
    totals: {
      tool_calls: 0,
      tokens: {
        input_tokens: 10,
        output_tokens: 2,
        cache_read_tokens: 0,
        cache_write_tokens: 0,
      },
      cost: { reported_calls: 0, estimated_calls: 0, total: { usd: 0 } },
      cache_savings: { available_calls: 0 },
    },
    turns: [],
  },
  classification: { work_type: "unknown", turns: [] },
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });

  return { promise, resolve };
}

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
  api.GetAppInfo.mockResolvedValue({ name: "Pulsemetry", version: "test" });
  api.IsTrayVisible.mockResolvedValue(false);

  api.Updates.mockResolvedValue({
    status: "disabled",
    current_version: "test",
    latest_version: "",
    update_available: null,
    last_attempt_at: "",
    last_success_at: "",
  });

  api.Activity.mockResolvedValue({ rows: [row(1), row(2)], has_more: false });

  api.ActivitySession.mockImplementation((id: number) =>
    Promise.resolve(detail(id)),
  );
});

afterEach(() => vi.useRealTimers());

describe("React 전환 후 화면과 조회 수명주기", () => {
  it("메인 화면에서 설정과 종료 대화상자를 열고 키보드로 닫는다", async () => {
    const view = render(<App />);

    fireEvent.click(screen.getByTitle("설정"));
    await screen.findByText(/^Pulsemetrytest$/);
    expect(api.GetAppInfo).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(screen.getByTitle("종료"));
    const dialog = screen.getByRole("alertdialog");

    expect(within(dialog).getByRole("button", { name: "취소" })).toBe(
      document.activeElement,
    );

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("alertdialog")).toBeNull();
    view.unmount();
    expect(api.listeners.size).toBe(0);
  });

  it("트레이 오류 상태를 표시하고 창 이벤트 구독을 정리한다", async () => {
    api.Tray.mockRejectedValue(new Error("daemon unavailable"));
    const { wrapper, client } = provider();
    const view = render(<TrayQuickView />, { wrapper });

    await screen.findByText("사용량을 불러오지 못했습니다");
    expect(screen.getByText("Error: daemon unavailable")).toBeTruthy();
    expect(api.listeners.has("tray:shown")).toBe(true);
    view.unmount();
    expect(api.listeners.size).toBe(0);
    client.clear();
  });

  it("빠르게 선택을 바꿔도 이전 상세 응답이 현재 세션을 덮지 않는다", async () => {
    const first = deferred<ReturnType<typeof detail>>();

    api.ActivitySession.mockImplementation((id: number) =>
      id === 1 ? first.promise : Promise.resolve(detail(id)),
    );

    const { wrapper, client } = provider();
    const view = render(<Activity />, { wrapper });

    fireEvent.click(await screen.findByText("session-1"));
    await waitFor(() => expect(api.ActivitySession).toHaveBeenCalledWith(1));
    fireEvent.click(within(view.container).getByText("session-2"));
    await waitFor(() => expect(api.ActivitySession).toHaveBeenCalledWith(2));
    await act(async () => first.resolve(detail(1)));
    const overlay = document.querySelector(".session-overlay")!;

    expect(overlay.textContent).toContain("session-2");
    expect(overlay.textContent).not.toContain("session-1");
    fireEvent.keyDown(window, { key: "Escape" });

    await waitFor(() =>
      expect(document.querySelector(".session-overlay")).toBeNull(),
    );

    view.unmount();
    expect(api.listeners.size).toBe(0);
    client.clear();
  });

  it("검색 입력을 지연 반영하고 필터가 바뀌면 이전 목록을 비운다", async () => {
    const { wrapper, client } = provider();

    render(<Activity />, { wrapper });
    await screen.findByText("session-1");
    const pending = deferred<unknown>();

    api.Activity.mockReturnValue(pending.promise);

    fireEvent.change(screen.getByRole("searchbox"), {
      target: { value: "new search" },
    });

    expect(api.Activity).toHaveBeenCalledTimes(1);

    await waitFor(() =>
      expect(api.Activity).toHaveBeenLastCalledWith(
        expect.objectContaining({ text: "new search" }),
      ),
    );

    expect(screen.queryByText("session-1")).toBeNull();
    await act(async () => pending.resolve({ rows: [], has_more: false }));
    client.clear();
  });

  it("달력 초안은 적용 전까지 공유 기간을 바꾸지 않고 적용 후 구독 화면에 반영한다", () => {
    period.value = { start: "2026-08-01", end: "2026-08-02" };

    function PeriodText() {
      const value = usePeriod();

      return <output>{value.value.start}</output>;
    }

    const view = render(
      <>
        <DateRangePicker />
        <PeriodText />
      </>,
    );

    fireEvent.click(view.container.querySelector("button")!);
    fireEvent.click(screen.getByRole("button", { name: "오늘", exact: true }));
    expect(screen.getByRole("status").textContent).toBe("2026-08-01");
    fireEvent.click(screen.getByRole("button", { name: /적용/ }));
    expect(screen.getByRole("status").textContent).toBe(todayRange().start);
    expect(screen.queryByRole("button", { name: "달력 닫기" })).toBeNull();
  });

  it("숨겨진 Activity는 폴링을 멈추고 다시 보이면 재개한다", async () => {
    vi.useFakeTimers();
    const { wrapper, client } = provider();
    const query = {
      since: 0,
      until: 1,
      vendors: [],
      projects: [],
      status: [],
      text: "",
      limit: 50,
      cursor: { id: 0, running: false, sort_at: 0 },
    };
    const view = renderHook(({ visible }) => useActivityQuery(query, visible), {
      wrapper,
      initialProps: { visible: true },
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });

    expect(api.Activity).toHaveBeenCalledTimes(1);
    view.rerender({ visible: false });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });

    expect(api.Activity).toHaveBeenCalledTimes(1);
    view.rerender({ visible: true });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_001);
    });

    expect(api.Activity.mock.calls.length).toBeGreaterThan(1);
    view.unmount();
    client.clear();
  });

  it("트레이 수동 갱신 후 늦은 일반 조회가 최신 캐시를 덮지 않는다", async () => {
    const old = deferred<unknown>();

    api.Tray.mockReturnValue(old.promise);
    api.RefreshTray.mockResolvedValue({ marker: "new" });
    const { wrapper, client } = provider();
    const view = renderHook(
      () => ({
        query: useTrayQuery({ tz: "Asia/Seoul", recent_limit: 5 }, false),
        mutation: useTrayRefreshMutation({ tz: "Asia/Seoul", recent_limit: 5 }),
      }),
      { wrapper },
    );

    await act(async () => {
      await view.result.current.mutation.mutateAsync();
    });

    await act(async () => old.resolve({ marker: "old" }));
    expect(client.getQueryData(["tray"])).toEqual({ marker: "new" });
    view.unmount();
    client.clear();
  });
});
