<script lang="ts">
  import { period, periodRangeText, deltaNoun } from "../../lib/period.svelte";
  import { formatTokens } from "../../lib/format";
  import {
    connection,
    liveTokensToday,
    summaryFor,
    vendorSyncedText,
    vendorUsage,
  } from "./data";
  import Header from "./components/Header.svelte";
  import PageTitle from "./components/PageTitle.svelte";
  import MetricRow from "./components/MetricRow.svelte";
  import VendorLimitsPanel from "./components/VendorLimitsPanel.svelte";

  let { onOpenSettings }: { onOpenSettings?: () => void } = $props();

  // ② 기간 스코프 — period 변경에 반응
  const p = $derived(period.value);
  const summary = $derived(summaryFor(p));

  // ① 라이브 — pill의 "tokens today"는 항상 오늘 기준
  const tokensToday = formatTokens(liveTokensToday);
</script>

<Header
  online={connection.online}
  activeAgents={connection.activeAgents}
  {tokensToday}
  {onOpenSettings}
/>

<main
  class="mx-auto w-full flex-1"
  style="max-width:var(--page-max-width);padding:14px 32px 10px"
>
  <PageTitle label={periodRangeText(p)} />

  <MetricRow {summary} deltaNoun={deltaNoun(p)} />

  <div style="margin-top:14px">
    <VendorLimitsPanel vendors={vendorUsage} syncedText={vendorSyncedText} />
  </div>
</main>
