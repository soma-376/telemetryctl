<script lang="ts">
  import Home from "./pages/home/Home.svelte";
  import Activity from "./pages/activity/Activity.svelte";
  import BottomNav, {
    type Tab,
  } from "./pages/home/components/BottomNav.svelte";

  // 라우터 없이 로컬 탭 상태로 페이지 전환. overview(Home)·activity(Activity) 구현,
  // insights·settings 는 플레이스홀더입니다.
  let activeTab = $state<Tab>("overview");
  const go = (tab: Tab) => (activeTab = tab);

  const STUB_LABEL: Record<"insights" | "settings", string> = {
    insights: "Insights",
    settings: "Settings",
  };
</script>

<!-- 앱 셸: 창 높이 고정(h-screen). 가운데 영역만 스크롤되고 하단 nav는 창 최하단에 고정. -->
<div class="flex h-screen min-w-[1024px] flex-col overflow-hidden bg-bg">
  <div class="flex flex-1 flex-col overflow-y-auto">
    {#if activeTab === "overview"}
      <Home onNavigate={go} />
    {:else if activeTab === "activity"}
      <Activity />
    {:else}
      <main class="flex flex-1 flex-col items-center justify-center gap-2">
        <div
          class="text-text font-bold"
          style="font-size:25px;letter-spacing:-0.02em"
        >
          {STUB_LABEL[activeTab as "insights" | "settings"]}
        </div>
        <div class="text-text-secondary" style="font-size:13.5px">
          아직 준비 중인 화면이야.
        </div>
      </main>
    {/if}
  </div>

  <BottomNav active={activeTab} onSelect={go} />
</div>
