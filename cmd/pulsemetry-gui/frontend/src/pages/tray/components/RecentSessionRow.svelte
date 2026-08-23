<script lang="ts">
  import { AGENT_STYLE } from "../../../lib/agents";
  import { openMainWindow } from "../../../lib/backend";
  import type { TraySession } from "../types";

  let { session }: { session: TraySession } = $props();
  const style = $derived(AGENT_STYLE[session.agentId]);
</script>

<button
  type="button"
  onclick={openMainWindow}
  class="bg-surface hover:bg-surface-hover grid w-full cursor-pointer items-center text-left"
  style="grid-template-columns:8px 24px minmax(0,1fr);gap:9px;border:1px solid var(--color-border);border-left:3px solid {session.live
    ? 'var(--color-sand)'
    : 'var(--color-border)'};border-radius:11px;padding:7px 11px;margin-bottom:7px"
>
  <span
    style="width:7px;height:7px;border-radius:50%;background:{session.live
      ? 'var(--color-sand)'
      : 'var(--color-border-strong)'};animation:{session.live
      ? 'livePulse 2s ease-out infinite'
      : 'none'}"
  ></span>
  <span
    class="flex items-center justify-center"
    style="width:24px;height:24px;border-radius:7px;background:{style.bg};color:{style.fg};font-size:{style.fontSm}px;font-weight:{style.weight}"
  >{style.glyph}</span>
  <span style="min-width:0">
    <span
      class="text-text block truncate font-semibold"
      style="font-size:12.5px;margin-bottom:2px"
    >{session.title}</span>
    <span class="text-text-muted block truncate" style="font-size:10.5px">
      {session.sub}
    </span>
  </span>
</button>
