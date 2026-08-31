<script lang="ts">
  import ChevronDownIcon from "$lib/icons/ChevronDownIcon.svelte";

  // 날짜는 공용 Header의 DateRangePicker가 담당한다.
  // 나머지 필터는 아직 표시 전용(실데이터 연동 시 구현).
  const filters = ["모든 프로젝트", "All agents", "모든 상태"];
</script>

<div class="flex items-center" style="gap:10px">
  {#each filters as label (label)}
    <button
      type="button"
      class="bg-surface border-border text-text hover:border-border-strong flex flex-none cursor-pointer items-center whitespace-nowrap border"
      style="gap:9px;border-radius:10px;padding:10px 14px;font-size:13px"
    >
      {` ${label} `}
      <ChevronDownIcon
        strokeWidth={2}
        class="text-text-muted"
        style="margin-left:10px"
      />
    </button>
  {/each}
  <div class="min-w-[12px] flex-1"></div>
  <div
    class="bg-surface border-border flex flex-none items-center border"
    style="gap:9px;border-radius:10px;padding:10px 14px;width:260px"
  >
    <svg
      viewBox="0 0 24 24"
      class="flex-none"
      style="width:16px;height:16px"
      fill="none"
      stroke="var(--color-text-muted)"
      stroke-width="1.8"
      stroke-linecap="round"
      ><circle cx="11" cy="11" r="6.5"></circle><path d="m16 16 4 4"
      ></path></svg
    >
    <span
      class="text-text-muted overflow-hidden text-ellipsis whitespace-nowrap"
      style="font-size:13px">작업, 경로, 파일 검색</span
    >
  </div>
</div>
