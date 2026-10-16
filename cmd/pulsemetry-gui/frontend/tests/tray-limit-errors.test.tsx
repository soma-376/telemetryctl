import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { TraySnapshot } from "../src/lib/bindings";
import { toTrayView } from "../src/pages/tray/adapter";
import VendorLimits from "../src/pages/tray/components/VendorLimits";

vi.mock("$lib/bindings", () => ({
  LimitState: { StateAvailable: "available" },
}));

function snapshot(reason: string, hasLastGood = false): TraySnapshot {
  return {
    limits: [
      {
        vendor: "claude_code",
        state: reason ? "unavailable" : "available",
        reason,
        detail: "화면에 노출하면 안 되는 원본 오류",
        plan: "max",
        observed_at: "2026-09-14T03:04:05Z",
        windows: hasLastGood
          ? [
              {
                period: "five_hour",
                label: "five_hour",
                window_minutes: 300,
                used_ratio: 0.2,
                resets_at: "",
                resets_in_seconds: 0,
              },
            ]
          : [],
      },
    ],
    limits_observed_at: "2026-09-15T03:00:00Z",
    recent_sessions: [],
    monitoring: {},
  } as TraySnapshot;
}

it.each([
  ["network_error", "통신 오류로 사용량을 조회하지 못했습니다"],
  ["dns_error", "공급자 서버 주소를 확인하지 못했습니다"],
  ["tls_error", "공급자 서버와의 보안 연결을 확인하지 못했습니다"],
  ["request_timeout", "사용량 조회 시간이 초과됐습니다"],
  ["auth_rejected", "현재 자격증명으로 조회하지 못했습니다"],
  ["access_denied", "공급자가 사용량 조회를 허용하지 않았습니다"],
  ["rate_limited", "조회 요청이 제한됐습니다"],
  ["upstream_status", "공급자 응답 오류로 사용량을 조회하지 못했습니다"],
  ["response_unrecognized", "사용량 응답을 해석하지 못했습니다"],
])("%s를 로그아웃으로 표시하지 않는다", (reason, message) => {
  const view = toTrayView(snapshot(reason));

  render(
    <VendorLimits vendors={view.vendors} unavailable={view.unavailable} />,
  );

  expect(screen.getByText(message, { exact: false })).toBeTruthy();
  expect(screen.queryByText(/로그인|토큰이 만료/)).toBeNull();
  expect(screen.queryByText(/원본 오류/)).toBeNull();
});

it("통신 실패 시 이전 사용량과 해당 벤더의 성공 시각을 함께 표시하고 복구하면 안내를 지운다", () => {
  const failed = toTrayView(snapshot("network_error", true));
  const { rerender } = render(
    <VendorLimits vendors={failed.vendors} unavailable={failed.unavailable} />,
  );

  expect(screen.getByText("80%")).toBeTruthy();
  expect(screen.getByRole("status").textContent).toContain("통신 오류");
  expect(screen.getByRole("status").textContent).toContain("마지막 성공 결과");
  expect(screen.getByRole("status").textContent).toContain("2026. 9. 14.");
  expect(screen.getByRole("status").textContent).not.toContain("2026. 9. 15.");

  const recovered = toTrayView(snapshot("", true));

  rerender(
    <VendorLimits
      vendors={recovered.vendors}
      unavailable={recovered.unavailable}
    />,
  );

  expect(screen.getByText("80%")).toBeTruthy();
  expect(screen.queryByRole("status")).toBeNull();
});

it("만료 시각이 지난 경우에도 먼저 도구 실행과 새로고침을 안내한다", () => {
  const view = toTrayView(snapshot("token_expired"));

  render(
    <VendorLimits vendors={view.vendors} unavailable={view.unavailable} />,
  );

  expect(screen.getByText(/토큰이 만료됐습니다/)).toBeTruthy();
  expect(screen.getByText(/해당 도구를 실행한 뒤 새로고침/)).toBeTruthy();
  expect(screen.queryByText(/다시 로그인/)).toBeNull();
});
