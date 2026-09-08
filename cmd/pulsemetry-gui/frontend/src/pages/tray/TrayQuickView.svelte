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
  const refresh = trayRefreshMutation(QUERY);

  $effect(() => {
    const offShow = Events.On("tray:shown", () => {
      visible = true;
      // 창 열기는 저장된 값을 즉시 읽기만 한다. 진행 중인 조회가 있으면 공유한다.
      void tray.refetch({ cancelRefetch: false });
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

  // 창 열기와 폴링은 로컬 조회 상태만 공유한다.
  const fetching = $derived(tray.isFetching);

  const view = $derived(tray.data ? toTrayView(tray.data) : null);
  const failure = $derived(tray.error || refresh.error ? String(tray.error ?? refresh.error) : "");

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
    observedAt={tray.data?.limits_observed_at ?? ""}
    trayState={shown?.monitoring.state}
    {fetching}
    onRefresh={async () => {
      // 헤더가 이 약속을 기다려 스피너 최소 표시 시간을 맞춘다. 스냅샷은 캐시로 들어가므로
      // (query/tray.ts) 여기서 받을 것이 없다.
      try {
        await refresh.mutateAsync();
      } catch {
        // 오류는 mutation 상태로 표시하고 마지막 정상 데이터를 유지한다.
      }
    }}
  />

  {#if failure && shown}
    <p role="status" class="text-text-secondary px-4 py-2 text-xs">
      최신 상태를 확인하지 못했습니다. 마지막 조회 결과를 표시합니다.
    </p>
  {/if}

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

  <QuitDialog open={quitOpen} onClose={() => (quitOpen = false)} />
</div>
