<script lang="ts">
  import PeriodPicker from "../home/components/PeriodPicker.svelte";

  // 날짜는 헤더와 같은 전역 period 스토어를 쓰는 PeriodPicker.
  // 나머지 필터는 아직 표시 전용(실데이터 연동 시 구현).
  const filters = ["모든 프로젝트", "All agents", "모든 상태"];
</script>

<div class="flex items-center" style="gap:10px">
  <PeriodPicker align="left" />
  {#each filters as label (label)}
    <button
      type="button"
      class="bg-surface border-border text-text hover:border-border-strong flex flex-none cursor-pointer items-center whitespace-nowrap border"
      style="gap:9px;border-radius:10px;padding:10px 14px;font-size:13px"
    >
      {` ${label} `}
      <svg
        viewBox="0 0 24 24"
        style="width:14px;height:14px;margin-left:10px"
        fill="none"
        stroke="var(--color-text-muted)"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"><path d="m6 9 6 6 6-6"></path></svg
      >
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
      ><circle cx="11" cy="11" r="6.5"></circle><path d="m16 16 4 4"></path></svg
    >
    <span
      class="text-text-muted overflow-hidden text-ellipsis whitespace-nowrap"
      style="font-size:13px">작업, 경로, 파일 검색</span
    >
  </div>
</div>
