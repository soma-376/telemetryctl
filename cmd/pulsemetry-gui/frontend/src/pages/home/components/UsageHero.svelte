<script lang="ts">
  import type { HeroData } from "../types";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import UsageChart from "./usage-chart/UsageChart.svelte";

  // 히어로 카드 — 스택 바 자체가 벤더 분해라 별도 총계/점유율 카드가 필요 없다.
  let { hero }: { hero: HeroData } = $props();

</script>

<div
  class="bg-surface border-border border"
  style="border-radius:14px;padding:18px 22px;margin-bottom:12px"
>
  <div
    class="grid items-stretch"
    style="grid-template-columns:186px minmax(0,1fr);gap:26px"
  >
    <div class="flex flex-col" style="padding-right:24px;border-right:1px solid #f1ece4">
      <div class="text-text-muted" style="font-size:12px;margin-bottom:8px">
        토큰 사용량
      </div>
      <div class="flex items-baseline" style="gap:4px;margin-bottom:16px">
        <span
          class="text-text font-bold"
          style="font-size:38px;letter-spacing:-0.035em;line-height:1;font-variant-numeric:tabular-nums"
        >
          {hero.totalTokens}
        </span>
        <span class="text-text-secondary font-semibold" style="font-size:19px">k</span>
      </div>
      <div
        class="grid"
        style="grid-template-columns:auto minmax(0,1fr);gap:7px 12px;font-size:12.5px"
      >
        <span class="text-text-muted whitespace-nowrap">예상 비용</span>
        <span
          class="text-right font-semibold"
          style="font-variant-numeric:tabular-nums">{hero.totalCost}</span
        >
        <span class="text-text-muted whitespace-nowrap">AI 활동 시간</span>
        <span
          class="text-right font-semibold"
          style="font-variant-numeric:tabular-nums">{hero.totalTime}</span
        >
        <span class="text-text-muted whitespace-nowrap">{hero.avgLabel}</span>
        <span
          class="text-right font-semibold"
          style="font-variant-numeric:tabular-nums">{hero.avgValue}</span
        >
      </div>
      <div class="mt-auto flex flex-wrap" style="padding-top:14px;gap:6px 12px">
        {#each hero.legend as l (l.name)}
          <span
            class="text-text-secondary flex items-center whitespace-nowrap"
            style="gap:6px;font-size:11.5px"
          >
            <span
              class="flex-none"
              style="width:8px;height:8px;border-radius:2px;background:{l.color}"
            ></span>{l.name}
          </span>
        {/each}
      </div>
    </div>

    <div class="flex flex-col" style="min-width:0">
      <div class="flex items-baseline" style="gap:10px;margin-bottom:12px">
        <span class="text-text-muted" style="font-size:12px">{hero.caption}</span>
        <span style="flex:1"></span>
        <span class="text-text-muted" style="font-size:11.5px">{hero.peakNote}</span>
      </div>
      <!-- 막대는 기간마다 버킷 수만큼 생성되고 값만 0이 된다. 따라서 비어 있음의
           판정은 bars.length 가 아니라 grandTotal 이다. -->
      {#if hero.grandTotal === 0}
        <EmptyState
          title="이 기간에 기록된 사용량이 없어요"
          description={"다른 기간을 선택하거나, CLI 가 연결되어 있는지 확인해보세요."}
        />
      {:else}
        <UsageChart {hero} />
      {/if}
    </div>
  </div>
</div>
