<script lang="ts">
  import type { HeroData } from "../chart";
  import { labelStep } from "../chart";
  import EmptyState from "../../../lib/components/EmptyState.svelte";

  // 히어로 카드 — 스택 바 자체가 벤더 분해라 별도 총계/점유율 카드가 필요 없다.
  let { hero }: { hero: HeroData } = $props();

  // 라벨을 몇 개 걸러 보여줄지는 컬럼 폭을 알아야 정할 수 있어 렌더 폭을 잰다.
  let plotWidth = $state(0);
  const step = $derived(
    labelStep(
      hero.bars.map((b) => b.label),
      plotWidth / (hero.bars.length || 1),
    ),
  );
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
        <div
          class="grid items-stretch"
          style="flex:1;grid-template-columns:{hero.gridCols};gap:{hero.gridGap};min-height:148px"
        >
          {#each hero.bars as b, i (i)}
            <div class="flex h-full flex-col items-center" style="gap:8px">
              <span
                class="font-semibold whitespace-nowrap"
                style="font-size:{b.valueSize};color:{b.valueFg};font-variant-numeric:tabular-nums"
              >
                {b.total}
              </span>
              <!-- 값 → 높이 매핑은 flex-grow 로 한다. 위 여백과 채움이 비율대로
                   공간을 나누므로 픽셀 계산도, 반올림 누적도 없다. -->
              <span class="flex w-full flex-col" style="flex:1;min-height:0">
                <span style="flex:{100 - b.fillPct} 1 0"></span>
                <span class="flex w-full flex-col" style="flex:{b.fillPct} 1 0">
                  {#each b.parts as pt, j (j)}
                    <!-- 조각 사이 2px 간격은 gap 이 아니라 border 로 낸다. gap 은
                         고정 픽셀이라 막대가 짧아지면 합이 넘치지만, border 는
                         box-sizing:border-box 아래에서 조각 안쪽을 깎는다. -->
                    <span
                      class="w-full"
                      style="flex:{pt.weight} 1 0;background:{pt.color};border-radius:{pt.radius};{j >
                      0
                        ? 'border-top:2px solid var(--color-surface)'
                        : ''}"
                    ></span>
                  {/each}
                </span>
              </span>
            </div>
          {/each}
        </div>
        <div
          bind:clientWidth={plotWidth}
          class="grid"
          style="grid-template-columns:{hero.gridCols};gap:{hero.gridGap};margin-top:9px;padding-top:9px;border-top:1px solid #f1ece4"
        >
          {#each hero.bars as b, i (i)}
            {@const isLast = i === hero.bars.length - 1}
            <span
              class="text-center whitespace-nowrap"
              style="font-size:11px;color:{b.labelFg};font-weight:{b.labelWeight};overflow:visible"
            >
              {isLast || (i % step === 0 && hero.bars.length - 1 - i >= step)
                ? b.label
                : ""}
            </span>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>
