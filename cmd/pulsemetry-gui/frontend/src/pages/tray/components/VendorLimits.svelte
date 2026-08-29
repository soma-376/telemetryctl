<script lang="ts">
  import AgentBadge from "$lib/components/ui/AgentBadge.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import { AGENT_STYLE } from "$lib/domain/agent";
  import type { UnavailableVendor } from "../adapter";
  import type { TrayVendor } from "../types";
  import VendorLimitCard from "./VendorLimitCard.svelte";

  let {
    vendors,
    unavailable = [],
  }: { vendors: TrayVendor[]; unavailable?: UnavailableVendor[] } = $props();
</script>

<section style="padding:10px 14px 3px" aria-label="벤더 한도">
  {#each vendors as vendor (vendor.id)}
    <VendorLimitCard {vendor} />
  {/each}

  <!-- 한도를 읽지 못한 벤더도 자리를 지킨다. 목록에서 빠지면 "로그인하지 않음" 과
       "아직 로딩 중" 을 구분할 수 없다. 막대 대신 무엇을 하면 되는지를 적는다. -->
  {#each unavailable as vendor (vendor.id)}
    <div
      class="bg-surface border-border flex items-center border"
      style="gap:8px;border-radius:11px;padding:9px 12px;margin-bottom:7px"
    >
      <AgentBadge
        agent={vendor.id}
        size={24}
        fontSize={Math.min(AGENT_STYLE[vendor.id].fontSm + 2, 15)}
      />
      <span class="text-text flex-none font-bold" style="font-size:12.5px">
        {AGENT_STYLE[vendor.id].name}
      </span>
      <span
        class="text-text-muted truncate"
        style="font-size:10.5px;min-width:0;flex:1;text-align:right"
      >
        {vendor.text}
      </span>
    </div>
  {/each}

  {#if vendors.length === 0 && unavailable.length === 0}
    <EmptyState
      size="sm"
      pose="no-data"
      title="한도 정보가 없습니다"
      description="지원하는 도구에 로그인하면 남은 한도가 여기 표시됩니다."
    />
  {/if}
</section>
