<script lang="ts">
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { period, periodRangeText, toDate, addDays } from "$lib/domain/period.svelte";
  import { activityQuery, activityDetailQuery } from "$lib/query/activity";
  import type { ActivityQuery } from "$lib/bindings";
  import { sessionRow, sessionDetail } from "./adapter";
  import SessionTable from "./components/SessionTable.svelte";
  import SessionDetail from "./components/SessionDetail.svelte";

  let visible = $state(true);
  let selectedId = $state<number | null>(null);
  let vendor = $state("");
  let status = $state("");
  let project = $state("");
  let search = $state("");
  let text = $state("");
  let projectPath = $state("");
  $effect(() => {
    const value = search.trim();
    const path = project.trim();
    const timer = setTimeout(() => { text = value; projectPath = path; }, 300);
    return () => clearTimeout(timer);
  });
  const request: ActivityQuery = $derived({
    since: Math.floor(toDate(period.value.start).getTime() / 1000),
    until: Math.floor(addDays(toDate(period.value.end), 1).getTime() / 1000),
    vendors: vendor ? [vendor] : [], projects: projectPath ? [projectPath] : [],
    status: status ? [status] : [], text, limit: 50, cursor: { id: 0, started_at: 0 },
  });
  const list = activityQuery(() => request, () => visible);
  const detail = activityDetailQuery(() => selectedId, () => visible);
  const sessions = $derived((list.data?.pages ?? []).flatMap(page => page.rows ?? []).map(sessionRow));
  const selectedIndex = $derived(sessions.findIndex(s => Number(s.id) === selectedId));
  const selected = $derived(selectedIndex >= 0 ? sessions[selectedIndex] ?? null : null);
  const shown = $derived(detail.data?.detail.found && detail.data.detail.session.id === selectedId ? sessionDetail(detail.data) : selected);
  const detailError = $derived(detail.isError ? "상세를 갱신하지 못했습니다." : detail.data && !detail.data.detail.found ? "세션이 삭제되었거나 더 이상 존재하지 않습니다." : "");
  $effect(() => { void request; selectedId = null; });
  function step(dir: number) {
    if (!sessions.length) return;
    const next = sessions[(selectedIndex + dir + sessions.length) % sessions.length];
    if (next) selectedId = Number(next.id);
  }
  function onKeydown(e: KeyboardEvent) {
    if (selectedId === null || (e.target instanceof HTMLElement && (e.target.matches("input,textarea,select") || e.target.isContentEditable))) return;
    if (e.key === "Escape") { e.preventDefault(); selectedId = null; }
    else if (["j", "J", "ArrowDown"].includes(e.key)) { e.preventDefault(); step(1); }
    else if (["k", "K", "ArrowUp"].includes(e.key)) { e.preventDefault(); step(-1); }
  }
  onMount(() => {
    const shown = Events.On("main:shown", () => { visible = true; void list.refetch(); if (selectedId !== null) void detail.refetch(); });
    const hidden = Events.On("main:hidden", () => { visible = false; });
    return () => { shown(); hidden(); };
  });
</script>

<svelte:window onkeydown={onKeydown} />
<main class="mx-auto w-full flex-1" style="max-width:var(--page-max-width);padding:14px 32px 10px">
  <div class="flex items-baseline gap-3 mb-4">
    <h1 class="text-text m-0 font-bold" style="font-size:38px;letter-spacing:-0.035em">Activity</h1>
    <span class="text-text-muted text-sm">({periodRangeText(period.value)})</span>
    <button class="ml-auto" disabled={list.isFetching} onclick={() => { void list.refetch(); if (selectedId !== null) void detail.refetch(); }}>새로고침</button>
  </div>
  <div class="flex flex-wrap gap-2 mb-4">
    <select aria-label="에이전트 필터" bind:value={vendor} class="bg-surface border-border rounded-lg border p-2">
      <option value="">모든 에이전트</option><option value="claude_code">Claude Code</option><option value="codex">Codex</option>
    </select>
    <select aria-label="상태 필터" bind:value={status} class="bg-surface border-border rounded-lg border p-2">
      <option value="">모든 상태</option><option value="running">진행 중</option><option value="completed">종료</option>
    </select>
    <input aria-label="프로젝트 전체 경로" placeholder="프로젝트 전체 경로" bind:value={project} class="bg-surface border-border rounded-lg border p-2" />
    <input type="search" aria-label="활동 검색" placeholder="제목·경로·파일·프롬프트 검색" bind:value={search} class="bg-surface border-border min-w-60 flex-1 rounded-lg border p-2" />
  </div>
  {#if list.isError}
    <p role="alert">데몬에서 활동을 불러오지 못했습니다. {list.data ? "마지막 조회 결과를 표시합니다." : "데몬 실행 상태를 확인해주세요."}</p>
  {/if}
  {#if list.dataUpdatedAt}<p class="text-text-muted text-xs mb-2">{new Date(list.dataUpdatedAt).toLocaleTimeString()} 조회 · 목록 비용은 벤더 보고값입니다.</p>{/if}
  {#if list.isPending}<p role="status">활동을 불러오는 중입니다.</p>
  {:else if list.data}
    <SessionTable {sessions} selectedIndex={selectedIndex} onOpen={i => { const row = sessions[i]; if (row) selectedId = Number(row.id); }} hasMore={list.hasNextPage} loadingMore={list.isFetchingNextPage} onLoadMore={() => { void list.fetchNextPage(); }} />
  {/if}
</main>
<SessionDetail open={selectedId !== null} session={shown} position={selectedIndex >= 0 ? `${selectedIndex + 1} / ${sessions.length}` : ""}
  loading={detail.isPending} error={detailError} onRetry={() => { void detail.refetch(); }}
  onClose={() => selectedId = null} onPrev={() => step(-1)} onNext={() => step(1)} />
