import { act, render } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import TrayQuickView from "../src/pages/tray/TrayQuickView";

const api = vi.hoisted(() => ({
  query: vi.fn(),
  refetch: vi.fn(),
  isTrayVisible: vi.fn(),
  on: vi.fn(),
  off: vi.fn(),
  bindRetry: vi.fn(),
  offRetry: vi.fn(),
  listeners: new Map<string, () => void>(),
  dataUpdatedAt: 0,
}));

vi.mock("$lib/bindings", () => ({
  TrayState: { StateNotInstalled: "not_installed" },
  LimitState: { StateAvailable: "available" },
}));

vi.mock("$lib/query/tray", () => ({
  useTrayQuery: api.query,
  useTrayRefreshMutation: () => ({ error: null, mutateAsync: vi.fn() }),
}));

vi.mock("$lib/ipc/app", () => ({
  isTrayVisible: api.isTrayVisible,
  hideCurrentWindow: vi.fn(),
  openMainWindow: vi.fn(),
  openMainSettings: vi.fn(),
  quitApp: vi.fn(),
}));

vi.mock("$lib/domain/reconnect", () => ({
  useReconnect: () => ({ down: false }),
  bindRetry: api.bindRetry,
  noteFailure: vi.fn(),
  noteSuccess: vi.fn(),
}));

vi.mock("@wailsio/runtime", () => ({ Events: { On: api.on } }));

function deferredVisibility() {
  let resolve!: (value: boolean) => void;
  const promise = new Promise<boolean>((done) => {
    resolve = done;
  });

  return { promise, resolve };
}

beforeEach(() => {
  vi.resetAllMocks();
  api.listeners.clear();
  api.dataUpdatedAt = 0;
  api.isTrayVisible.mockResolvedValue(false);
  api.refetch.mockResolvedValue({});
  api.bindRetry.mockReturnValue(api.offRetry);

  api.on.mockImplementation((name: string, listener: () => void) => {
    api.listeners.set(name, listener);

    return () => {
      api.off(name);
      api.listeners.delete(name);
    };
  });

  // Query 결과 객체는 매번 새로 생겨도 refetch 함수의 정체성은 유지된다.
  api.query.mockImplementation(() => ({
    refetch: api.refetch,
    dataUpdatedAt: api.dataUpdatedAt,
    errorUpdatedAt: 0,
    data: undefined,
    error: null,
    isPending: true,
    isFetching: false,
  }));
});

it("조회 결과가 바뀌어도 창 이벤트와 재연결 콜백을 다시 구독하지 않는다", async () => {
  const view = render(<TrayQuickView />);

  await act(async () => {});
  expect(api.on).toHaveBeenCalledTimes(2);
  expect(api.bindRetry).toHaveBeenCalledTimes(1);
  expect(api.isTrayVisible).toHaveBeenCalledTimes(1);
  api.dataUpdatedAt = 1;
  view.rerender(<TrayQuickView />);
  expect(api.on).toHaveBeenCalledTimes(2);
  expect(api.bindRetry).toHaveBeenCalledTimes(1);
  expect(api.off).not.toHaveBeenCalled();

  await act(async () => api.listeners.get("tray:shown")?.());
  expect(api.refetch).toHaveBeenCalledWith({ cancelRefetch: false });
  expect(api.isTrayVisible).toHaveBeenCalledTimes(2);
  expect(api.on).toHaveBeenCalledTimes(2);
  view.unmount();
  expect(api.off).toHaveBeenCalledTimes(2);
  expect(api.offRetry).toHaveBeenCalledTimes(1);
  expect(api.listeners.size).toBe(0);
});

it("늦게 완료된 초기 조회와 이전 표시 응답이 최신 가시성을 덮지 않는다", async () => {
  const initial = deferredVisibility();
  const shown = deferredVisibility();
  const staleShown = deferredVisibility();
  const hidden = deferredVisibility();

  api.isTrayVisible
    .mockReturnValueOnce(initial.promise)
    .mockReturnValueOnce(shown.promise)
    .mockReturnValueOnce(staleShown.promise)
    .mockReturnValueOnce(hidden.promise);

  const view = render(<TrayQuickView />);

  await act(async () => api.listeners.get("tray:shown")?.());
  await act(async () => shown.resolve(true));
  expect(api.query.mock.lastCall?.[1]).toBe(true);
  await act(async () => initial.resolve(false));
  expect(api.query.mock.lastCall?.[1]).toBe(true);
  await act(async () => api.listeners.get("tray:shown")?.());
  await act(async () => api.listeners.get("tray:hidden")?.());
  await act(async () => hidden.resolve(false));
  expect(api.query.mock.lastCall?.[1]).toBe(false);
  await act(async () => staleShown.resolve(true));
  expect(api.query.mock.lastCall?.[1]).toBe(false);
  view.unmount();
});
