import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import Uninstall, { UninstallSetting } from "../src/pages/settings/Uninstall";

const api = vi.hoisted(() => ({
  Preview: vi.fn(),
  Execute: vi.fn(),
  Finish: vi.fn(),
  OpenUninstaller: vi.fn(),
  Quit: vi.fn(),
}));

vi.mock("$lib/bindings", () => ({ Removal: api, App: api }));

beforeEach(() => {
  vi.clearAllMocks();
  api.Preview.mockResolvedValue({ installed: true, warnings: [] });
  api.Execute.mockResolvedValue({ success: true, warnings: [], preserved: [] });
});

it("제거 화면을 열기만 해서는 삭제하지 않고 기록 삭제는 기본 해제한다", async () => {
  render(<Uninstall />);

  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "Pulsemetry 제거" })
        .hasAttribute("disabled"),
    ).toBe(false),
  );

  expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(
    false,
  );

  expect(api.Execute).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Pulsemetry 제거" }));
  await screen.findByRole("button", { name: "닫고 마무리" });
  expect(api.Execute).toHaveBeenCalledWith(false);
  expect(api.Finish).not.toHaveBeenCalled();
});

it("명시적으로 선택한 기록 삭제를 전달하고 실패하면 재시도를 허용한다", async () => {
  api.Execute.mockResolvedValueOnce({
    success: false,
    error: "데몬 종료 실패",
    warnings: [],
  });

  render(<Uninstall />);
  fireEvent.click(screen.getByRole("checkbox"));

  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "Pulsemetry 제거" })
        .hasAttribute("disabled"),
    ).toBe(false),
  );

  fireEvent.click(screen.getByRole("button", { name: "Pulsemetry 제거" }));

  expect((await screen.findByRole("alert")).textContent).toContain(
    "데몬 종료 실패",
  );

  expect(api.Execute).toHaveBeenCalledWith(true);
  expect(api.Finish).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Pulsemetry 제거" }));
  await screen.findByRole("button", { name: "닫고 마무리" });
  expect(api.Execute).toHaveBeenCalledTimes(2);
});

it("제거 도구를 실행할 수 없으면 성공으로 숨기지 않는다", async () => {
  api.OpenUninstaller.mockRejectedValue(new Error("제거 도구 없음"));
  render(<UninstallSetting />);
  fireEvent.click(screen.getByRole("button", { name: "제거 도구 열기" }));

  expect((await screen.findByRole("alert")).textContent).toContain(
    "제거 도구 없음",
  );

  expect(api.Quit).not.toHaveBeenCalled();
});
