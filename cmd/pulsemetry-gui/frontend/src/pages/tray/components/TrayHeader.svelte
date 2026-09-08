<script lang="ts">
  import Mascot from "$lib/components/ui/Mascot.svelte";
  import { hideCurrentWindow } from "$lib/ipc/app";
  import RefreshIcon from "$lib/icons/RefreshIcon.svelte";
  import XIcon from "$lib/icons/XIcon.svelte";
  import Dot from "$lib/components/ui/Dot.svelte";

  import { TrayState } from "$lib/bindings";
  import { observedAtText } from "../adapter";

  // 프롭 이름을 state 로 두면 안 된다. Svelte 는 선언된 변수 앞의 $ 를 스토어 구독으로
  // 읽으므로, 이 파일의 $state(...) 룬이 전부 "state 스토어" 로 해석돼 컴파일이 깨진다.
  let {
    observedAt,
    trayState,
    fetching = false,
    onRefresh,
  }: {
    /** 벤더 한도 관측 시각(RFC3339). 로컬 재조회 시각과 구분한다. */
    observedAt: string;
    trayState?: TrayState;
    /** 폴링을 포함해 조회가 나가 있다. */
    fetching?: boolean;
    onRefresh?: () => Promise<void> | void;
  } = $props();

  let pulling = $state(false);

  // 창 열기·폴링은 저장된 값 조회, pulling은 실제 벤더 갱신 요청이다.
  const busyNow = $derived(pulling || fetching);

  // 빠른 응답에도 버튼의 동작을 확인할 수 있게 최소 표시 시간을 둔다.
  const MIN_BUSY_MS = 450;
  let busy = $state(false);
  let busyUntil = 0;
  $effect(() => {
    if (busyNow) {
      busy = true;
      busyUntil = Date.now() + MIN_BUSY_MS;
      return;
    }
    const wait = busyUntil - Date.now();
    if (wait <= 0) {
      busy = false;
      return;
    }
    const id = setTimeout(() => (busy = false), wait);
    return () => clearTimeout(id);
  });

  // 절대 시각이라 다시 그릴 필요가 없다. observedAt 이 바뀔 때만 갱신된다.
  const synced = $derived(observedAtText(observedAt));

  // 최소 표시 시간은 busy 가 맡는다. 여기서는 실제 갱신만 기다린다.
  async function pull() {
    if (pulling) return;
    pulling = true;
    try {
      await onRefresh?.();
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
    >{busy ? "조회 중" : `${synced} 조회`}</span
  >
  <button
    type="button"
    disabled={busy}
    title={busy ? "조회 중" : "새로고침"}
    onclick={pull}
    class="flex flex-none items-center justify-center border bg-transparent transition-[opacity,border-color] duration-[180ms] ease-in-out {busy
      ? 'text-text-muted cursor-default'
      : 'text-accent hover:border-border-strong hover:bg-surface-hover cursor-pointer'}"
    style="width:30px;height:30px;border-radius:9px;border-color:{busy
      ? '#efe9e1'
      : 'var(--color-border)'};opacity:{busy ? '0.6' : '1'}"
  >
    <RefreshIcon
      size={15}
      strokeWidth={2.2}
      style="animation:{busy
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
