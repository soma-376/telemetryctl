import {
  act,
  render,
  renderHook,
  screen,
  within,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { UpdateSnapshot } from "../src/lib/bindings";
import { makeQueryClient } from "../src/lib/query/client";
import { useUpdatesQuery } from "../src/lib/query/updates";
import DaemonUpdates from "../src/pages/settings/DaemonUpdates";
import SettingsModal from "../src/pages/settings/SettingsModal";

const api = vi.hoisted(() => ({ Updates: vi.fn(), GetAppInfo: vi.fn() }));

vi.mock("$lib/bindings", () => ({ Dashboard: api, App: api }));
vi.mock("@wailsio/runtime", () => ({ Window: { Hide: vi.fn() } }));

function snapshot(overrides: Partial<UpdateSnapshot> = {}): UpdateSnapshot {
  return {
    status: "ready",
    current_version: "0.1.0",
    latest_version: "0.2.0",
    update_available: true,
    last_attempt_at: "2026-09-18T00:00:00Z",
    last_success_at: "2026-09-18T00:00:00Z",
    ...overrides,
  };
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
  vi.resetAllMocks();
  api.Updates.mockResolvedValue(snapshot());

  api.GetAppInfo.mockResolvedValue({
    name: "Pulsemetry",
    version: "development",
  });
});

afterEach(() => vi.useRealTimers());

describe("데몬 업데이트 표시", () => {
  it.each([
    ["checking", "업데이트 확인 중"],
    ["disabled", "등록 후 확인할 수 있어요"],
    ["unsupported", "서버가 업데이트 확인을 지원하지 않아요"],
    ["error", "업데이트 확인에 실패했어요"],
  ])("%s를 최신 버전으로 오인하지 않는다", (status, message) => {
    render(
      <DaemonUpdates
        snapshot={snapshot({
          status,
          latest_version: "",
          update_available: null,
          last_success_at: "",
        })}
        unavailable={false}
      />,
    );

    expect(screen.getByRole("status").textContent).toBe(message);
    expect(screen.getByText("데몬 버전 0.1.0")).toBeTruthy();
    expect(screen.queryByText(/마지막 확인한 최신 버전/)).toBeNull();
    expect(screen.queryByText(/마지막 성공 확인/)).toBeNull();
  });

  it.each([
    [true, "업데이트 가능"],
    [false, "최신 버전이에요"],
  ])("성공한 확인 결과와 관측 시각을 표시한다 (%s)", (available, message) => {
    render(
      <DaemonUpdates
        snapshot={snapshot({ update_available: available })}
        unavailable={false}
      />,
    );

    expect(screen.getByRole("status").textContent).toBe(message);
    expect(screen.getByText("마지막 확인한 최신 버전 0.2.0")).toBeTruthy();

    expect(
      screen.getByText(/마지막 성공 확인/).querySelector("time")?.dateTime,
    ).toBe("2026-09-18T00:00:00Z");
  });

  it("성공한 확인이 없으면 최신 버전이라고 표시하지 않는다", () => {
    render(
      <DaemonUpdates
        snapshot={snapshot({ update_available: null, last_success_at: "" })}
        unavailable={false}
      />,
    );

    expect(screen.getByRole("status").textContent).toBe(
      "아직 확인하지 않았어요",
    );

    expect(screen.queryByText(/마지막 확인한 최신 버전/)).toBeNull();
  });

  it.each(["checking", "unsupported", "error"])(
    "%s일 때 마지막 성공 결과를 과거 확인값으로 표시한다",
    (status) => {
      render(
        <DaemonUpdates snapshot={snapshot({ status })} unavailable={false} />,
      );

      expect(screen.getByRole("status").textContent).not.toBe(
        "최신 버전이에요",
      );

      expect(screen.getByRole("status").textContent).not.toBe("업데이트 가능");
      expect(screen.getByText("마지막 확인한 최신 버전 0.2.0")).toBeTruthy();
      expect(screen.getByText(/마지막 성공 확인/)).toBeTruthy();
    },
  );

  it("연결 실패를 알리면서 마지막 정상 스냅샷을 유지한다", () => {
    render(<DaemonUpdates snapshot={snapshot()} unavailable />);

    expect(screen.getByRole("status").textContent).toBe(
      "데몬에 연결할 수 없음",
    );

    expect(screen.getByText("데몬 버전 0.1.0")).toBeTruthy();
    expect(screen.getByText("마지막 확인한 최신 버전 0.2.0")).toBeTruthy();
  });

  it("첫 연결 실패는 버전을 지어내지 않는다", () => {
    render(<DaemonUpdates unavailable />);

    expect(screen.getByRole("status").textContent).toBe(
      "데몬에 연결할 수 없음",
    );

    expect(screen.queryByText(/데몬 버전/)).toBeNull();
  });

  it("설정은 데몬 버전을 표시하고 자동 설치 토글을 제공하지 않는다", async () => {
    const { wrapper, client } = provider();
    const view = render(<SettingsModal open onClose={() => {}} />, { wrapper });
    const updates = screen.getByRole("region", { name: "데몬 업데이트" });

    expect(await within(updates).findByText("데몬 버전 0.1.0")).toBeTruthy();
    expect(screen.queryByRole("switch", { name: "자동 업데이트" })).toBeNull();
    expect(within(updates).queryByRole("button")).toBeNull();
    expect(screen.getByText(/development/)).toBeTruthy();
    view.unmount();
    client.clear();
  });
});

