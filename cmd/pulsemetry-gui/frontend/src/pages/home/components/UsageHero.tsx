import EmptyState from "$lib/components/ui/EmptyState";
import { cssStyle } from "$lib/react-utils";
import { Fragment } from "react";
import type { HeroData } from "../types";
import UsageChart from "./usage-chart/UsageChart";

export default function UsageHero({ hero }: { hero: HeroData }) {
  return (
    <>
      <div className="bg-surface border-border border rounded-[14px] p-[18px_22px] mb-[12px]">
        <div className="grid items-stretch grid-cols-[186px_minmax(0,1fr)] gap-[26px]">
          <div className="flex flex-col pr-[24px] [border-right:1px_solid_#f1ece4]">
            <div className="text-text-muted text-[12px] mb-[8px]">
              토큰 사용량
            </div>
            <div className="flex items-baseline gap-[4px] mb-[16px]">
              <span className="text-text font-bold text-[38px] tracking-[-0.035em] leading-none tabular-nums">
                {hero.totalTokens}
              </span>
            </div>
            <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-[7px_12px] text-[12.5px]">
              <span className="text-text-muted whitespace-nowrap">
                예상 비용
              </span>
              <span className="text-right font-semibold tabular-nums">
                {hero.totalCost}
              </span>
              <span className="text-text-muted whitespace-nowrap">
                AI 활동 시간
              </span>
              <span className="text-right font-semibold tabular-nums">
                {hero.totalTime}
              </span>
              <span className="text-text-muted whitespace-nowrap">
                {hero.avgLabel}
              </span>
              <span className="text-right font-semibold tabular-nums">
                {hero.avgValue}
              </span>
            </div>
            <div className="mt-auto flex flex-wrap pt-[14px] gap-[6px_12px]">
              {hero.legend.map((l) => (
                <Fragment key={l.name}>
                  <span className="text-text-secondary flex items-center whitespace-nowrap gap-[6px] text-[11.5px]">
                    <span
                      style={cssStyle("background:" + String(l.color))}
                      className="flex-none w-[8px] h-[8px] rounded-[2px]"
                    />
                    {l.name}
                  </span>
                </Fragment>
              ))}
            </div>
          </div>
          <div className="flex flex-col min-w-0">
            <div className="flex items-baseline gap-[10px] mb-[12px]">
              <span className="text-text-muted text-[12px]">
                {hero.caption}
              </span>
              <span className="flex-1" />
              <span className="text-text-muted text-[11.5px]">
                {hero.peakNote}
              </span>
            </div>

            {hero.grandTotal === 0 ? (
              <>
                <EmptyState
                  title="이 기간에 기록된 사용량이 없어요"
                  description="다른 기간을 선택하거나, CLI 가 연결되어 있는지 확인해보세요."
                />
              </>
            ) : (
              <>
                <UsageChart hero={hero} />
              </>
            )}
          </div>
        </div>
        {hero.costNote && (
          <p className="text-text-muted text-[11.5px] mt-[10px]">
            {hero.costNote}
          </p>
        )}
      </div>
    </>
  );
}
