import { cssStyle } from "$lib/react-utils";
import { Fragment } from "react";
import type { TurnSegment } from "../types";

export default function TurnFlow({
  turnCount,
  segments,
  legend,
  selected,
  onPick,
}: {
  turnCount: string;
  segments: TurnSegment[];
  legend: {
    name: string;
    color: string;
    pct: string;
  }[];
  selected: number | null;
  onPick?: (n: number) => void;
}) {
  return (
    <>
      <div className="bg-surface border-border border rounded-[12px] p-[14px_16px]">
        <div className="flex items-baseline gap-[9px] mb-[12px]">
          <span className="text-text-secondary font-semibold text-[12.5px]">
            턴 흐름
          </span>
          <span className="text-text-muted text-[11.5px]">{turnCount}</span>
        </div>
        <div className="flex gap-[2px] mb-[9px]">
          {segments.map((seg, i) => (
            <Fragment key={i}>
              <button
                type="button"
                title={seg.tip}
                aria-label={seg.tip}
                onClick={() => onPick?.(i + 1)}
                style={cssStyle(
                  "flex:" +
                    String(seg.grow) +
                    " 1 0;background:" +
                    String(seg.color) +
                    ";border-radius:" +
                    String(seg.radius) +
                    ";outline:" +
                    String(
                      selected === i + 1
                        ? "2px solid var(--color-text)"
                        : "none",
                    ),
                )}
                className="cursor-pointer border-none p-0 h-[9px] min-w-[8px] [outline-offset:1px]"
              />
            </Fragment>
          ))}
        </div>
        <div className="flex items-center gap-[13px]">
          {legend.map((item) => (
            <Fragment key={item.name}>
              <span className="text-text-secondary flex items-center whitespace-nowrap gap-[6px] text-[11px]">
                <span
                  style={cssStyle("background:" + String(item.color))}
                  className="flex-none w-[8px] h-[8px] rounded-[2px]"
                />
                {item.name}
                {item.pct}
              </span>
            </Fragment>
          ))}
          <span className="flex-1" />
          <span className="text-text-muted whitespace-nowrap text-[11px]">
            비율 · 막대 폭 = 소요 시간
          </span>
        </div>
      </div>
    </>
  );
}
