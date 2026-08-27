<script lang="ts">
  import Mascot from "../../../lib/components/Mascot.svelte";
  import { hideCurrentWindow } from "../../../lib/backend";
  import RefreshIcon from "../../../lib/icons/RefreshIcon.svelte";
  import XIcon from "../../../lib/icons/XIcon.svelte";
  import Dot from "../../../lib/components/Dot.svelte";

  let { syncedText }: { syncedText: string } = $props();
  let pulling = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;

  function pull() {
    if (pulling) return;
    pulling = true;
    timer = setTimeout(() => (pulling = false), 1800);
  }

  $effect(() => () => clearTimeout(timer));
</script>

<header
  class="bg-surface flex flex-none items-center"
  style="gap:10px;padding:12px 14px;border-bottom:1px solid #ede7de"
>
  <Mascot pose="view-front" height={26} />
  <span
    class="text-text flex-none font-bold"
    style="font-size:13.5px;letter-spacing:-0.01em">Pulsemetry</span
  >
  <span
    class="text-text-secondary flex items-center"
    style="gap:5px;font-size:11px;flex:1;min-width:0"
  >
    <Dot size={6} color="var(--color-success)" />
    <span class="truncate">모니터링 중</span>
  </span>
  <span class="flex-none whitespace-nowrap" style="font-size:12px;color:#b3aba0"
    >{pulling ? "조회 중" : syncedText}</span
  >
  <button
    type="button"
    title={pulling ? "조회 중" : "새로고침"}
    onclick={pull}
    class="flex flex-none items-center justify-center border bg-transparent transition-[opacity,border-color] duration-[180ms] ease-in-out {pulling
      ? 'text-text-muted cursor-default'
      : 'text-accent hover:border-border-strong hover:bg-surface-hover cursor-pointer'}"
    style="width:30px;height:30px;border-radius:9px;border-color:{pulling
      ? '#efe9e1'
      : 'var(--color-border)'};opacity:{pulling ? '0.6' : '1'}"
  >
    <RefreshIcon
      size={15}
      strokeWidth={2.2}
      style="animation:{pulling
        ? 'spin 900ms linear infinite'
        : 'none'};transform-origin:50% 50%"
    />
  </button>
  <button
    type="button"
    title="퀵뷰 닫기"
    aria-label="퀵뷰 닫기"
    onclick={hideCurrentWindow}
    class="border-border text-text-muted hover:text-text hover:border-border-strong hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border bg-transparent transition-colors duration-[120ms] ease-in-out"
    style="width:30px;height:30px;border-radius:9px"
  >
    <XIcon size={15} strokeWidth={1.9} />
  </button>
</header>
