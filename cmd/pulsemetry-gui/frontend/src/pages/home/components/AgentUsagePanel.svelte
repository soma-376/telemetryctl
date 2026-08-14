<script lang="ts">
  import type { AgentUsage } from "../../../lib/types";
  import { AGENT_STYLE } from "../../../lib/agents";
  import PanelSection from "../../../lib/components/PanelSection.svelte";
  import AgentBadge from "../../../lib/components/AgentBadge.svelte";

  let {
    agents,
    onAllAgents,
  }: { agents: AgentUsage[]; onAllAgents?: () => void } = $props();

  const fmt = (n: number) => `${Math.round(n / 1000)}k`;
</script>

<PanelSection
  title="Agent 사용 비율"
  info
  headerActionLabel="All agents"
  onHeaderAction={onAllAgents}
>
  <div class="flex flex-col" style="gap:15px">
    {#each agents as a (a.id)}
      <div class="flex items-center" style="gap:12px">
        <AgentBadge agent={a.id} size="md" />
        <div style="flex:1;min-width:0">
          <div
            class="flex items-baseline justify-between"
            style="margin-bottom:8px"
          >
            <span class="text-text font-semibold" style="font-size:13.5px">
              {a.name}
            </span>
            <span class="text-text font-semibold" style="font-size:12.5px">
              {a.pct}%
              <span class="text-text-muted font-normal">({fmt(a.tokens)})</span>
            </span>
          </div>
          <div
            class="bg-track overflow-hidden"
            style="height:7px;border-radius:999px"
          >
            <div
              style="width:{a.pct}%;height:100%;border-radius:999px;background:{AGENT_STYLE[
                a.id
              ].fg}"
            ></div>
          </div>
        </div>
      </div>
    {/each}
  </div>
</PanelSection>
