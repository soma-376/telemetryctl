<script lang="ts">
  import Mascot from "../Mascot.svelte";
  import BellIcon from "../../icons/BellIcon.svelte";
  import SlidersIcon from "../../icons/SlidersIcon.svelte";
  import DateRangePicker from "../period/DateRangePicker.svelte";

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
  class="mx-auto grid w-full flex-none items-center"
  style="max-width:var(--page-max-width);grid-template-columns:1fr auto 1fr;gap:20px;padding:14px 32px 0"
>
  <div class="flex items-center" style="gap:12px">
    <Mascot pose="view-front" height={42} />
    <div class="flex flex-col" style="gap:4px">
      <div class="text-text font-bold" style="font-size:18px;letter-spacing:-0.01em">
        Pulsemetry
      </div>
      <div
        class="text-text-secondary flex items-center"
        style="gap:6px;font-size:12px"
      >
        <span
          class="inline-block"
          style="width:7px;height:7px;border-radius:50%;background:{online
            ? 'var(--color-success)'
            : 'var(--color-inactive)'}"
        ></span>{online ? "모니터링 중" : "연결 끊김"}
      </div>
    </div>
  </div>

  <div
    class="bg-surface border-border flex items-center border whitespace-nowrap"
    style="gap:9px;border-radius:999px;padding:8px 16px;font-size:13.5px"
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
    <DateRangePicker />
    <span class="text-text-secondary" title="알림 (준비 중)" style="cursor:default">
      <BellIcon size={19} strokeWidth={1.7} />
    </span>
    <button
      type="button"
      title="설정"
      onclick={onOpenSettings}
      class="text-text flex cursor-pointer items-center justify-center border-none bg-transparent"
      style="padding:0"
    >
      <SlidersIcon size={19} strokeWidth={1.7} knobFill="var(--color-bg)" />
    </button>
  </div>
</header>
