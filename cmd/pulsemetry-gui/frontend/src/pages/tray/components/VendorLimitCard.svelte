<script lang="ts">
  import { AGENT_STYLE } from "../../../lib/agents";
  import { headOf, limitTone } from "../tray";
  import type { TrayVendor } from "../types";
  import LimitWindowRow from "./LimitWindowRow.svelte";

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
    <span
      class="flex items-center justify-center"
      style="width:24px;height:24px;border-radius:7px;background:{style.bg};color:{style.fg};font-size:{Math.min(
        style.fontSm + 2,
        15,
      )}px;font-weight:{style.weight}">{style.glyph}</span
    >
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
      <svg
        viewBox="0 0 24 24"
        class="flex-none"
        style="width:12px;height:12px;color:#b3aba0;transform:rotate({open
          ? 180
          : 0}deg);transition:transform 200ms cubic-bezier(0.32,0.72,0,1)"
        fill="none"
        stroke="currentColor"
        stroke-width="2.4"
        stroke-linecap="round"
        stroke-linejoin="round"><path d="m6 9 6 6 6-6" /></svg
      >
    </span>
  </div>
  <div class="bg-track" style="height:5px;border-radius:999px">
    <div
      style="height:100%;border-radius:999px;background:{tone.bar};width:{head.pct}%;transition:width 300ms ease"
    ></div>
  </div>

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
