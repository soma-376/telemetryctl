<script lang="ts">
  import { openMainWindow } from "$lib/ipc/app";
  import AgentBadge from "$lib/components/ui/AgentBadge.svelte";
  import Dot from "$lib/components/ui/Dot.svelte";
  import type { TraySession } from "../types";

  let { session }: { session: TraySession } = $props();
</script>

<button
  type="button"
  onclick={openMainWindow}
  class="bg-surface hover:bg-surface-hover grid w-full cursor-pointer items-center text-left"
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
      class="text-text block truncate font-semibold"
      style="font-size:12.5px;margin-bottom:2px">{session.title}</span
    >
    <span class="text-text-muted block truncate" style="font-size:10.5px">
      {session.sub}
    </span>
  </span>
</button>
