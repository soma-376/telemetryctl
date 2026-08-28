<script lang="ts">
  import type { HeroData } from "../../types";
  import type { ChartBar } from "./layout";

  let {
    hero,
    bar,
    left,
    top,
  }: { hero: HeroData; bar: ChartBar; left: number; top: number } = $props();
</script>

<div
  class="usage-tooltip pointer-events-none absolute z-10 bg-surface border-border border shadow-sm"
  style="left:{left}px;top:{top}px;transform:translate(-50%,-100%);width:184px;border-radius:9px;padding:9px 11px"
>
  <div class="text-text font-semibold" style="font-size:12px;margin-bottom:7px">
    {bar.label}
  </div>
  <div
    class="grid"
    style="grid-template-columns:1fr auto;gap:5px 10px;font-size:11.5px"
  >
    {#each hero.legend as legend, index (legend.name)}
      <span class="text-text-secondary flex items-center" style="gap:6px">
        <span
          style="width:7px;height:7px;border-radius:2px;background:{legend.color}"
        ></span>
        {legend.name}
      </span>
      <span class="font-semibold" style="font-variant-numeric:tabular-nums">
        {bar.values[index]}k
      </span>
    {/each}
    <span
      class="text-text font-semibold"
      style="padding-top:3px;border-top:1px solid #f1ece4">합계</span
    >
    <span
      class="text-text text-right font-bold"
      style="padding-top:3px;border-top:1px solid #f1ece4;font-variant-numeric:tabular-nums"
    >
      {bar.totalValue}k
    </span>
  </div>
</div>

<style>
  .usage-tooltip {
    transition:
      left 400ms cubic-bezier(0.22, 1, 0.36, 1),
      top 400ms cubic-bezier(0.22, 1, 0.36, 1);
    animation: tooltip-in 200ms ease-out;
    will-change: left, top;
  }

  @keyframes tooltip-in {
    from {
      opacity: 0;
      margin-top: 3px;
    }
    to {
      opacity: 1;
      margin-top: 0;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .usage-tooltip {
      transition: none;
      animation: none;
    }
  }
</style>
