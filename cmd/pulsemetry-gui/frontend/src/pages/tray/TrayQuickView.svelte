<script lang="ts">
  import { Events } from "@wailsio/runtime";
  import RecentSessions from "./components/RecentSessions.svelte";
  import TrayFooter from "./components/TrayFooter.svelte";
  import TrayHeader from "./components/TrayHeader.svelte";
  import TrayInsight from "./components/TrayInsight.svelte";
  import TraySettingsMenu from "./components/TraySettingsMenu.svelte";
  import VendorLimits from "./components/VendorLimits.svelte";
  import QuitDialog from "$lib/components/dialog/QuitDialog.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import {
    fetchTray,
    localTimeZone,
    refreshTray,
    TrayState,
    type TrayQuery,
  } from "$lib/ipc/dashboard";
  import { toTrayView, type TrayView } from "./adapter";
  import { TRAY_SESSIONS, TRAY_SYNCED_AGO_SEC, TRAY_VENDORS } from "./mock";

  let settingsOpen = $state(false);
  let quitOpen = $state(false);

  // 종료 진입점이 푸터 버튼과 설정 메뉴 두 곳이라 확인 모달 진입을 한곳으로 모은다.
  function requestQuit() {
    settingsOpen = false;
    quitOpen = true;
  }

  // tz 는 "오늘" 의 경계다. 창이 떠 있는 동안 시간대가 바뀔 일은 없으니 한 번만 읽는다.
  const QUERY: TrayQuery = { tz: localTimeZone(), recent_limit: 5 };

  let view = $state<TrayView | null>(null);
  let failure = $state("");
  let loading = $state(true);

  async function load(fresh: boolean) {
    const r = await (fresh ? refreshTray(QUERY) : fetchTray(QUERY));
    loading = false;
    if (r.ok) {
      view = toTrayView(r.data);
      failure = "";
      return;
    }
    // 실패했을 때 직전 화면을 남겨 두지 않는다 — 낡은 숫자를 현재로 읽게 된다.
    view = null;
    failure = r.message;
  }

  // 데몬은 5분마다 한도를 갱신하는데, 여기서 다시 읽지 않으면 화면은 앱을 켠 순간의
  // 값을 계속 들고 있는다. 헤더의 "N분 전" 이 늘어나기만 하던 것이 그 증상이었다.
  //
  // 퀵뷰는 닫혀도 파괴되지 않고 숨겨질 뿐이다. document.visibilityState는 네이티브
  // 창의 Hide/Show와 같은 계약이 아니므로 Wails 창 이벤트로 폴링 수명을 제어한다.
  const POLL_MS = 60_000;
  $effect(() => {
    let visible = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const poll = async () => {
      if (!visible) return;
      await load(false);
      if (visible) timer = setTimeout(poll, POLL_MS);
    };

    const offShow = Events.On("tray:shown", () => {
      visible = true;
      clearTimeout(timer);
      void poll();
    });
    const offHide = Events.On("tray:hidden", () => {
      visible = false;
      clearTimeout(timer);
    });

    // 브라우저 프리뷰와 첫 Show 이벤트보다 늦게 리스너가 붙는 경우에도 초기 화면은 만든다.
    void load(false);
    return () => {
      visible = false;
      clearTimeout(timer);
      offShow();
      offHide();
    };
  });

  // 브라우저 프리뷰(vite dev 단독)에는 백엔드가 없다. 그때만 목데이터로 화면을 채운다.
  // 프로덕션 빌드에서는 이 갈래가 없어지므로 실제 장애가 정상 화면으로 위장되지 않는다.
  const PREVIEW = import.meta.env.DEV;
  const previewView: TrayView = {
    vendors: TRAY_VENDORS,
    unavailable: [],
    sessions: TRAY_SESSIONS,
    monitoring: {
      state: TrayState.StateMonitoring,
      database_available: true,
      daemon_running: true,
      daemon_stale: false,
      last_event_at: 0,
      running_sessions: TRAY_SESSIONS.length,
    },
    limitsObservedAt: Math.floor(Date.now() / 1000) - TRAY_SYNCED_AGO_SEC,
  };

  const shown = $derived(view ?? (failure && PREVIEW ? previewView : null));
  const notInstalled = $derived(
    shown?.monitoring.state === TrayState.StateNotInstalled,
  );
</script>

<div
  class="bg-bg relative flex h-screen flex-col overflow-hidden"
  style="animation:trayIn 180ms cubic-bezier(0.32,0.72,0,1)"
>
  <TrayHeader
    limitsObservedAt={shown?.limitsObservedAt ?? 0}
    trayState={shown?.monitoring.state}
    onRefresh={() => load(true)}
  />

  <main class="tray-scroll min-h-0 flex-1 overflow-y-auto">
    {#if loading}
      <!-- 첫 조회. 로컬 SQLite 조회라 보통 한 프레임 안에 끝나므로 스켈레톤을 두지 않는다.
           깜빡임을 만드는 쪽이 기다림보다 눈에 띈다. -->
    {:else if !shown}
      <EmptyState
        pose="warning"
        title="사용량을 불러오지 못했습니다"
        description={failure}
      />
    {:else if notInstalled}
      <EmptyState
        pose="no-data"
        title="아직 수집한 데이터가 없습니다"
        description="Pulsemetry 를 설치하고 AI 도구를 한 번 쓰면 여기에 사용량이 쌓입니다."
      />
    {:else}
      <VendorLimits vendors={shown.vendors} unavailable={shown.unavailable} />
      <RecentSessions sessions={shown.sessions} />
      <TrayInsight />
    {/if}
  </main>

  <TrayFooter
    {settingsOpen}
    onSettings={() => (settingsOpen = !settingsOpen)}
    onRequestQuit={requestQuit}
  />

  {#if settingsOpen}
    <TraySettingsMenu
      onClose={() => (settingsOpen = false)}
      onRequestQuit={requestQuit}
    />
  {/if}

  <QuitDialog open={quitOpen} onClose={() => (quitOpen = false)} />
</div>
