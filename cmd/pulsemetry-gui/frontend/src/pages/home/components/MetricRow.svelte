<script lang="ts">
  import type { Summary, MascotHeadline } from "../../../lib/types";
  import { formatTokens } from "../../../lib/format";
  import MetricCard from "./MetricCard.svelte";
  import Mascot from "../../../lib/components/Mascot.svelte";
  import ClockIcon from "../../../lib/icons/ClockIcon.svelte";
  import PulseIcon from "../../../lib/icons/PulseIcon.svelte";
  import WaveIcon from "../../../lib/icons/WaveIcon.svelte";

  let {
    summary,
    mascot,
    deltaNoun,
  }: { summary: Summary; mascot: MascotHeadline; deltaNoun: string } = $props();

  const arrow = (d: number) =>
    d > 0 ? `▲ ${d}%` : d < 0 ? `▼ ${Math.abs(d)}%` : "–";
  const note = (d: number) => (d === 0 ? "(변화 없음)" : `(${deltaNoun} 대비)`);
</script>

<div
  class="grid items-stretch"
  style="grid-template-columns:repeat(4,minmax(170px,1fr)) minmax(230px,420px);gap:14px"
>
  <MetricCard
    iconBg="var(--color-accent-soft)"
    iconColor="var(--color-accent)"
    value={summary.activeTime}
    label="AI 활동 시간"
    delta={arrow(summary.activeTimeDelta)}
    deltaColor="var(--color-accent)"
    deltaNote={note(summary.activeTimeDelta)}
  >
    {#snippet icon()}<ClockIcon />{/snippet}
  </MetricCard>

  <MetricCard
    iconBg="var(--color-info-soft)"
    iconColor="var(--color-info)"
    value={formatTokens(summary.tokens)}
    label="토큰 사용량"
    delta={arrow(summary.tokensDelta)}
    deltaColor="var(--color-info)"
    deltaNote={note(summary.tokensDelta)}
  >
    {#snippet icon()}<PulseIcon />{/snippet}
  </MetricCard>

  <MetricCard
    iconBg="var(--color-success-soft)"
    iconColor="var(--color-success)"
    value={`$${summary.cost.toFixed(2)}`}
    label="예상 비용"
    delta={arrow(summary.costDelta)}
    deltaColor="var(--color-success)"
    deltaNote={note(summary.costDelta)}
  >
    {#snippet icon()}<span style="font-size:15px;font-weight:700">$</span
      >{/snippet}
  </MetricCard>

  <MetricCard
    iconBg="var(--color-session-soft)"
    iconColor="var(--color-session)"
    value={String(summary.sessions)}
    label="세션 수"
    delta={arrow(summary.sessionsDelta)}
    deltaColor="var(--color-text-muted)"
    deltaNote={note(summary.sessionsDelta)}
  >
    {#snippet icon()}<WaveIcon />{/snippet}
  </MetricCard>

  <div class="flex items-end justify-center" style="gap:22px;padding:0 8px">
    <div class="flex flex-col items-center" style="gap:8px">
      <Mascot pose={mascot.pose} height={128} />
      <div
        class="bg-border-strong"
        style="width:150px;height:2px;border-radius:2px"
      ></div>
    </div>
    <div
      class="text-text whitespace-pre"
      style="font-size:16px;line-height:1.7;padding-bottom:28px"
    >
      {mascot.msg}
    </div>
  </div>
</div>
