<script lang="ts">
  import PanelSection from "../../../lib/components/PanelSection.svelte";
  import Mascot from "../../../lib/components/Mascot.svelte";

  let {
    weeklyPattern,
    patternLabel,
    patternBody,
    tiredMsg,
    onMore,
  }: {
    weeklyPattern: number[];
    patternLabel: string;
    patternBody: string;
    tiredMsg: string;
    onMore?: () => void;
  } = $props();

  const ramp = (n: number) =>
    n >= 90
      ? "var(--color-ramp-4)"
      : n >= 60
        ? "var(--color-ramp-3)"
        : n >= 48
          ? "var(--color-ramp-2)"
          : "var(--color-ramp-1)";
</script>

<PanelSection
  title="인사이트"
  headMb={12}
  headerActionLabel="인사이트 더 보기"
  onHeaderAction={onMore}
>
  <div class="flex items-center" style="gap:24px">
    <div class="flex flex-none items-center" style="gap:14px">
      <Mascot pose="tired" height={96} />
      <div
        class="text-text whitespace-pre-line"
        style="font-size:13px;line-height:1.85"
      >
        {tiredMsg}
      </div>
    </div>
    <div
      class="bg-surface-hover border-border border"
      style="flex:1;border-radius:12px;padding:14px 16px"
    >
      <div class="text-text-muted" style="font-size:11.5px;margin-bottom:8px">
        {patternLabel}
      </div>
      <div
        class="text-text whitespace-pre-line"
        style="font-size:13px;line-height:1.6;margin-bottom:12px"
      >
        {patternBody}
      </div>
      <div class="flex items-end" style="gap:5px;height:52px">
        {#each weeklyPattern as v}
          <div
            style="flex:1;height:{v}%;background:{ramp(v)};border-radius:3px"
          ></div>
        {/each}
      </div>
    </div>
  </div>
</PanelSection>
