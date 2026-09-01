<script lang="ts">
  import Mascot from "$lib/components/ui/Mascot.svelte";
  import { hideCurrentWindow } from "$lib/ipc/app";
  import RefreshIcon from "$lib/icons/RefreshIcon.svelte";
  import XIcon from "$lib/icons/XIcon.svelte";
  import Dot from "$lib/components/ui/Dot.svelte";

  import { TrayState } from "$lib/ipc/dashboard";
  import { syncedText } from "../adapter";

  // 프롭 이름을 state 로 두면 안 된다. Svelte 는 선언된 변수 앞의 $ 를 스토어 구독으로
  // 읽으므로, 이 파일의 $state(...) 룬이 전부 "state 스토어" 로 해석돼 컴파일이 깨진다.
  let {
    limitsObservedAt,
    trayState,
    syncing = false,
    onRefresh,
  }: {
    limitsObservedAt: number;
    trayState?: TrayState;
    /** 창 열기로 시작된 갱신이 도는 중이다. 버튼을 누른 것과 달리 이 창이 시작하지 않았다. */
    syncing?: boolean;
    onRefresh?: () => Promise<void> | void;
  } = $props();

  let pulling = $state(false);

  // 버튼을 눌렀든 창이 열려서든, 갱신이 도는 동안은 경과 시간 대신 그 사실을 말한다.
  // 낡은 "N분 전" 옆에서 아무 일도 안 일어나는 것처럼 보이는 구간을 없앤다.
  const busy = $derived(pulling || syncing);

  // 한도를 마지막으로 확인한 뒤로 얼마나 지났는지다. 초를 세어 주지 않으면 처음 계산한
  // 값이 몇 시간이고 그대로 남는다 — 창을 닫아도 컴포넌트가 살아 있기 때문이다.
  let tick = $state(Date.now());
  $effect(() => {
    const id = setInterval(() => (tick = Date.now()), 1000);
    return () => clearInterval(id);
  });
  const synced = $derived(syncedText(limitsObservedAt, new Date(tick)));

  async function pull() {
    if (pulling) return;
    pulling = true;
    // 로컬 SQLite 조회는 대개 한 프레임 안에 끝난다. 최소 표시 시간을 함께 기다리지
    // 않으면 스피너가 번쩍이기만 해서 눌린 것인지 알 수 없다.
    try {
      await Promise.all([onRefresh?.(), new Promise((r) => setTimeout(r, 450))]);
    } finally {
      pulling = false;
    }
  }

  // 상태 줄. 아직 첫 조회가 끝나지 않았으면(undefined) 초록 점을 켜지 않는다 —
  // 확인하지 않은 것을 "모니터링 중" 이라고 말하면 안 된다.
  const status = $derived.by(() => {
    switch (trayState) {
      case TrayState.StateMonitoring:
        return { text: "모니터링 중", color: "var(--color-success)" };
      case TrayState.StatePaused:
        return { text: "수집 중지됨", color: "var(--color-inactive)" };
      case TrayState.StateNotInstalled:
        return { text: "설치되지 않음", color: "var(--color-inactive)" };
      default:
        return { text: "연결 중", color: "var(--color-inactive)" };
    }
  });
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
    <Dot size={6} color={status.color} />
    <span class="truncate">{status.text}</span>
  </span>
  <span class="flex-none whitespace-nowrap" style="font-size:12px;color:#b3aba0"
    >{busy ? "조회 중" : synced}</span
  >
  <button
    type="button"
    disabled={pulling}
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
