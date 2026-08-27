<script lang="ts">
  import type { ActivitySession } from "../data";
  import SessionRow from "./SessionRow.svelte";

  let {
    sessions,
    selectedIndex = null,
    onOpen,
  }: {
    sessions: ActivitySession[];
    selectedIndex?: number | null;
    onOpen?: (index: number) => void;
  } = $props();
</script>

<div
  class="bg-surface border-border overflow-hidden border"
  style="border-radius:14px"
>
  <div
    class="border-border text-text-muted grid items-center border-b font-semibold"
    style="grid-template-columns:14px 58px minmax(0,1.35fr) minmax(0,1fr) 62px 62px 62px 76px;gap:12px;padding:11px 18px;background:#faf7f2;font-size:11.5px;letter-spacing:0.02em"
  >
    <span></span>
    <span>시작</span>
    <span>작업</span>
    <span>경로</span>
    <span style="text-align:right">소요</span>
    <span style="text-align:right">토큰</span>
    <span style="text-align:right">비용</span>
    <span>상태</span>
  </div>
  {#each sessions as session, i (session)}
    <SessionRow
      {session}
      selected={selectedIndex === i}
      onOpen={() => onOpen?.(i)}
    />
  {/each}
  <button
    type="button"
    class="text-text-secondary hover:text-text flex w-full cursor-pointer items-center justify-center border-none bg-transparent"
    style="gap:7px;padding:14px;font-size:12.5px"
  >
    더 불러오기
    <svg
      viewBox="0 0 24 24"
      style="width:14px;height:14px"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"><path d="m6 9 6 6 6-6"></path></svg
    >
  </button>
</div>
