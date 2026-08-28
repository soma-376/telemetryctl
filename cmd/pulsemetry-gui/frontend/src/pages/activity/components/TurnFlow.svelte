<script lang="ts">
  import type { TurnSegment } from "../types";

  let {
    turnCount,
    segments,
    legend,
    selected,
    onPick,
  }: {
    turnCount: string;
    segments: TurnSegment[];
    legend: { name: string; color: string; pct: string }[];
    selected: number | null;
    onPick?: (n: number) => void;
  } = $props();
</script>

<div
  class="bg-surface border-border border"
  style="border-radius:12px;padding:14px 16px"
>
  <div class="flex items-baseline" style="gap:9px;margin-bottom:12px">
    <span class="text-text-secondary font-semibold" style="font-size:12.5px"
      >턴 흐름</span
    >
    <span class="text-text-muted" style="font-size:11.5px">{turnCount}</span>
  </div>
  <div class="flex" style="gap:2px;margin-bottom:9px">
    {#each segments as seg, i (i)}
      <button
        type="button"
        class="cursor-pointer border-none p-0"
        title={seg.tip}
        aria-label={seg.tip}
        style="height:9px;min-width:8px;flex:{seg.grow} 1 0;background:{seg.color};border-radius:{seg.radius};outline:{selected ===
        i + 1
          ? '2px solid var(--color-text)'
          : 'none'};outline-offset:1px"
        onclick={() => onPick?.(i + 1)}
      ></button>
    {/each}
  </div>
  <div class="flex items-center" style="gap:13px">
    {#each legend as item (item.name)}
      <span
        class="text-text-secondary flex items-center whitespace-nowrap"
        style="gap:6px;font-size:11px"
      >
        <span
          class="flex-none"
          style="width:8px;height:8px;border-radius:2px;background:{item.color}"
        ></span>{item.name}
        {item.pct}
      </span>
    {/each}
    <span class="flex-1"></span>
    <span class="text-text-muted whitespace-nowrap" style="font-size:11px"
      >비율 · 막대 폭 = 소요 시간</span
    >
  </div>
</div>
