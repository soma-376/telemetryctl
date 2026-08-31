<script lang="ts">
  import type { HeroData } from "../../types";
  import type { ChartBar, UsageChartLayout } from "./layout";

  let {
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
  } = $props();
</script>

<svg
  class="block h-full w-full overflow-visible"
  viewBox={`0 0 ${layout.width} ${layout.height}`}
  role="img"
  aria-label="기간별 벤더 토큰 사용량"
>
  {#each [0, 0.25, 0.5, 0.75, 1] as ratio}
    {@const value = layout.niceMax * ratio}
    {@const y = layout.yAt(value)}
    <line
      x1={layout.margin.left}
      x2={layout.width - layout.margin.right}
      y1={y}
      y2={y}
      stroke="#f1ece4"
      stroke-width="1"
    />
    <text
      x={layout.margin.left - 8}
      y={y + 3.5}
      text-anchor="end"
      fill="var(--color-text-muted)"
      font-size="10">{Math.round(value)}k</text
    >
  {/each}

  {#each layout.bars as bar (bar.index)}
    {#each bar.segments as segment (segment.seriesIndex)}
      {#if segment.value > 0}
        <rect
          x={bar.x}
          y={segment.y}
          width={layout.barWidth}
          height={Math.max(segment.height, 0)}
          fill={hero.legend[segment.seriesIndex]?.color}
          opacity={hoveredIndex === null || hoveredIndex === bar.index
            ? 1
            : 0.48}
          rx="2"
        />
      {/if}
    {/each}
    <rect
      x={layout.margin.left + layout.slotWidth * bar.index}
      y={layout.margin.top}
      width={layout.slotWidth}
      height={layout.plotHeight}
      fill="transparent"
      role="presentation"
      onpointerenter={() => onHover(bar.index)}
      onpointermove={() => onHover(bar.index)}
    />
  {/each}

  {#if hovered}
    <g
      class="crosshair-motion"
      style:transform={`translate(${layout.xAt(hovered.index)}px, 0)`}
      pointer-events="none"
    >
      <line
        x1="0"
        x2="0"
        y1={layout.margin.top}
        y2={layout.margin.top + layout.plotHeight}
        stroke="var(--color-text-muted)"
        stroke-width="1"
        stroke-dasharray="4 4"
      />
    </g>
    <g
      class="crosshair-motion"
      style:transform={`translate(0, ${layout.yAt(hovered.totalValue)}px)`}
      pointer-events="none"
    >
      <line
        x1={layout.margin.left}
        x2={layout.width - layout.margin.right}
        y1="0"
        y2="0"
        stroke="var(--color-text-muted)"
        stroke-width="1"
        stroke-dasharray="4 4"
      />
    </g>
    <g
      class="crosshair-motion"
      style:transform={`translate(${layout.xAt(hovered.index)}px, ${layout.yAt(hovered.totalValue)}px)`}
      pointer-events="none"
    >
      <circle
        cx="0"
        cy="0"
        r="3"
        fill="var(--color-surface)"
        stroke="var(--color-text-secondary)"
        stroke-width="1.5"
      />
    </g>
  {/if}

  {#each layout.tickIndexes as index (index)}
    <text
      x={layout.xAt(index)}
      y={layout.height - 7}
      text-anchor="middle"
      fill="var(--color-text-muted)"
      font-size="11">{hero.bars[index].label}</text
    >
  {/each}
</svg>

<style>
  .crosshair-motion {
    transform-box: view-box;
    transform-origin: 0 0;
    transition: transform 400ms cubic-bezier(0.22, 1, 0.36, 1);
    will-change: transform;
  }

  @media (prefers-reduced-motion: reduce) {
    .crosshair-motion {
      transition: none;
    }
  }
</style>
