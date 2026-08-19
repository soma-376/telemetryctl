<script lang="ts">
  import type { VendorUsage } from "../../../lib/types";
  import { AGENT_STYLE } from "../../../lib/agents";
  import AgentBadge from "../../../lib/components/AgentBadge.svelte";

  let { vendor }: { vendor: VendorUsage } = $props();

  const style = $derived(AGENT_STYLE[vendor.id]);

  // 남은 비율이 줄수록 벤더색 → 경고 → 위험으로 전환
  const barFg = (pct: number) =>
    pct >= 50 ? style.fg : pct >= 25 ? "var(--color-warning)" : "var(--color-danger)";
  const valueFg = (pct: number) =>
    pct >= 25 ? "var(--color-text)" : "var(--color-danger-strong)";
</script>

<div
  class="bg-surface border-border flex flex-col rounded-[14px] border"
  style="padding:16px 18px"
>
  <div class="flex items-center" style="gap:11px;margin-bottom:14px">
    <AgentBadge agent={vendor.id} size="md" />
    <span
      class="text-text truncate font-bold"
      style="font-size:15.5px;letter-spacing:-0.01em;min-width:0"
    >
      {style.name}
    </span>
    <span style="flex:1"></span>
    <span
      class="text-text-muted flex-none whitespace-nowrap"
      style="font-size:11.5px"
    >
      {vendor.plan}
    </span>
    <span
      class="flex-none"
      style="width:8px;height:8px;border-radius:50%;background:var(--color-success)"
    ></span>
  </div>

  <div
    class="border-track flex items-baseline border-b"
    style="gap:8px;margin-bottom:16px;padding-bottom:14px"
  >
    <span
      class="text-text font-bold"
      style="font-size:24px;letter-spacing:-0.025em;font-variant-numeric:tabular-nums"
    >
      {vendor.spend}
    </span>
    <span class="text-text-secondary whitespace-nowrap" style="font-size:12.5px">
      {vendor.spendNote}
    </span>
    <span style="flex:1"></span>
    <span class="text-text-muted whitespace-nowrap" style="font-size:12px">
      {vendor.tokens}
    </span>
  </div>

  {#each vendor.limits as l (l.scope)}
    <div style="margin-bottom:14px">
      <div class="flex items-baseline" style="gap:10px;margin-bottom:6px">
        <span
          class="text-text-secondary truncate"
          style="font-size:12px;min-width:0"
        >
          {l.scope}
        </span>
        <span style="flex:1"></span>
        <span
          class="text-text-muted flex-none whitespace-nowrap"
          style="font-size:11.5px"
        >
          {l.reset}
        </span>
      </div>
      <div class="flex items-baseline" style="gap:8px;margin-bottom:7px">
        <span
          class="font-bold whitespace-nowrap"
          style="font-size:16px;font-variant-numeric:tabular-nums;color:{valueFg(
            l.pct,
          )}"
        >
          {l.remain}
        </span>
        <span class="text-text-muted whitespace-nowrap" style="font-size:12px">
          {l.used}
        </span>
      </div>
      <div class="bg-track" style="height:6px;border-radius:999px">
        <div
          style="height:100%;border-radius:999px;background:{barFg(
            l.pct,
          )};width:{l.pct}%"
        ></div>
      </div>
    </div>
  {/each}

  <div
    class="text-text-muted mt-auto flex items-center"
    style="gap:8px;font-size:11.5px;padding-top:2px"
  >
    <span class="truncate" style="min-width:0">{vendor.credential}</span>
  </div>
</div>
