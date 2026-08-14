<script lang="ts">
  import Mascot from "../../../lib/components/Mascot.svelte";
  import PeriodPicker from "./PeriodPicker.svelte";
  import BellIcon from "../../../lib/icons/BellIcon.svelte";
  import GearIcon from "../../../lib/icons/GearIcon.svelte";

  let {
    online = true,
    activeAgents,
    tokensToday,
  }: { online?: boolean; activeAgents: number; tokensToday: string } = $props();
</script>

<header
  class="mx-auto grid w-full items-center"
  style="max-width:1520px;padding:18px 28px 8px;grid-template-columns:1fr auto 1fr;gap:20px"
>
  <div class="flex items-center" style="gap:12px">
    <Mascot pose="view-front" height={48} />
    <div class="flex flex-col" style="gap:4px">
      <div class="text-text font-bold" style="font-size:16px;letter-spacing:-0.01em">
        Pulsemetry
      </div>
      <div
        class="text-text-secondary flex items-center"
        style="font-size:11.5px;gap:6px"
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
    class="bg-surface border-border text-text flex items-center rounded-full border"
    style="gap:9px;padding:10px 18px;font-size:12.5px"
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

  <div class="text-text flex items-center justify-end" style="gap:16px">
    <PeriodPicker />
    <BellIcon />
    <GearIcon />
  </div>
</header>
