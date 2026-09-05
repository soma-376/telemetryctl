<script lang="ts">
  import { Events } from "@wailsio/runtime";
  import { onMount } from "svelte";
  import RecentSessions from "./components/RecentSessions.svelte";
  import TrayFooter from "./components/TrayFooter.svelte";
  import TrayHeader from "./components/TrayHeader.svelte";
  import TraySettingsMenu from "./components/TraySettingsMenu.svelte";
  import VendorLimits from "./components/VendorLimits.svelte";
  import QuitDialog from "$lib/components/dialog/QuitDialog.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import DaemonDown from "$lib/components/DaemonDown.svelte";
  import { TrayState, type TrayQuery } from "$lib/bindings";
  import { localTimeZone } from "$lib/utils/timezone";
  import {
    trayQuery,
    trayRefreshMutation,
    traySyncMutation,
  } from "$lib/query/tray";
  import {
    bindRetry,
    noteFailure,
    noteSuccess,
    reconnect,
  } from "$lib/domain/reconnect.svelte";
  import { toTrayView } from "./adapter";
  import { isTrayVisible } from "$lib/ipc/app";

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

  let visibilitySync = 0;
  async function syncVisibility() {
    const currentSync = ++visibilitySync;
    const current = await isTrayVisible();
    // 마운트 조회와 show/hide 조회가 겹쳐도 늦게 끝난 과거 응답이 최신 상태를 덮지 않는다.
    if (currentSync === visibilitySync) visible = current;
  }

  // show/hide 이벤트는 상태 전이만 알려준다. 절전 복귀로 WebView가 다시 만들어질 때
  // 네이티브 창은 계속 보이는 상태라 새 show 이벤트가 없으므로 현재 상태를 직접 복구한다.
  onMount(() => {
    void syncVisibility();
  });

  $effect(() => {
    const offShow = Events.On("tray:shown", () => {
      void syncVisibility();
      // 창이 열린 순간은 갱신을 건다. 폴링은 읽기만 한다 — 폴링까지 갱신으로 두면 창을
      // 열어둔 채 두는 것만으로 벤더 호출이 계속 나간다 (ADR 0014).
      sync.mutate();
    });
    const offHide = Events.On("tray:hidden", () => {
      void syncVisibility();
    });
    return () => {
      visibilitySync++;
      visible = false;
      offShow();
      offHide();
    };
  });

  // 조회의 성공·실패를 연결 상태에 알린다.
  //
  // 이벤트 시각(dataUpdatedAt·errorUpdatedAt)으로 판정하는 이유는 **한 사건을 한 번만**
  // 세기 위해서다. isError 같은 불리언을 보면 다른 이유로 이 효과가 다시 돌 때마다 실패가
  // 중복 집계되어 끊김 판정이 실제보다 빨라진다.
  let seenErrorAt = 0;
  let seenDataAt = 0;
  $effect(() => {
    // 재시도는 조회를 다시 부르는 것이다. 데몬을 띄우는 경로는 GUI 에 없다.
    bindRetry(() => tray.refetch());
    if (tray.errorUpdatedAt > seenErrorAt) {
      seenErrorAt = tray.errorUpdatedAt;
      noteFailure();
    }
    if (tray.dataUpdatedAt > seenDataAt) {
      seenDataAt = tray.dataUpdatedAt;
      noteSuccess();
    }
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
    disconnected={reconnect.down}
    onRefresh={async () => {
      // 헤더가 이 약속을 기다려 스피너 최소 표시 시간을 맞춘다. 스냅샷은 캐시로 들어가므로
      // (query/tray.ts) 여기서 받을 것이 없다.
      await refresh.mutateAsync();
    }}
  />

  <main class="tray-scroll min-h-0 flex-1 overflow-y-auto">
    <!-- 끊김을 가장 먼저 본다. 캐시에 마지막 스냅샷이 남아 있어(ADR 0015) 아래 갈래는
         "데이터가 있다" 로 보이지만, 그것은 지금의 사실이 아니다. -->
    {#if reconnect.down}
      <DaemonDown />
    {:else if tray.isPending}
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
