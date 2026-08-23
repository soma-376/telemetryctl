<script lang="ts">
  import type { TraySession } from "../types";
  import RecentSessionRow from "./RecentSessionRow.svelte";

  let { sessions }: { sessions: TraySession[] } = $props();
</script>

<section style="padding:5px 14px 8px" aria-labelledby="recent-sessions-title">
  <h2
    id="recent-sessions-title"
    class="text-text-muted font-semibold"
    style="font-size:11px;letter-spacing:0.02em;margin-bottom:7px"
  >최근 세션</h2>
  {#each sessions as session (session.id)}
    <RecentSessionRow {session} />
  {/each}
</section>
