<script lang="ts">
  import { openMainWindow } from "$lib/ipc/app";
  import SlidersIcon from "$lib/icons/SlidersIcon.svelte";
  import PowerIcon from "$lib/icons/PowerIcon.svelte";

  let {
    settingsOpen,
    onSettings,
    onRequestQuit,
  }: {
    settingsOpen: boolean;
    onSettings: () => void;
    onRequestQuit: () => void;
  } = $props();
</script>

<footer
  class="bg-surface flex flex-none items-center"
  style="gap:8px;padding:10px 14px;border-top:1px solid #ede7de"
>
  <button
    type="button"
    onclick={openMainWindow}
    class="bg-accent hover:bg-accent-hover flex cursor-pointer items-center justify-center border-none font-semibold whitespace-nowrap transition-colors duration-[120ms] ease-in-out"
    style="flex:1;gap:8px;border-radius:9px;padding:10px;font-size:12.5px;color:var(--color-surface)"
    >Pulsemetry 열기</button
  >
  <button
    type="button"
    title="트레이 설정"
    aria-expanded={settingsOpen}
    onclick={onSettings}
    class="text-text-secondary hover:border-border-strong hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border transition-colors duration-[120ms] ease-in-out"
    style="width:34px;height:34px;border-radius:9px;border-color:{settingsOpen
      ? 'var(--color-border-strong)'
      : 'var(--color-border)'};background:{settingsOpen
      ? 'var(--color-surface-hover)'
      : 'transparent'}"
  >
    <SlidersIcon size={15} strokeWidth={1.8} knobFill="var(--color-surface)" />
  </button>
  <button
    type="button"
    title="종료"
    onclick={onRequestQuit}
    class="border-border text-text-secondary hover:border-border-strong hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border bg-transparent transition-colors duration-[120ms] ease-in-out"
    style="width:34px;height:34px;border-radius:9px"
  >
    <PowerIcon size={15} strokeWidth={1.8} />
  </button>
</footer>
