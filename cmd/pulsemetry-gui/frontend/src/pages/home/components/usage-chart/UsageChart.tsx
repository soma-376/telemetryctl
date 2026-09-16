import { cssStyle, useWidth } from "$lib/react-utils";
import { useState } from "react";
import type { HeroData } from "../../types";
import UsageChartPlot from "./UsageChartPlot";
import UsageChartTooltip from "./UsageChartTooltip";
import { createUsageChartLayout } from "./layout";

export default function UsageChart({ hero }: { hero: HeroData }) {
  const [width, setWidth] = useState(720);
  const measureChart = useWidth(setWidth);
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  const layout = createUsageChartLayout(
    hero.bars,
    width,
    hero.unit,
    hero.bucketSize,
  );
  const hovered =
    hoveredIndex === null ? null : (layout.bars[hoveredIndex] ?? null);
  const tooltipLeft = hovered
    ? Math.min(Math.max(layout.xAt(hovered.index), 105), layout.width - 105)
    : 0;
  const tooltipTop = hovered
    ? Math.max(8, layout.yAt(hovered.totalValue) - 8)
    : 0;

  return (
    <>
      <div
        ref={measureChart}
        onPointerLeave={() => setHoveredIndex(null)}
        role="presentation"
        style={cssStyle("height:" + String(layout.height) + "px")}
        className="relative w-full"
      >
        <UsageChartPlot
          hero={hero}
          layout={layout}
          hovered={hovered}
          hoveredIndex={hoveredIndex}
          onHover={(index) => setHoveredIndex(index)}
        />
        {hovered ? (
          <>
            <UsageChartTooltip
              hero={hero}
              bar={hovered}
              left={tooltipLeft}
              top={tooltipTop}
            />
          </>
        ) : null}
      </div>
    </>
  );
}
