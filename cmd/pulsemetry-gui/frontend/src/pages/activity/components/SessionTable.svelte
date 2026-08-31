<script lang="ts">
  import type { ActivitySession } from "../types";
  import SessionRow from "./SessionRow.svelte";
  import ChevronDownIcon from "$lib/icons/ChevronDownIcon.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";

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
  {#if sessions.length === 0}
    <!-- 첫 실행(수집 이력 자체가 없음)과 기간에만 없음은 문구가 달라야 한다.
         구분하려면 dashboard.Status() 가 필요해서 지금은 후자로 적어둔다. -->
    <EmptyState
      title="세션이 없어요"
      description={"선택한 기간에 실행된 세션이 없습니다.\n기간을 넓히거나 필터를 지워보세요."}
    />
  {:else}
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
      <ChevronDownIcon strokeWidth={2} />
    </button>
  {/if}
</div>
