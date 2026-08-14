<script lang="ts">
  import Header from "../home/components/Header.svelte";
  import { period, periodRangeText } from "../../lib/period.svelte";
  import { SESSIONS, HEADER } from "./data";
  import Filters from "./Filters.svelte";
  import SessionTable from "./SessionTable.svelte";
  import SessionDetail from "./SessionDetail.svelte";

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
</script>

<svelte:window onkeydown={onKeydown} />

<Header
  online={HEADER.online}
  activeAgents={HEADER.activeAgents}
  tokensToday={HEADER.tokensToday}
/>

<main
  class="mx-auto w-full flex-1"
  style="max-width:1520px;padding:12px 28px 14px"
>
  <div class="flex items-end" style="gap:12px;margin-bottom:18px">
    <h1
      class="text-text m-0 font-bold"
      style="font-size:37px;letter-spacing:-0.035em;line-height:1"
    >
      Activity
    </h1>
    <div class="text-text-secondary" style="font-size:14px;padding-bottom:6px">
      ({rangeText})
    </div>
  </div>

  <div style="margin-bottom:14px">
    <Filters />
  </div>

  <SessionTable sessions={SESSIONS} selectedIndex={selected} onOpen={open} />
</main>

<SessionDetail
  open={selected !== null}
  index={selected}
  onClose={close}
  onPrev={() => step(-1)}
  onNext={() => step(1)}
/>
