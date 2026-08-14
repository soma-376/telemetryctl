<script lang="ts">
  import { period, periodRangeText, deltaNoun } from "../../lib/period.svelte";
  import { formatTokens } from "../../lib/format";
  import {
    AGENT_NAMES,
    agentsFor,
    connection,
    headlineFor,
    insightsFor,
    liveTokensToday,
    summaryFor,
    topSessions,
  } from "./data";
  import Header from "./components/Header.svelte";
  import PageTitle from "./components/PageTitle.svelte";
  import MetricRow from "./components/MetricRow.svelte";
  import AgentUsagePanel from "./components/AgentUsagePanel.svelte";
  import InsightsPanel from "./components/InsightsPanel.svelte";
  import TopSessionsPanel from "./components/TopSessionsPanel.svelte";
  import { type Tab } from "./components/BottomNav.svelte";

  let { onNavigate }: { onNavigate?: (tab: Tab) => void } = $props();
  const go = (tab: Tab) => onNavigate?.(tab);

  // ② 기간 스코프 — period 변경에 반응
  const p = $derived(period.value);
  const summary = $derived(summaryFor(p));
  const agents = $derived(agentsFor(p));
  const sessions = $derived(topSessions(p, 6));
  const insight = $derived(insightsFor(p));
  const mascot = $derived(headlineFor(p));

  // ① 라이브 — pill의 "tokens today"는 항상 오늘 기준
  const tokensToday = formatTokens(liveTokensToday);
</script>

<Header
  online={connection.online}
  activeAgents={connection.activeAgents}
  {tokensToday}
/>

<main
  class="mx-auto w-full flex-1"
  style="max-width:1520px;padding:12px 28px 6px"
>
  <PageTitle label={periodRangeText(p)} />

  <MetricRow {summary} {mascot} deltaNoun={deltaNoun(p)} />

  <div
    class="grid"
    style="grid-template-columns:minmax(0,0.9fr) minmax(0,1.1fr);gap:14px;margin-top:14px"
  >
    <AgentUsagePanel {agents} onAllAgents={() => go("settings")} />
    <TopSessionsPanel
      {sessions}
      agentNames={AGENT_NAMES}
      onViewAll={() => go("activity")}
    />
  </div>

  <div style="margin-top:14px">
    <InsightsPanel
      weeklyPattern={insight.weeklyPattern}
      patternLabel={insight.patternLabel}
      patternBody={insight.patternBody}
      tiredMsg={insight.tiredMsg}
      onMore={() => go("insights")}
    />
  </div>
</main>
