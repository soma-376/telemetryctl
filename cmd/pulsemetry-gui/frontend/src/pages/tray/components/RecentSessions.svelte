<script lang="ts">
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import type { TraySession } from "../types";
  import RecentSessionRow from "./RecentSessionRow.svelte";

  let { sessions }: { sessions: TraySession[] } = $props();
</script>

<section style="padding:5px 14px 8px" aria-labelledby="recent-sessions-title">
  <h2
    id="recent-sessions-title"
    class="text-text-muted font-semibold"
    style="font-size:11px;letter-spacing:0.02em;margin-bottom:7px"
  >
    최근 세션
  </h2>
  {#each sessions as session (session.id)}
    <RecentSessionRow {session} />
  {/each}
  {#if sessions.length === 0}
    <EmptyState
      size="sm"
      pose="collecting-alt"
      title="아직 세션이 없습니다"
      description="AI 도구로 작업을 시작하면 여기에 나타납니다."
    />
  {/if}
</section>
