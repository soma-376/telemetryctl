<script lang="ts">
  import { Events } from "@wailsio/runtime";
  import Home from "./pages/home/Home.svelte";
  import Activity from "./pages/activity/Activity.svelte";
  import Header from "./lib/components/shell/Header.svelte";
  import Nav from "./lib/components/shell/Nav.svelte";
  import type { AppSection } from "./lib/navigation";
  import SettingsModal from "./pages/settings/SettingsModal.svelte";
  import TrayQuickView from "./pages/tray/TrayQuickView.svelte";

  const isTray =
    new URLSearchParams(window.location.search).get("view") === "tray";

  let activeTab = $state<AppSection>("overview");
  let settingsOpen = $state(false);
  const go = (tab: AppSection) => (activeTab = tab);

  // 퀵뷰의 "트레이 설정" → Go 가 메인 창을 열고 이 이벤트를 쏜다.
  if (!isTray) {
    try {
      Events.On("open-settings", () => (settingsOpen = true));
    } catch {
      /* browser preview */
    }
  }
</script>

{#if isTray}
  <TrayQuickView />
{:else}
  <!-- 앱 셸: 창 높이 고정(h-screen). 가운데 영역만 스크롤되고 하단 nav는 창 최하단에 고정. -->
  <div
    class="flex h-screen min-w-(--page-min-width) flex-col overflow-hidden bg-bg"
  >
    <div class="flex flex-1 flex-col overflow-y-auto">
      <Header
        activeAgents={3}
        tokensToday="148k"
        onOpenSettings={() => (settingsOpen = true)}
      />

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
            Insights
          </div>
          <div class="text-text-secondary" style="font-size:13.5px">
            아직 준비 중인 화면이야.
          </div>
        </main>
      {/if}
    </div>

    <Nav active={activeTab} onSelect={go} />
  </div>

  <SettingsModal open={settingsOpen} onClose={() => (settingsOpen = false)} />
{/if}
