<script lang="ts">
  import type { HeroData } from "../../types";
  import UsageChartPlot from "./UsageChartPlot.svelte";
  import UsageChartTooltip from "./UsageChartTooltip.svelte";
  import { createUsageChartLayout } from "./layout";

  let { hero }: { hero: HeroData } = $props();

  let width = $state(720);
  let hoveredIndex = $state<number | null>(null);
  const layout = $derived(createUsageChartLayout(hero.bars, width));
  const hovered = $derived(
    hoveredIndex === null ? null : (layout.bars[hoveredIndex] ?? null),
  );
  const tooltipLeft = $derived(
    hovered
      ? Math.min(
          Math.max(layout.xAt(hovered.index), 105),
          layout.width - 105,
        )
      : 0,
  );
  const tooltipTop = $derived(
    hovered ? Math.max(8, layout.yAt(hovered.totalValue) - 8) : 0,
  );
</script>

<div
  class="relative w-full"
  style="height:{layout.height}px"
  bind:clientWidth={width}
  onpointerleave={() => (hoveredIndex = null)}
  role="presentation"
>
  <UsageChartPlot
    {hero}
    {layout}
    {hovered}
    {hoveredIndex}
    onHover={(index) => (hoveredIndex = index)}
  />

  {#if hovered}
    <UsageChartTooltip
      {hero}
      bar={hovered}
      left={tooltipLeft}
      top={tooltipTop}
    />
  {/if}
</div>