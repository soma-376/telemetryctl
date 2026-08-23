<script lang="ts">
  import RecentSessions from "./components/RecentSessions.svelte";
  import TrayFooter from "./components/TrayFooter.svelte";
  import TrayHeader from "./components/TrayHeader.svelte";
  import TrayInsight from "./components/TrayInsight.svelte";
  import TraySettingsMenu from "./components/TraySettingsMenu.svelte";
  import VendorLimits from "./components/VendorLimits.svelte";
  import { TRAY_SESSIONS, TRAY_SYNCED_TEXT, TRAY_VENDORS } from "./data";

  let settingsOpen = $state(false);
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
  />

  {#if settingsOpen}
    <TraySettingsMenu onClose={() => (settingsOpen = false)} />
  {/if}
</div>
