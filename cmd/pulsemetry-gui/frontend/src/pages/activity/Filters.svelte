<script lang="ts">
  const filters = ["오늘 (8월 8일)", "모든 프로젝트", "All agents", "모든 상태"];
</script>

<div class="flex items-center" style="gap:10px">
  {#each filters as label, i (label)}
    <button
      type="button"
      class="bg-surface border-border text-text hover:border-border-strong flex flex-none cursor-pointer items-center whitespace-nowrap border"
      style="gap:9px;border-radius:10px;padding:10px 14px;font-size:13px"
    >
      {#if i === 0}
        <svg
          viewBox="0 0 24 24"
          style="width:16px;height:16px"
          fill="none"
          stroke="var(--color-text-secondary)"
          stroke-width="1.7"
          stroke-linecap="round"
          ><rect x="3" y="5" width="18" height="16" rx="3"></rect><path
            d="M8 3v4M16 3v4M3 10h18"
          ></path></svg
        >
      {/if}
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
