import Dot from "$lib/components/ui/Dot";
import Mascot from "$lib/components/ui/Mascot";
import { useWidth } from "$lib/react-utils";
import { useState } from "react";
import BellIcon from "../../icons/BellIcon";
import PowerIcon from "../../icons/PowerIcon";
import SlidersIcon from "../../icons/SlidersIcon";
import DateRangePicker from "../period/DateRangePicker";

export default function Header({
  online = true,
  activeAgents,
  tokensToday,
  onOpenSettings,
  onQuit,
}: {
  online?: boolean;
  activeAgents: number;
  tokensToday: string;
  onOpenSettings?: () => void;
  onQuit?: () => void;
}) {
  const [headerWidth, setHeaderWidth] = useState(0);
  const measureHeader = useWidth(setHeaderWidth);
  const compact = headerWidth > 0 && headerWidth < 950;

  return (
    <>
      <header className="mx-auto w-full flex-none max-w-[var(--page-max-width)] p-[18px_32px_12px]">
        <div
          ref={measureHeader}
          className="bg-surface border-border grid items-center border grid-cols-[1fr_auto_1fr] gap-[20px] rounded-[16px] p-[8px_14px]"
        >
          <div className="flex items-center gap-[12px]">
            <Mascot pose="view-front" height={42} />
            <div className="flex flex-col gap-[4px]">
              <div className="text-text font-bold text-[18px] tracking-[-0.01em]">
                Pulsemetry
              </div>
              <div className="text-text-secondary flex items-center gap-[6px] text-[12px]">
                <Dot
                  color={
                    online ? "var(--color-success)" : "var(--color-inactive)"
                  }
                />
                {online ? "모니터링 중" : "연결 끊김"}
              </div>
            </div>
          </div>

          <div className="bg-bg border-border flex items-center border whitespace-nowrap gap-[9px] rounded-[999px] p-[8px_16px] text-[13.5px]">
            <Dot
              size={8}
              color={online ? "var(--color-success)" : "var(--color-inactive)"}
            />
            <span className="font-semibold">
              {activeAgents}
              {compact ? " agents" : " agents active"}
            </span>
            <span className="text-text-muted">•</span>
            <span className="text-text-secondary">
              {tokensToday}
              {compact ? "" : " tokens today"}
            </span>
          </div>
          <div className="text-text flex items-center justify-end gap-[14px]">
            <DateRangePicker />
            <span
              title="알림 (준비 중)"
              className="text-text-secondary [cursor:default]"
            >
              <BellIcon size={19} strokeWidth={1.7} />
            </span>
            <button
              type="button"
              title="설정"
              onClick={onOpenSettings}
              className="text-text flex cursor-pointer items-center justify-center border-none bg-transparent p-[0]"
            >
              <SlidersIcon
                size={19}
                strokeWidth={1.7}
                knobFill="var(--color-surface)"
              />
            </button>
            <button
              type="button"
              title="종료"
              onClick={onQuit}
              className="text-text-secondary hover:text-danger-strong flex cursor-pointer items-center justify-center border-none bg-transparent transition-colors duration-[120ms] ease-in-out p-[0]"
            >
              <PowerIcon size={19} strokeWidth={1.7} />
            </button>
          </div>
        </div>
      </header>
    </>
  );
}
