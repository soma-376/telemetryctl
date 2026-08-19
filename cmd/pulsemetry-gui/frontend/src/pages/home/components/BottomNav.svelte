<script lang="ts">
  import HomeIcon from "../../../lib/icons/HomeIcon.svelte";
  import ListIcon from "../../../lib/icons/ListIcon.svelte";
  import BarsIcon from "../../../lib/icons/BarsIcon.svelte";
  import GearIcon from "../../../lib/icons/GearIcon.svelte";

  export type Tab = "overview" | "activity" | "insights" | "settings";

  let {
    active = "overview",
    onSelect,
  }: { active?: Tab; onSelect?: (tab: Tab) => void } = $props();

  const TABS = [
    { id: "overview", label: "Home", icon: HomeIcon },
    { id: "activity", label: "Activity", icon: ListIcon },
    { id: "insights", label: "Insights", icon: BarsIcon },
    { id: "settings", label: "Settings", icon: GearIcon },
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
  </div>
</nav>
