<script lang="ts">
  import Mascot from "../../../lib/components/Mascot.svelte";
  import PeriodPicker from "./PeriodPicker.svelte";
  import BellIcon from "../../../lib/icons/BellIcon.svelte";
  import GearIcon from "../../../lib/icons/GearIcon.svelte";
  import SlidersIcon from "../../../lib/icons/SlidersIcon.svelte";

  let {
    online = true,
    activeAgents,
    tokensToday,
    onOpenSettings,
  }: {
    online?: boolean;
    activeAgents: number;
    tokensToday: string;
    onOpenSettings?: () => void;
  } = $props();
</script>

<header
  class="mx-auto grid w-full items-center"
  style="max-width:var(--page-max-width);padding:18px 32px 0;grid-template-columns:1fr auto 1fr;gap:20px"
>
  <div class="flex items-center" style="gap:12px">
    <Mascot pose="view-front" height={42} />
    <div class="flex flex-col" style="gap:4px">
      <div class="text-text font-bold" style="font-size:18px;letter-spacing:-0.01em">
        Pulsemetry
      </div>
      <div
        class="text-text-secondary flex items-center"
        style="font-size:12px;gap:6px"
      >
        <span
          class="inline-block"
          style="width:7px;height:7px;border-radius:50%;background:{online
            ? 'var(--color-success)'
            : 'var(--color-inactive)'}"
        ></span>
        {online ? "모니터링 중" : "연결 끊김"}
      </div>
    </div>
  </div>

  <div
    class="bg-surface border-border text-text flex items-center rounded-full border whitespace-nowrap"
    style="gap:9px;padding:10px 18px;font-size:13.5px"
  >
    <span
      class="inline-block"
      style="width:8px;height:8px;border-radius:50%;background:{online
        ? 'var(--color-success)'
        : 'var(--color-inactive)'}"
    ></span>
    <span class="font-semibold">{activeAgents} agents active</span>
    <span class="text-text-muted">•</span>
    <span class="text-text-secondary">{tokensToday} tokens today</span>
  </div>

  <div class="text-text flex items-center justify-end" style="gap:14px">
    <PeriodPicker />
    <BellIcon size={19} />
    <GearIcon size={19} strokeWidth={1.7} />
    <button
      type="button"
      title="설정"
      onclick={onOpenSettings}
      class="text-text flex cursor-pointer items-center border-none bg-transparent p-0"
    >
      <SlidersIcon size={19} />
    </button>
  </div>
</header>