describe("업데이트 스냅샷 조회 수명", () => {
  it("닫혀 있으면 조회하지 않고 열기·60초·다시 열기에만 로컬 상태를 읽는다", async () => {
    vi.useFakeTimers();
    const { wrapper, client } = provider();
    const view = renderHook(({ open }) => useUpdatesQuery(open), {
      wrapper,
      initialProps: { open: false },
    });

    await act(async () => vi.advanceTimersByTimeAsync(60_000));
    expect(api.Updates).not.toHaveBeenCalled();
    view.rerender({ open: true });
    await act(async () => vi.advanceTimersByTimeAsync(1));
    expect(api.Updates).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTimeAsync(60_000));
    expect(api.Updates).toHaveBeenCalledTimes(2);
    view.rerender({ open: false });
    await act(async () => vi.advanceTimersByTimeAsync(60_000));
    expect(api.Updates).toHaveBeenCalledTimes(2);
    view.rerender({ open: true });
    await act(async () => vi.advanceTimersByTimeAsync(1));
    expect(api.Updates).toHaveBeenCalledTimes(3);
    view.rerender({ open: false });
    view.rerender({ open: true });
    await act(async () => vi.advanceTimersByTimeAsync(1));
    expect(api.Updates).toHaveBeenCalledTimes(4);
    view.unmount();
    client.clear();
  });

  it("데몬 연결 실패 후 재시도 없이 마지막 정상값을 보존한다", async () => {
    vi.useFakeTimers();
    const { wrapper, client } = provider();
    const view = renderHook(
      () => {
        const query = useUpdatesQuery(true);

        return { data: query.data, isError: query.isError };
      },
      { wrapper },
    );

    await act(async () => vi.advanceTimersByTimeAsync(1));
    expect(view.result.current.data).toEqual(snapshot());
    api.Updates.mockRejectedValue(new Error("daemon unavailable"));
    await act(async () => vi.advanceTimersByTimeAsync(60_001));
    expect(view.result.current.isError).toBe(true);
    expect(view.result.current.data).toEqual(snapshot());
    expect(api.Updates).toHaveBeenCalledTimes(2);
    await act(async () => vi.advanceTimersByTimeAsync(10_000));
    expect(api.Updates).toHaveBeenCalledTimes(2);
    view.unmount();
    client.clear();
  });
});
