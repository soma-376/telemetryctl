<script lang="ts">
  import type { Session, AgentId } from "../../../lib/types";
  import { formatTokens } from "../../../lib/format";
  import PanelSection from "../../../lib/components/PanelSection.svelte";
  import AgentBadge from "../../../lib/components/AgentBadge.svelte";
  import StatusBadge from "../../../lib/components/StatusBadge.svelte";

  let {
    sessions,
    agentNames,
    onViewAll,
  }: {
    sessions: Session[];
    agentNames: Record<AgentId, string>;
    onViewAll?: () => void;
  } = $props();

  const dur = (n: number) =>
    n >= 60 ? `${Math.floor(n / 60)}h ${n % 60}m` : `${n}m`;
</script>

<PanelSection
  title="주요 세션"
  info
  headMb={8}
  headerActionLabel="전체 보기"
  onHeaderAction={onViewAll}
>
  <div class="flex flex-col">
    {#each sessions as s (s.id)}
      <div
        class="grid items-center"
        style="grid-template-columns:40px 1fr auto auto;gap:12px;padding:11px 0"
      >
        <AgentBadge agent={s.agentId} size="lg" />
        <div style="min-width:0">
          <div
            class="text-text truncate font-semibold"
            style="font-size:14px;margin-bottom:5px"
          >
            {s.title}
          </div>
          <div class="text-text-muted" style="font-size:12px">
            {agentNames[s.agentId] ?? s.agentId} • {dur(s.durationMin)}
          </div>
        </div>
        <div class="text-right" style="min-width:56px">
          <div
            class="text-text font-bold"
            style="font-size:15px;letter-spacing:-0.02em"
          >
            {formatTokens(s.tokens)}
          </div>
          <div class="text-text-muted" style="font-size:11.5px">tokens</div>
        </div>
        <StatusBadge state={s.status} />
      </div>
    {/each}
  </div>
</PanelSection>
