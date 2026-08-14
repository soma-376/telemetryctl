<script lang="ts">
  import type { AgentId } from "../types";
  import { AGENT_STYLE } from "../agents";

  let { agent, size = "md" }: { agent: AgentId; size?: "sm" | "md" | "lg" } =
    $props();

  const DIM = { sm: 24, md: 38, lg: 40 } as const;
  const RADIUS = { sm: 7, md: 11, lg: 12 } as const;

  const style = $derived(AGENT_STYLE[agent]);
  const dim = $derived(DIM[size]);
  const font = $derived(size === "sm" ? style.fontSm : style.fontMd);
</script>

<div
  class="flex flex-none items-center justify-center"
  style="width:{dim}px;height:{dim}px;border-radius:{RADIUS[
    size
  ]}px;background:{style.bg};color:{style.fg};font-size:{font}px;font-weight:{style.weight}"
>
  {style.glyph}
</div>
