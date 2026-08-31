<script lang="ts">
  import { AGENT_STYLE } from "$lib/domain/agent";
  import AgentBadge from "$lib/components/ui/AgentBadge.svelte";
  import ProgressBar from "$lib/components/ui/ProgressBar.svelte";
  import type { VendorRow } from "../types";

  let { vendors }: { vendors: VendorRow[] } = $props();
</script>

<div
  class="bg-surface border-border border"
  style="border-radius:14px;padding:6px 22px 10px;margin-bottom:12px"
>
  {#each vendors as v, i (v.id)}
    {@const style = AGENT_STYLE[v.id]}
    <div
      class="grid items-center"
      style="grid-template-columns:30px 128px 74px 62px minmax(0,1fr) 168px;gap:16px;padding:12px 0;border-bottom:1px solid {i ===
      vendors.length - 1
        ? 'transparent'
        : '#f5f1ea'}"
    >
      <AgentBadge agent={v.id} size={30} />
      <span style="min-width:0">
        <span
          class="text-text block truncate font-semibold"
          style="font-size:13.5px;margin-bottom:3px"
        >
          {style.name}
        </span>
        <span class="text-text-muted block truncate" style="font-size:11px">
          {v.plan}
        </span>
      </span>
      <span
        class="text-right font-bold whitespace-nowrap"
        style="font-size:15px;font-variant-numeric:tabular-nums"
      >
        {v.spend}
      </span>
      <span
        class="text-text-secondary text-right whitespace-nowrap"
        style="font-size:12.5px;font-variant-numeric:tabular-nums"
      >
        {v.tokens}
      </span>
      <span class="flex items-center" style="min-width:0;gap:10px">
        <ProgressBar
          pct={v.share}
          color={style.fg}
          height={6}
          style="flex:1;min-width:0"
        />
        <span
          class="text-text-secondary flex-none font-semibold whitespace-nowrap"
          style="font-size:11.5px;font-variant-numeric:tabular-nums"
        >
          {v.share}
        </span>
      </span>
      <span class="text-text-muted truncate" style="font-size:11.5px">
        {v.topModel}
      </span>
    </div>
  {/each}
</div>
