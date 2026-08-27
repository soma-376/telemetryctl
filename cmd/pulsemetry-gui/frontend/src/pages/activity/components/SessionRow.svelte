<script lang="ts">
  import type { ActivitySession } from "../data";
  import { rowDisplay } from "../data";
  import AgentBadge from "../../../lib/components/AgentBadge.svelte";
  import Dot from "../../../lib/components/Dot.svelte";
  import Pill from "../../../lib/components/Pill.svelte";

  let {
    session,
    selected = false,
    onOpen,
  }: {
    session: ActivitySession;
    selected?: boolean;
    onOpen?: () => void;
  } = $props();

  const d = $derived(rowDisplay(session, selected));

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onOpen?.();
    }
  }
</script>

<div
  class="row grid cursor-pointer items-center border-b"
  role="button"
  tabindex="0"
  style="--row-bg:{d.bg};grid-template-columns:14px 58px minmax(0,1.35fr) minmax(0,1fr) 62px 62px 62px 76px;gap:12px;padding:12px 18px;border-color:#f1ece4;box-shadow:{d.rail}"
  onclick={() => onOpen?.()}
  onkeydown={onKeydown}
>
  <Dot size={9} color={d.dot} pulse={d.running} />
  <span
    class="text-text-secondary"
    style="font-size:13px;font-variant-numeric:tabular-nums">{d.time}</span
  >
  <span class="flex min-w-0 items-center" style="gap:10px">
    <AgentBadge agent={d.agentId} size={28} />
    <span style="min-width:0">
      <span
        class="text-text block overflow-hidden font-semibold text-ellipsis whitespace-nowrap"
        style="font-size:13.5px;margin-bottom:3px">{d.title}</span
      >
      <span
        class="block overflow-hidden text-ellipsis whitespace-nowrap"
        style="font-size:11.5px"
        ><span class="text-text-muted">{d.agentName}</span><span
          class="font-semibold"
          style="color:var(--color-accent-hover)">{d.stageText}</span
        ></span
      >
    </span>
  </span>
  <span
    class="flex min-w-0 items-baseline"
    style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px"
  >
    <span class="text-text flex-none">{d.repo}</span>
    <span class="text-text-muted flex-none">&nbsp;/&nbsp;</span>
    <span
      class="text-text-muted min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap"
      style="direction:rtl;text-align:left"><bdi>{d.path}</bdi></span
    >
  </span>
  <span
    style="font-size:12.5px;text-align:right;font-variant-numeric:tabular-nums;color:{d.durColor};font-weight:{d.durWeight}"
    >{d.dur}</span
  >
  <span
    class="text-text"
    style="font-size:12.5px;text-align:right;font-variant-numeric:tabular-nums"
    >{d.tokens}</span
  >
  <span
    class="text-text"
    style="font-size:12.5px;text-align:right;font-variant-numeric:tabular-nums"
    >{d.cost}</span
  >
  <Pill
    class="justify-self-start"
    label={d.badge.label}
    fg={d.badge.fg}
    bg={d.badge.bg}
    radius={6}
    padding="4px 8px"
    gap={5}
    dot={d.badge.dot}
    pulse={d.running}
  />
</div>

<style>
  .row {
    background: var(--row-bg);
    transition: background 0.12s ease;
  }
  .row:hover {
    background: var(--color-surface-hover);
  }
</style>
