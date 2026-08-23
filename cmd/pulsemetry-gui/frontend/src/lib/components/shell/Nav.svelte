<script lang="ts">
  import HomeIcon from "../../icons/HomeIcon.svelte";
  import ListIcon from "../../icons/ListIcon.svelte";
  import BarsIcon from "../../icons/BarsIcon.svelte";
  import type { AppSection } from "../../navigation";

  let {
    active = "overview",
    onSelect,
  }: { active?: AppSection; onSelect?: (tab: AppSection) => void } = $props();

  // 설정은 헤더의 슬라이더 아이콘 → 모달로 이동했고, Insights 는 준비 중이라
  // 비활성 자리만 지킨다 (디자인 Overview v3).
  const TABS = [
    { id: "overview", label: "Home", icon: HomeIcon },
    { id: "activity", label: "Activity", icon: ListIcon },
  ] as const;
</script>

<nav
  class="mx-auto w-full"
  style="max-width:var(--page-max-width);padding:12px 32px 18px"
>
  <div
    class="bg-surface border-border flex border"
    style="gap:6px;border-radius:16px;padding:7px"
  >
    {#each TABS as tab (tab.id)}
      {@const isActive = active === tab.id}
      <button
        type="button"
        onclick={() => onSelect?.(tab.id)}
        class="flex flex-1 cursor-pointer items-center justify-center border-none font-semibold whitespace-nowrap transition-colors duration-[120ms] ease-in-out {isActive
          ? 'bg-accent-soft text-accent'
          : 'text-text-secondary bg-transparent hover:bg-surface-hover'}"
        style="gap:9px;padding:12px 8px;border-radius:12px;font-size:14px"
      >
        <tab.icon size={17} strokeWidth={1.8} />
        {tab.label}
      </button>
    {/each}

    <span
      title="준비 중"
      class="text-text-muted flex flex-1 items-center justify-center font-semibold whitespace-nowrap"
      style="gap:9px;padding:12px 8px;border-radius:12px;font-size:14px;cursor:default"
    >
      <BarsIcon size={17} strokeWidth={1.8} />
      Insights
      <span
        class="bg-track text-text-muted font-semibold"
        style="font-size:10.5px;border-radius:5px;padding:3px 6px"
      >
        준비 중
      </span>
    </span>
  </div>
</nav>
