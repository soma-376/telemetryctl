<script lang="ts">
  interface StageBar {
    name: string;
    dur: string;
    weight: number;
    color: string;
    labelColor: string;
    weightFont: number;
  }

  let { stages }: { stages: StageBar[] } = $props();
</script>

<div class="bg-surface border-border border" style="border-radius:12px;padding:14px 16px">
  <div
    class="text-text-secondary font-semibold"
    style="font-size:12.5px;margin-bottom:14px"
  >
    세션 흐름
  </div>
  <div class="flex items-end" style="gap:8px">
    {#each stages as stage (stage.name + stage.dur)}
      <div class="relative text-center" style="flex:{stage.weight}">
        <div
          style="height:3px;border-radius:2px;margin-bottom:12px;background:{stage.color}"
        ></div>
        <div
          class="whitespace-nowrap"
          style="font-size:12.5px;margin-bottom:4px;font-weight:{stage.weightFont};color:{stage.labelColor}"
        >
          {stage.name}
        </div>
        <div class="text-text-muted" style="font-size:12px">{stage.dur}</div>
      </div>
    {/each}
  </div>
</div>
