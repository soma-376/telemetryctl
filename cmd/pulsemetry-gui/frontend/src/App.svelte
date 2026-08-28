<script lang="ts">
  import { Events } from "@wailsio/runtime";
  import Home from "./pages/home/Home.svelte";
  import Activity from "./pages/activity/Activity.svelte";
  import Header from "$lib/components/shell/Header.svelte";
  import Nav from "$lib/components/shell/Nav.svelte";
  import type { AppSection } from "$lib/navigation";
  import { period } from "$lib/domain/period.svelte";
  import SettingsModal from "./pages/settings/SettingsModal.svelte";
  import QuitDialog from "$lib/components/dialog/QuitDialog.svelte";
  import TrayQuickView from "./pages/tray/TrayQuickView.svelte";

  const isTray =
    new URLSearchParams(window.location.search).get("view") === "tray";

  let activeTab = $state<AppSection>("overview");
  let settingsOpen = $state(false);
  let quitOpen = $state(false);
  const go = (tab: AppSection) => (activeTab = tab);

  // ── 본문 가장자리 페이드 ──────────────────────────────
  // 고정된 헤더·내비 경계에서 본문이 칼로 자른 듯 끊기지 않도록 위아래를 녹인다.
  // 끝에 닿은 쪽은 끈다 — 더 볼 게 없는데 흐리면 렌더 오류처럼 보인다.
  const FADE = 18;
  let scroller = $state<HTMLDivElement | null>(null);
  let fadeTop = $state(false);
  let fadeBottom = $state(false);

  function syncFade() {
    const el = scroller;
    if (!el) return;
    fadeTop = el.scrollTop > 0;
    // 화면 배율에 따라 바닥에서 1px 미만 오차가 남으므로 여유를 둔다.
    fadeBottom = el.scrollTop + el.clientHeight < el.scrollHeight - 1;
  }

  // 탭·기간이 바뀌면 본문 높이가 달라지므로 다시 잰다.
  $effect(() => {
    void activeTab;
    void period.value;
    syncFade();
  });

  const maskStyle = $derived.by(() => {
    if (!fadeTop && !fadeBottom) return "";
    const head = fadeTop ? `transparent 0, #000 ${FADE}px` : "#000 0";
    const tail = fadeBottom
      ? `#000 calc(100% - ${FADE}px), transparent 100%`
      : "#000 100%";
    const gradient = `linear-gradient(to bottom, ${head}, ${tail})`;
    return `-webkit-mask-image:${gradient};mask-image:${gradient}`;
  });

  // 퀵뷰의 "트레이 설정" → Go 가 메인 창을 열고 이 이벤트를 쏜다.
  if (!isTray) {
    try {
      Events.On("open-settings", () => (settingsOpen = true));
    } catch {
      /* browser preview */
    }
  }
</script>

<!-- 창 크기가 바뀌면 스크롤 여유가 달라지므로 페이드를 다시 잰다.
     (svelte:window 는 블록 안에 둘 수 없어 트레이 모드와 공유한다 — 그쪽은 scroller 가 없어 무시된다) -->
<svelte:window onresize={syncFade} />

{#if isTray}
  <TrayQuickView />
{:else}
  <!-- 앱 셸: 창 높이 고정(h-screen). 헤더와 하단 nav는 스크롤 영역 밖에 두어
       창에 고정되고, 가운데 본문만 스크롤된다. -->
  <div
    class="flex h-screen min-w-(--page-min-width) flex-col overflow-hidden bg-bg"
  >
    <Header
      activeAgents={3}
      tokensToday="148k"
      onOpenSettings={() => (settingsOpen = true)}
      onQuit={() => (quitOpen = true)}
    />

    <div
      bind:this={scroller}
      onscroll={syncFade}
      class="flex flex-1 flex-col overflow-y-auto"
      style={maskStyle}
    >
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
  <QuitDialog open={quitOpen} onClose={() => (quitOpen = false)} />
{/if}
