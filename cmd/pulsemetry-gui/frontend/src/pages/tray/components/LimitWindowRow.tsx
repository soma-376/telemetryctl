import ProgressBar from "$lib/components/ui/ProgressBar";
import { cssStyle } from "$lib/react-utils";
import { limitTone } from "../model";
import type { TrayLimitWindow } from "../types";

export default function LimitWindowRow({
  window,
  accent,
}: {
  window: TrayLimitWindow;
  accent: string;
}) {
  const tone = limitTone(window.pct, accent);

  return (
    <>
      <div className="mb-[10px] animate-[rowIn_200ms_ease-out]">
        <div className="grid items-center grid-cols-[minmax(0,1fr)_auto_42px] gap-[8px] mb-[6px]">
          <span className="text-text-secondary truncate text-[12.5px] min-w-0">
            {window.label}
          </span>
          <span className="whitespace-nowrap text-[10.5px] text-[#b3aba0]">
            {window.reset}
          </span>
          <span
            style={cssStyle("color:" + String(tone.value))}
            className="text-right font-bold whitespace-nowrap text-[13px] tabular-nums"
          >
            {window.remain}
          </span>
        </div>
        <ProgressBar pct={window.pct} color={tone.bar} />
      </div>
    </>
  );
}
