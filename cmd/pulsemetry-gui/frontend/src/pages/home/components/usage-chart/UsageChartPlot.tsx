import { Fragment } from "react";
import { formatTokens } from "$lib/utils/format";
import type { HeroData } from "../../types";
import type { ChartBar, UsageChartLayout } from "./layout";
import "./UsageChartPlot.css";

export default function UsageChartPlot({
  hero,
  layout,
  hovered,
  hoveredIndex,
  onHover,
}: {
  hero: HeroData;
  layout: UsageChartLayout;
  hovered: ChartBar | null;
  hoveredIndex: number | null;
  onHover: (index: number) => void;
}) {
  return (
    <>
      <svg
        viewBox={`0 0 ${layout.width} ${layout.height}`}
        role="img"
        aria-label="기간별 벤더 토큰 사용량"
        className="block h-full w-full overflow-visible scope-ec91ct"
      >
        {[0, 0.25, 0.5, 0.75, 1].map((ratio, index) => (
          <Fragment key={index}>
            {(() => {
              const value = layout.niceMax * ratio;
              const y = layout.yAt(value);

              return (
                <>
                  <line
                    x1={layout.margin.left}
                    x2={layout.width - layout.margin.right}
                    y1={y}
                    y2={y}
                    stroke="#f1ece4"
                    strokeWidth="1"
                    className="scope-ec91ct"
                  />
                  <text
                    x={layout.margin.left - 8}
                    y={y + 3.5}
                    textAnchor="end"
                    fill="var(--color-text-muted)"
                    fontSize="10"
                    className="scope-ec91ct"
                  >
                    {formatTokens(value)}
                  </text>
                </>
              );
            })()}
          </Fragment>
        ))}
        {layout.bars.map((bar) => (
          <Fragment key={bar.index}>
            {bar.segments.map((segment) => (
              <Fragment key={segment.seriesIndex}>
                {segment.value > 0 ? (
                  <>
                    <rect
                      x={bar.x}
                      y={segment.y}
                      width={layout.barWidth}
                      height={Math.max(segment.height, 0)}
                      fill={hero.legend[segment.seriesIndex]?.color}
                      opacity={
                        hoveredIndex === null || hoveredIndex === bar.index
                          ? 1
                          : 0.48
                      }
                      rx="2"
                      className="scope-ec91ct"
                    />
                  </>
                ) : null}
              </Fragment>
            ))}
            <rect
              x={layout.margin.left + layout.slotWidth * bar.index}
              y={layout.margin.top}
              width={layout.slotWidth}
              height={layout.plotHeight}
              fill="transparent"
              role="presentation"
              onPointerEnter={() => onHover(bar.index)}
              onPointerMove={() => onHover(bar.index)}
              className="scope-ec91ct"
            />
          </Fragment>
        ))}
        {hovered ? (
          <>
            <g
              pointerEvents="none"
              style={{
                transform: `translate(${layout.xAt(hovered.index)}px, 0)`,
              }}
              className="crosshair-motion scope-ec91ct"
            >
              <line
                x1="0"
                x2="0"
                y1={layout.margin.top}
                y2={layout.margin.top + layout.plotHeight}
                stroke="var(--color-text-muted)"
                strokeWidth="1"
                strokeDasharray="4 4"
                className="scope-ec91ct"
              />
            </g>
            <g
              pointerEvents="none"
              style={{
                transform: `translate(0, ${layout.yAt(hovered.totalValue)}px)`,
              }}
              className="crosshair-motion scope-ec91ct"
            >
              <line
                x1={layout.margin.left}
                x2={layout.width - layout.margin.right}
                y1="0"
                y2="0"
                stroke="var(--color-text-muted)"
                strokeWidth="1"
                strokeDasharray="4 4"
                className="scope-ec91ct"
              />
            </g>
            <g
              pointerEvents="none"
              style={{
                transform: `translate(${layout.xAt(hovered.index)}px, ${layout.yAt(hovered.totalValue)}px)`,
              }}
              className="crosshair-motion scope-ec91ct"
            >
              <circle
                cx="0"
                cy="0"
                r="3"
                fill="var(--color-surface)"
                stroke="var(--color-text-secondary)"
                strokeWidth="1.5"
                className="scope-ec91ct"
              />
            </g>
          </>
        ) : null}
        {layout.ticks.map((tick) => (
          <Fragment key={tick.x}>
            <text
              x={tick.x}
              y={layout.height - 7}
              textAnchor="middle"
              fill="var(--color-text-muted)"
              fontSize="11"
              className="scope-ec91ct"
            >
              {tick.label}
            </text>
          </Fragment>
        ))}
      </svg>
    </>
  );
}
