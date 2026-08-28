<script lang="ts">
  import { AGENT_STYLE } from "$lib/domain/agent";
  import { headOf, limitTone } from "../model";
  import type { TrayVendor } from "../types";
  import LimitWindowRow from "./LimitWindowRow.svelte";
  import AgentBadge from "$lib/components/ui/AgentBadge.svelte";
  import ProgressBar from "$lib/components/ui/ProgressBar.svelte";
  import ChevronDownIcon from "$lib/icons/ChevronDownIcon.svelte";

  let { vendor }: { vendor: TrayVendor } = $props();
  let open = $state(false);
  const style = $derived(AGENT_STYLE[vendor.id]);
  const head = $derived(headOf(vendor.windows));
  const rest = $derived(vendor.windows.filter((window) => window !== head));
  const tone = $derived(limitTone(head.pct, style.fg));
</script>

<button
  type="button"
  onclick={() => (open = !open)}
  aria-expanded={open}
  class="bg-surface hover:border-border-strong block w-full cursor-pointer border text-left"
  style="border-radius:11px;padding:9px 12px;margin-bottom:7px;border-color:{open
    ? 'var(--color-border-strong)'
    : 'var(--color-border)'}"
>
  <div
    class="grid items-center"
    style="grid-template-columns:24px minmax(0,1fr) auto auto auto;gap:8px;margin-bottom:6px"
  >
    <AgentBadge
      agent={vendor.id}
      size={24}
      fontSize={Math.min(style.fontSm + 2, 15)}
    />
    <span class="flex items-baseline" style="gap:6px;min-width:0">
      <span class="text-text flex-none font-bold" style="font-size:12.5px">
        {style.name}
      </span>
      <span class="flex-none" style="font-size:10.5px;color:#c9c3ba">·</span>
      <span
        class="text-text-secondary truncate"
        style="font-size:11px;min-width:0">{head.label}</span
      >
    </span>
    <span
      class="flex-none whitespace-nowrap"
      style="font-size:10.5px;color:#b3aba0"
    >
      {head.reset}
    </span>
    <span
      class="flex-none text-right font-bold whitespace-nowrap"
      style="font-size:13px;font-variant-numeric:tabular-nums;color:{tone.value};min-width:34px"
      >{head.remain}</span
    >
    <span class="flex items-center justify-end" style="gap:4px">
      <span class="whitespace-nowrap" style="font-size:9.5px;color:#b3aba0">
        {rest.length && !open ? `+${rest.length}` : ""}
      </span>
      <ChevronDownIcon
        size={12}
        strokeWidth={2.4}
        class="flex-none"
        style="color:#b3aba0"
        rotated={open}
      />
    </span>
  </div>
  <ProgressBar pct={head.pct} color={tone.bar} animate />

  {#if open}
    <div style="margin-top:10px;padding-top:9px;border-top:1px solid #f1ece4">
      {#each rest as window (window.label)}
        <LimitWindowRow {window} accent={style.fg} />
      {/each}
      <div class="flex items-baseline" style="gap:8px;padding-top:2px">
        <span
          class="truncate"
          style="font-size:10.5px;color:#b3aba0;min-width:0"
        >
          {vendor.credential}
        </span>
        <span style="flex:1"></span>
        <span
          class="text-text-muted flex-none whitespace-nowrap"
          style="font-size:10.5px"
        >
          {vendor.spend} · {vendor.tokens}
        </span>
      </div>
    </div>
  {/if}
</button>
