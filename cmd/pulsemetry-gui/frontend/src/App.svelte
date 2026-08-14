<script lang="ts">
  import Home from "./pages/home/Home.svelte";
  import BottomNav, {
    type Tab,
  } from "./pages/home/components/BottomNav.svelte";

  // overview(Home) 구현. activity·insights·settings 는 후속 티켓(PROJ-61~63) 플레이스홀더.
  let activeTab = $state<Tab>("overview");
  const go = (tab: Tab) => (activeTab = tab);

  const STUB_LABEL: Record<"activity" | "insights" | "settings", string> = {
    activity: "Activity",
    insights: "Insights",
    settings: "Settings",
  };
</script>

<div class="flex h-screen min-w-[1024px] flex-col overflow-hidden bg-bg">
  <div class="flex flex-1 flex-col overflow-y-auto">
    {#if activeTab === "overview"}
      <Home onNavigate={go} />
    {:else}
      <main class="flex flex-1 flex-col items-center justify-center gap-2">
        <div
          class="text-text font-bold"
          style="font-size:25px;letter-spacing:-0.02em"
        >
          {STUB_LABEL[activeTab as "activity" | "insights" | "settings"]}
        </div>
        <div class="text-text-secondary" style="font-size:13.5px">
          아직 준비 중인 화면이야.
        </div>
      </main>
    {/if}
  </div>

  <BottomNav active={activeTab} onSelect={go} />
</div>
