<script lang="ts">
  import type { VendorUsage } from "../../../lib/types";
  import RefreshIcon from "../../../lib/icons/RefreshIcon.svelte";
  import VendorCard from "./VendorCard.svelte";

  let {
    vendors,
    syncedText,
    onRefresh,
  }: {
    vendors: VendorUsage[];
    syncedText: string;
    onRefresh?: () => void;
  } = $props();
</script>

<div class="flex items-baseline" style="gap:10px;margin-bottom:10px">
  <div class="text-text font-bold" style="font-size:16px;letter-spacing:-0.01em">
    벤더별 한도
  </div>
  <div class="text-text-muted" style="font-size:12.5px">
    각 벤더 자격 증명으로 조회한 실제 사용량이에요.
  </div>
  <div style="flex:1"></div>
  <div
    class="text-text-muted flex items-center whitespace-nowrap"
    style="gap:7px;font-size:12px"
  >
    <span
      class="flex-none"
      style="width:7px;height:7px;border-radius:50%;background:var(--color-success)"
    ></span>
    {syncedText}
    <button
      type="button"
      title="새로고침"
      onclick={onRefresh}
      class="bg-surface border-border text-accent hover:border-border-strong hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border transition-colors duration-[120ms] ease-in-out"
      style="width:26px;height:26px;border-radius:8px;margin-left:4px"
    >
      <RefreshIcon />
    </button>
  </div>
</div>

<div
  class="grid items-start"
  style="grid-template-columns:repeat(3,minmax(0,1fr));gap:14px"
>
  {#each vendors as v (v.id)}
    <VendorCard vendor={v} />
  {/each}
</div>
