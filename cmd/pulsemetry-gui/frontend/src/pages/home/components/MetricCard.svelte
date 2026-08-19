<script lang="ts">
  import type { Snippet } from "svelte";

  let {
    icon,
    iconBg,
    iconColor,
    value,
    label,
    delta,
    deltaColor,
    deltaNote,
  }: {
    icon: Snippet;
    iconBg: string;
    iconColor: string;
    value: string;
    label: string;
    delta: string;
    deltaColor: string;
    deltaNote?: string;
  } = $props();
</script>

<div
  class="bg-surface border-border flex flex-col rounded-[14px] border"
  style="padding:16px 18px;gap:10px"
>
  <div class="flex items-center" style="gap:11px">
    <div
      class="flex flex-none items-center justify-center"
      style="width:32px;height:32px;border-radius:10px;background:{iconBg};color:{iconColor}"
    >
      {@render icon()}
    </div>
    <div
      class="text-text font-bold whitespace-nowrap"
      style="font-size:25px;letter-spacing:-0.025em"
    >
      {value}
    </div>
  </div>
  <div class="text-text-secondary" style="font-size:12.5px">{label}</div>
  <div class="mt-auto whitespace-nowrap" style="font-size:12px;color:{deltaColor}">
    {delta}{#if deltaNote}&nbsp;<span class="text-text-muted">{deltaNote}</span
      >{/if}
  </div>
</div>
