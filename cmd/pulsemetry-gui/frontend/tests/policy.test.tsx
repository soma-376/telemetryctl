import {
  render,
  renderHook,
  screen,
  within,
  act,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type { PolicySnapshot } from "../src/lib/bindings";
import { makeQueryClient } from "../src/lib/query/client";
import { usePolicyQuery } from "../src/lib/query/policy";
import CollectionPolicy from "../src/pages/settings/CollectionPolicy";

const api = vi.hoisted(() => ({ Policy: vi.fn() }));

vi.mock("$lib/bindings", () => ({ Dashboard: api }));
const snapshot: PolicySnapshot = {
  applied_revision: 3,
  expected_revision: 4,
  applied_at: "2026-10-29T00:00:00Z",
  last_checked_at: "2026-10-29T01:00:00Z",
  status: "pending",
  signals: { logs: true, metrics: false, traces: false },
  privacy: {
    collect_user_prompts: false,
    collect_assistant_responses: false,
    collect_tool_details: true,
    collect_tool_content: false,
    collect_user_email: false,
    collect_raw_api_bodies: false,
  },
};

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

it("실제 적용 판과 대기 판, manifest의 허용 항목을 표시한다", () => {
  render(<CollectionPolicy snapshot={snapshot} unavailable={false} />);
  expect(screen.getByText("적용 정책 v3")).toBeTruthy();
  expect(screen.getByText("정책 업데이트 대기")).toBeTruthy();
  expect(screen.getByText("마지막 확인한 서버 정책 v4")).toBeTruthy();
  const row = screen.getByText("사용자 프롬프트").parentElement!;

  expect(within(row).getByText("제외")).toBeTruthy();
  expect(screen.queryByText(/Acme|policy.yaml|40초 전/)).toBeNull();
});

it.each(["error", "stale", "unknown", "ahead", "checking", "disabled"])(
  "%s는 최신 적용으로 표시하지 않는다",
  (status) => {
    render(
      <CollectionPolicy
        snapshot={{ ...snapshot, status }}
        unavailable={false}
      />,
    );

    expect(screen.getByText("적용 정책 v3")).toBeTruthy();
    expect(screen.queryByText("최신 정책 적용 완료")).toBeNull();
    expect(screen.queryByText("정책 업데이트 대기")).toBeNull();
  },
);

it("서버 판이 null이거나 데몬이 끊기면 캐시를 최신으로 표시하지 않는다", () => {
  const view = render(
    <CollectionPolicy
      snapshot={{ ...snapshot, expected_revision: null, status: "unknown" }}
      unavailable={false}
    />,
  );

  expect(screen.queryByText(/마지막 확인한 서버 정책/)).toBeNull();

  view.rerender(
    <CollectionPolicy
      snapshot={{ ...snapshot, status: "current" }}
      unavailable
    />,
  );

  expect(screen.getByText(/데몬에 연결할 수 없어/)).toBeTruthy();
  expect(screen.queryByText("최신 정책 적용 완료")).toBeNull();
});

it("설정을 열 때와 열린 동안만 정책을 다시 조회한다", async () => {
  vi.useFakeTimers();
  api.Policy.mockResolvedValue(snapshot);
  const client = makeQueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const view = renderHook(({ open }) => usePolicyQuery(open), {
    wrapper,
    initialProps: { open: false },
  });

  expect(api.Policy).not.toHaveBeenCalled();

  await act(async () => {
    view.rerender({ open: true });
  });

  expect(api.Policy).toHaveBeenCalledTimes(1);
  api.Policy.mockResolvedValue({ ...snapshot, expected_revision: 5 });

  await act(async () => {
    await vi.advanceTimersByTimeAsync(10_010);
  });

  expect(view.result.current.data?.expected_revision).toBe(5);
  view.rerender({ open: false });
  const count = api.Policy.mock.calls.length;

  await act(async () => {
    await vi.advanceTimersByTimeAsync(20_000);
  });

  expect(api.Policy).toHaveBeenCalledTimes(count);
  view.unmount();
  client.clear();
});
