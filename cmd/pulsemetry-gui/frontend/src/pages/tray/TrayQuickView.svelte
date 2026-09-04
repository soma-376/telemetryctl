<script lang="ts">
  import { Events } from "@wailsio/runtime";
  import RecentSessions from "./components/RecentSessions.svelte";
  import TrayFooter from "./components/TrayFooter.svelte";
  import TrayHeader from "./components/TrayHeader.svelte";
  import TraySettingsMenu from "./components/TraySettingsMenu.svelte";
  import VendorLimits from "./components/VendorLimits.svelte";
  import QuitDialog from "$lib/components/dialog/QuitDialog.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import { TrayState, type TrayQuery } from "$lib/bindings";
  import { localTimeZone } from "$lib/utils/timezone";
  import {
    trayQuery,
    trayRefreshMutation,
    traySyncMutation,
  } from "$lib/query/tray";
  import { toTrayView } from "./adapter";

  let settingsOpen = $state(false);
  let quitOpen = $state(false);

  // 종료 진입점이 푸터 버튼과 설정 메뉴 두 곳이라 확인 모달 진입을 한곳으로 모은다.
  function requestQuit() {
    settingsOpen = false;
    quitOpen = true;
  }

  // tz 는 "오늘" 의 경계다. 창이 떠 있는 동안 시간대가 바뀔 일은 없으니 한 번만 읽는다.
  const QUERY: TrayQuery = { tz: localTimeZone(), recent_limit: 5 };

  // 퀵뷰는 닫혀도 파괴되지 않고 숨겨질 뿐이다. document.visibilityState 는 네이티브 창의
  // Hide/Show 와 같은 계약이 아니므로 Wails 창 이벤트로 폴링 수명을 제어한다 (ADR 0015).
  let visible = $state(false);

  const tray = trayQuery(QUERY, () => visible);
  const sync = traySyncMutation(QUERY);
  const refresh = trayRefreshMutation(QUERY);

  $effect(() => {
    const offShow = Events.On("tray:shown", () => {
      visible = true;
      // 창이 열린 순간은 갱신을 건다. 폴링은 읽기만 한다 — 폴링까지 갱신으로 두면 창을
      // 열어둔 채 두는 것만으로 벤더 호출이 계속 나간다 (ADR 0014).
      sync.mutate();
    });
    const offHide = Events.On("tray:hidden", () => {
      visible = false;
    });
    return () => {
      visible = false;
      offShow();
      offHide();
    };
  });

  // 창 열기가 벤더 조회까지 갈 수 있어(ADR 0014) 수십 초가 걸릴 수 있다. 그동안 헤더가 낡은
  // "N분 전" 을 아무 표시 없이 들고 있지 않도록 갱신 중임을 알린다. 버튼은 자기 스피너가 있다.
  const syncing = $derived(sync.isPending);
  // 폴링도 같은 표시를 공유한다. 세 경로(버튼·창 열기·폴링)가 헤더에서 한 상태로 모인다.
  const fetching = $derived(tray.isFetching);

  const view = $derived(tray.data ? toTrayView(tray.data) : null);
  const failure = $derived(tray.error ? String(tray.error) : "");

  const shown = $derived(view);
  const notInstalled = $derived(
    shown?.monitoring.state === TrayState.StateNotInstalled,
  );
</script>

<div
  class="bg-bg relative flex h-screen flex-col overflow-hidden"
  style="animation:trayIn 180ms cubic-bezier(0.32,0.72,0,1)"
>
  <TrayHeader
    fetchedAt={tray.dataUpdatedAt}
    trayState={shown?.monitoring.state}
    {syncing}
    {fetching}
    onRefresh={async () => {
      // 헤더가 이 약속을 기다려 스피너 최소 표시 시간을 맞춘다. 스냅샷은 캐시로 들어가므로
      // (query/tray.ts) 여기서 받을 것이 없다.
      await refresh.mutateAsync();
    }}
  />

  <main class="tray-scroll min-h-0 flex-1 overflow-y-auto">
    {#if tray.isPending}
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
  <h1 class="text-6xl">{visible}</h1>
  <QuitDialog open={quitOpen} onClose={() => (quitOpen = false)} />
</div>
