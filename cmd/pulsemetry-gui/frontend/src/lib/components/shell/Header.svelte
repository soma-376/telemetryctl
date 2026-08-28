<script lang="ts">
  import Mascot from "$lib/components/ui/Mascot.svelte";
  import Dot from "$lib/components/ui/Dot.svelte";
  import BellIcon from "../../icons/BellIcon.svelte";
  import SlidersIcon from "../../icons/SlidersIcon.svelte";
  import PowerIcon from "../../icons/PowerIcon.svelte";
  import DateRangePicker from "../period/DateRangePicker.svelte";

  let {
    online = true,
    activeAgents,
    tokensToday,
    onOpenSettings,
    onQuit,
  }: {
    online?: boolean;
    activeAgents: number;
    tokensToday: string;
    onOpenSettings?: () => void;
    onQuit?: () => void;
  } = $props();

  // 헤더가 좁아지면 가운데 칩이 날짜 선택기를 밀어낸다. 칩은 같은 숫자가 Home
  // 히어로에 크게 나오는 보조 정보라 먼저 줄인다 — 조작하는 컨트롤이 잘리는 것보다 낫다.
  // 뷰포트가 아니라 헤더 자신의 폭을 재는 이유: 헤더는 1080px 에서 멈춘다.
  let headerWidth = $state(0);
  const compact = $derived(headerWidth > 0 && headerWidth < 950);
</script>

<!-- 하단 Nav 와 같은 언어: 배경 위에 떠 있는 카드 한 장.
     바깥 여백은 Nav(12/32/18)를 뒤집어 창 가장자리 18, 본문 쪽 12로 대칭을 맞춘다. -->
<header
  class="mx-auto w-full flex-none"
  style="max-width:var(--page-max-width);padding:18px 32px 12px"
>
  <div
    bind:clientWidth={headerWidth}
    class="bg-surface border-border grid items-center border"
    style="grid-template-columns:1fr auto 1fr;gap:20px;border-radius:16px;padding:8px 14px"
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
          <Dot
            color={online ? "var(--color-success)" : "var(--color-inactive)"}
          />{online ? "모니터링 중" : "연결 끊김"}
        </div>
      </div>
    </div>

    <!-- 카드 위에서는 흰 칩이 묻히므로 배경색으로 눌러 넣은 칩이 된다 -->
    <div
      class="bg-bg border-border flex items-center border whitespace-nowrap"
      style="gap:9px;border-radius:999px;padding:8px 16px;font-size:13.5px"
    >
      <Dot
        size={8}
        color={online ? "var(--color-success)" : "var(--color-inactive)"}
      />
      <span class="font-semibold">
        {activeAgents}{compact ? " agents" : " agents active"}
      </span>
      <span class="text-text-muted">•</span>
      <span class="text-text-secondary">
        {tokensToday}{compact ? "" : " tokens today"}
      </span>
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
        <SlidersIcon size={19} strokeWidth={1.7} knobFill="var(--color-surface)" />
      </button>
      <button
        type="button"
        title="종료"
        onclick={onQuit}
        class="text-text-secondary hover:text-danger-strong flex cursor-pointer items-center justify-center border-none bg-transparent transition-colors duration-[120ms] ease-in-out"
        style="padding:0"
      >
        <PowerIcon size={19} strokeWidth={1.7} />
      </button>
    </div>
  </div>
</header>
