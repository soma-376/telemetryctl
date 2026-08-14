<script lang="ts">
  import type { ActivitySession } from "./data";
  import { rowDisplay } from "./data";

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
  <span
    class="inline-block"
    style="width:9px;height:9px;border-radius:50%;background:{d.dot};animation:{d.dotAnim}"
  ></span>
  <span
    class="text-text-secondary"
    style="font-size:13px;font-variant-numeric:tabular-nums">{d.time}</span
  >
  <span class="flex min-w-0 items-center" style="gap:10px">
    <span
      class="flex flex-none items-center justify-center"
      style="width:28px;height:28px;border-radius:9px;background:{d.agentTile
        .bg};color:{d.agentTile.fg};font-size:{d.agentTile
        .size}px;font-weight:{d.agentTile.weight}">{d.agentTile.glyph}</span
    >
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
  <span
    class="inline-flex items-center justify-self-start font-semibold whitespace-nowrap"
    style="gap:5px;font-size:11px;border-radius:6px;padding:4px 8px;background:{d
      .badge.bg};color:{d.badge.fg}"
    ><span
      class="inline-block"
      style="width:{d.badge.dot};height:{d.badge
        .dot};border-radius:50%;background:{d.badge.fg};animation:{d.dotAnim}"
    ></span> {d.badge.label}</span
  >
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
