<script lang="ts">
  import { openMainSettings, quitApp } from "../../../lib/backend";
  import { TRAY_OPTIONS } from "../data";
  import type { TrayOptionKey } from "../types";

  let { onClose }: { onClose: () => void } = $props();
  let optionOn = $state<Record<TrayOptionKey, boolean>>({
    notify: true,
    badge: true,
    hideIdle: false,
    launch: true,
  });

  function openSettings() {
    onClose();
    openMainSettings();
  }
</script>

<button
  type="button"
  aria-label="메뉴 닫기"
  onclick={onClose}
  class="absolute cursor-default border-none"
  style="inset:0;background:rgba(27,26,24,0.10)"
></button>
<div
  class="bg-surface absolute border"
  role="menu"
  style="right:12px;bottom:56px;width:244px;border-color:#dfd8ce;border-radius:12px;box-shadow:0 10px 28px rgba(27,26,24,0.16);padding:6px;animation:menuIn 160ms cubic-bezier(0.32,0.72,0,1)"
>
  {#each TRAY_OPTIONS as option (option.key)}
    <button
      type="button"
      onclick={() => (optionOn[option.key] = !optionOn[option.key])}
      class="hover:bg-surface-hover grid w-full cursor-pointer items-center border-none bg-transparent text-left"
      style="grid-template-columns:minmax(0,1fr) auto;gap:10px;padding:8px 10px;border-radius:9px"
    >
      <span style="min-width:0">
        <span class="text-text block truncate font-semibold" style="font-size:12.5px">
          {option.name}
        </span>
        {#if option.desc}
          <span
            class="text-text-muted block truncate"
            style="font-size:10.5px;margin-top:2px"
          >{option.desc}</span>
        {/if}
      </span>
      <span
        class="flex flex-none"
        style="width:34px;height:20px;border-radius:999px;padding:3px;background:{optionOn[
          option.key
        ]
          ? 'var(--color-accent)'
          : '#ddd6cc'};justify-content:{optionOn[option.key]
          ? 'flex-end'
          : 'flex-start'};transition:background 220ms cubic-bezier(0.32,0.72,0,1)"
      >
        <span
          class="block"
          style="width:14px;height:14px;border-radius:50%;background:#ffffff"
        ></span>
      </span>
    </button>
  {/each}

  <div style="height:1px;background:#f1ece4;margin:5px 8px"></div>

  <button
    type="button"
    onclick={openSettings}
    class="text-text hover:bg-surface-hover flex w-full cursor-pointer items-center border-none bg-transparent text-left font-semibold whitespace-nowrap"
    style="gap:9px;padding:9px 10px;border-radius:9px;font-size:12.5px"
  >
    <svg
      viewBox="0 0 24 24"
      class="flex-none"
      style="width:13px;height:13px"
      fill="none"
      stroke="#96918a"
      stroke-width="1.9"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path d="M14 5h5v5" />
      <path d="M19 5 11 13" />
      <path d="M17.5 14v4.5a1.5 1.5 0 0 1-1.5 1.5H6a1.5 1.5 0 0 1-1.5-1.5V8A1.5 1.5 0 0 1 6 6.5h4.5" />
    </svg>
    전체 설정 열기…
  </button>
  <button
    type="button"
    onclick={quitApp}
    class="flex w-full cursor-pointer items-center border-none bg-transparent text-left font-semibold whitespace-nowrap"
    style="gap:9px;padding:9px 10px;border-radius:9px;font-size:12.5px;color:var(--color-danger-strong)"
    onmouseenter={(event) => (event.currentTarget.style.background = "#fcf3f3")}
    onmouseleave={(event) => (event.currentTarget.style.background = "transparent")}
  >
    <svg
      viewBox="0 0 24 24"
      class="flex-none"
      style="width:13px;height:13px"
      fill="none"
      stroke="currentColor"
      stroke-width="1.9"
      stroke-linecap="round"
    >
      <path d="M12 4v8" />
      <path d="M7.5 7A7 7 0 1 0 16.5 7" />
    </svg>
    Pulsemetry 종료
  </button>
</div>
