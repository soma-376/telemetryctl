import { cssStyle } from "$lib/react-utils";
import { Fragment } from "react";
import type { HeroData } from "../../types";
import type { ChartBar } from "./layout";
import "./UsageChartTooltip.css";

export default function UsageChartTooltip({
  hero,
  bar,
  left,
  top,
}: {
  hero: HeroData;
  bar: ChartBar;
  left: number;
  top: number;
}) {
  return (
    <>
      <div
        style={cssStyle(
          "left:" + String(left) + "px;top:" + String(top) + "px",
        )}
        className="usage-tooltip pointer-events-none absolute z-10 bg-surface border-border border shadow-sm scope-1n5dj3j [transform:translate(-50%,-100%)] w-[184px] rounded-[9px] p-[9px_11px]"
      >
        <div className="text-text font-semibold scope-1n5dj3j text-[12px] mb-[7px]">
          {bar.label}
        </div>
        <div className="grid scope-1n5dj3j grid-cols-[1fr_auto] gap-[5px_10px] text-[11.5px]">
          {hero.legend.map((legend, index) => (
            <Fragment key={legend.name}>
              <span className="text-text-secondary flex items-center scope-1n5dj3j gap-[6px]">
                <span
                  style={cssStyle("background:" + String(legend.color))}
                  className="scope-1n5dj3j w-[7px] h-[7px] rounded-[2px]"
                />
                {legend.name}
              </span>
              <span className="font-semibold scope-1n5dj3j tabular-nums">
                {bar.values[index]}k
              </span>
            </Fragment>
          ))}
          <span className="text-text font-semibold scope-1n5dj3j pt-[3px] [border-top:1px_solid_#f1ece4]">
            합계
          </span>
          <span className="text-text text-right font-bold scope-1n5dj3j pt-[3px] [border-top:1px_solid_#f1ece4] tabular-nums">
            {bar.totalValue}k
          </span>
        </div>
      </div>
    </>
  );
}
