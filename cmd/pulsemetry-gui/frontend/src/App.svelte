<script lang="ts">
  import Home from "./pages/home/Home.svelte";
  import Activity from "./pages/activity/Activity.svelte";
  import BottomNav, {
    type Tab,
  } from "./pages/home/components/BottomNav.svelte";
  import SettingsModal from "./pages/settings/SettingsModal.svelte";

  // 라우터 없이 로컬 탭 상태로 페이지 전환. overview(Home)·activity(Activity) 구현,
  // insights 는 후속 티켓(PROJ-62) 플레이스홀더.
  // settings 는 페이지가 아니라 모달이다 — 탭을 눌러도 화면 전환 없이 모달만 연다.
  let activeTab = $state<Tab>("overview");
  let settingsOpen = $state(false);
  const go = (tab: Tab) => {
    if (tab === "settings") settingsOpen = true;
    else activeTab = tab;
  };
</script>

<!-- 앱 셸: 창 높이 고정(h-screen). 가운데 영역만 스크롤되고 하단 nav는 창 최하단에 고정. -->
<div
  class="flex h-screen min-w-(--page-min-width) flex-col overflow-hidden bg-bg"
>
  <div class="flex flex-1 flex-col overflow-y-auto">
    {#if activeTab === "overview"}
      <Home onOpenSettings={() => (settingsOpen = true)} />
    {:else if activeTab === "activity"}
      <Activity onOpenSettings={() => (settingsOpen = true)} />
    {:else}
      <main class="flex flex-1 flex-col items-center justify-center gap-2">
        <div
          class="text-text font-bold"
          style="font-size:25px;letter-spacing:-0.02em"
        >
          Insights
        </div>
        <div class="text-text-secondary" style="font-size:13.5px">
          아직 준비 중인 화면이야.
        </div>
      </main>
    {/if}
  </div>

  <BottomNav active={activeTab} onSelect={go} />
</div>

<SettingsModal open={settingsOpen} onClose={() => (settingsOpen = false)} />
