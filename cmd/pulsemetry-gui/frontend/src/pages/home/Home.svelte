<script lang="ts">
  import { period, toDate } from "../../lib/period.svelte";
  import { heroData, vendorRows, buildActivity, longLabel } from "./chart";
  import UsageHero from "./components/UsageHero.svelte";
  import VendorTable from "./components/VendorTable.svelte";
  import ActivityList from "./components/ActivityList.svelte";
  import type { AppSection } from "../../lib/navigation";

  let {
    onNavigate,
  }: {
    onNavigate?: (tab: AppSection) => void;
  } = $props();

  // 기간 스코프 — period(전역 날짜 범위) 변경에 전부 반응한다
  const p = $derived(period.value);
  const hero = $derived(heroData(p.start, p.end));
  const vendors = $derived(vendorRows(hero));
  const activity = $derived(buildActivity(p.start, p.end));

  const rangeLong = $derived.by(() => {
    if (p.start === p.end) return longLabel(p.start);
    const sameMonth = toDate(p.start).getMonth() === toDate(p.end).getMonth();
    return sameMonth
      ? `${longLabel(p.start)} ~ ${toDate(p.end).getDate()}일`
      : `${longLabel(p.start)} ~ ${longLabel(p.end)}`;
  });
</script>

<main
  class="mx-auto w-full flex-1"
  style="max-width:var(--page-max-width);padding:14px 32px 6px"
>
  <div class="flex items-baseline" style="gap:12px;margin-bottom:14px">
    <h1
      class="text-text m-0 font-bold"
      style="font-size:38px;letter-spacing:-0.035em;line-height:1"
    >
      Home
    </h1>
    <div class="text-text-muted" style="font-size:14px">({rangeLong})</div>
  </div>

  <UsageHero {hero} />
  <VendorTable {vendors} />
  <ActivityList {activity} onViewAll={() => onNavigate?.("activity")} />
</main>
