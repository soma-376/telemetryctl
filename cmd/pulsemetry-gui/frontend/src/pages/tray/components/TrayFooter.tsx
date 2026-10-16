import PowerIcon from "$lib/icons/PowerIcon";
import SlidersIcon from "$lib/icons/SlidersIcon";
import { openMainWindow } from "$lib/ipc/app";
import { cssStyle } from "$lib/react-utils";

export default function TrayFooter({
  settingsOpen,
  onSettings,
  onRequestQuit,
}: {
  settingsOpen: boolean;
  onSettings: () => void;
  onRequestQuit: () => void;
}) {
  return (
    <>
      <footer className="bg-surface flex flex-none items-center gap-[8px] p-[10px_14px] [border-top:1px_solid_#ede7de]">
        <button
          type="button"
          onClick={openMainWindow}
          className="bg-accent hover:bg-accent-hover flex cursor-pointer items-center justify-center border-none font-semibold whitespace-nowrap transition-colors duration-[120ms] ease-in-out flex-1 gap-[8px] rounded-[9px] p-[10px] text-[12.5px] text-[var(--color-surface)]"
        >
          Pulsemetry 열기
        </button>
        <button
          type="button"
          title="트레이 설정"
          aria-expanded={settingsOpen}
          onClick={onSettings}
          style={cssStyle(
            "border-color:" +
              String(
                settingsOpen
                  ? "var(--color-border-strong)"
                  : "var(--color-border)",
              ) +
              ";background:" +
              String(
                settingsOpen ? "var(--color-surface-hover)" : "transparent",
              ),
          )}
          className="text-text-secondary hover:border-border-strong hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border transition-colors duration-[120ms] ease-in-out w-[34px] h-[34px] rounded-[9px]"
        >
          <SlidersIcon
            size={15}
            strokeWidth={1.8}
            knobFill="var(--color-surface)"
          />
        </button>
        <button
          type="button"
          title="종료"
          onClick={onRequestQuit}
          className="border-border text-text-secondary hover:border-border-strong hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border bg-transparent transition-colors duration-[120ms] ease-in-out w-[34px] h-[34px] rounded-[9px]"
        >
          <PowerIcon size={15} strokeWidth={1.8} />
        </button>
      </footer>
    </>
  );
}
