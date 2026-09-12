<script lang="ts">
  import { openMainWindow } from "$lib/ipc/app";
  import AgentBadge from "$lib/components/ui/AgentBadge.svelte";
  import Dot from "$lib/components/ui/Dot.svelte";
  import type { TraySession } from "../types";

  let { session }: { session: TraySession } = $props();

  let titleViewport: HTMLSpanElement;
  let titleText: HTMLSpanElement;
  let overflow = $state(0);

  function measureTitle() {
    overflow = Math.max(0, titleText.scrollWidth - titleViewport.clientWidth);
  }
</script>

<button
  type="button"
  onclick={openMainWindow}
  onmouseenter={measureTitle}
  onfocus={measureTitle}
  class="session-row bg-surface hover:bg-surface-hover grid w-full cursor-pointer items-center text-left"
  style="grid-template-columns:8px 24px minmax(0,1fr);gap:9px;border:1px solid var(--color-border);border-left:3px solid {session.live
    ? 'var(--color-sand)'
    : 'var(--color-border)'};border-radius:11px;padding:7px 11px;margin-bottom:7px"
>
  <Dot
    color={session.live
      ? "var(--color-sand)"
      : "var(--color-border-strong)"}
    pulse={session.live}
  />
  <AgentBadge agent={session.agentId} size={24} />
  <span style="min-width:0">
    <span
      bind:this={titleViewport}
      class="session-title text-text block overflow-hidden whitespace-nowrap font-semibold"
      class:overflowing={overflow > 0}
      style="font-size:12.5px;margin-bottom:2px;--title-offset:-{overflow}px;--title-duration:{overflow / 35}s"
      ><span bind:this={titleText} class="title-text block truncate">{session.title}</span></span
    >
    <span class="text-text-muted block truncate" style="font-size:10.5px">
      {session.sub}
    </span>
  </span>
</button>

<style>
  @media (prefers-reduced-motion: no-preference) {
    .session-row:is(:hover, :focus-visible) .overflowing .title-text {
      overflow: visible;
      text-overflow: clip;
      transform: translateX(var(--title-offset));
      transition: transform var(--title-duration) linear 0.4s;
    }
  }
</style>
