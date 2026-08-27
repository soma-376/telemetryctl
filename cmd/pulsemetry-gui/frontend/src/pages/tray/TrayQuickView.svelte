<script lang="ts">
  import RecentSessions from "./components/RecentSessions.svelte";
  import TrayFooter from "./components/TrayFooter.svelte";
  import TrayHeader from "./components/TrayHeader.svelte";
  import TrayInsight from "./components/TrayInsight.svelte";
  import TraySettingsMenu from "./components/TraySettingsMenu.svelte";
  import VendorLimits from "./components/VendorLimits.svelte";
  import QuitDialog from "../../lib/components/dialog/QuitDialog.svelte";
  import { TRAY_SESSIONS, TRAY_SYNCED_TEXT, TRAY_VENDORS } from "./data";

  let settingsOpen = $state(false);
  let quitOpen = $state(false);

  // 종료 진입점이 푸터 버튼과 설정 메뉴 두 곳이라 확인 모달 진입을 한곳으로 모은다.
  function requestQuit() {
    settingsOpen = false;
    quitOpen = true;
  }
</script>

<div
  class="bg-bg relative flex h-screen flex-col overflow-hidden"
  style="animation:trayIn 180ms cubic-bezier(0.32,0.72,0,1)"
>
  <TrayHeader syncedText={TRAY_SYNCED_TEXT} />

  <main class="tray-scroll min-h-0 flex-1 overflow-y-auto">
    <VendorLimits vendors={TRAY_VENDORS} />
    <RecentSessions sessions={TRAY_SESSIONS} />
    <TrayInsight />
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
