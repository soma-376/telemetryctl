<script lang="ts">
  import { period, periodRangeText } from "$lib/domain/period.svelte";
  import { SESSIONS } from "./mock";
  import Filters from "./components/Filters.svelte";
  import SessionTable from "./components/SessionTable.svelte";
  import SessionDetail from "./components/SessionDetail.svelte";

  let selected = $state<number | null>(null);

  const open = (i: number) => (selected = i);
  const close = () => (selected = null);
  const step = (dir: number) => {
    const k = SESSIONS.length;
    selected = selected === null ? 0 : (selected + dir + k) % k;
  };

  function onKeydown(e: KeyboardEvent) {
    if (selected === null) return;
    if (e.key === "Escape") {
      e.preventDefault();
      close();
    } else if (e.key === "j" || e.key === "J" || e.key === "ArrowDown") {
      e.preventDefault();
      step(1);
    } else if (e.key === "k" || e.key === "K" || e.key === "ArrowUp") {
      e.preventDefault();
      step(-1);
    }
  }

  const rangeText = $derived(periodRangeText(period.value));

  // 드로어는 세션 자체와 "몇 번째인지" 만 알면 된다. 전체 목록을 아는 것은 이 화면이다.
  const detail = $derived(selected === null ? null : (SESSIONS[selected] ?? null));
  const detailPosition = $derived(
    selected === null ? "" : `${selected + 1} / ${SESSIONS.length}`,
  );
</script>

<svelte:window onkeydown={onKeydown} />

<main
  class="mx-auto w-full flex-1"
  style="max-width:var(--page-max-width);padding:14px 32px 10px"
>
  <div class="flex items-baseline" style="gap:12px;margin-bottom:14px">
    <h1
      class="text-text m-0 font-bold"
      style="font-size:38px;letter-spacing:-0.035em;line-height:1"
    >
      Activity
    </h1>
    <div class="text-text-muted" style="font-size:14px">({rangeText})</div>
  </div>

  <div style="margin-bottom:14px">
    <Filters />
  </div>

  <SessionTable sessions={SESSIONS} selectedIndex={selected} onOpen={open} />
</main>

<SessionDetail
  open={selected !== null}
  session={detail}
  position={detailPosition}
  onClose={close}
  onPrev={() => step(-1)}
  onNext={() => step(1)}
/>
