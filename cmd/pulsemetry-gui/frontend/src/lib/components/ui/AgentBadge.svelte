<script lang="ts">
  import type { AgentId } from "$lib/domain/agent.types";
  import { AGENT_STYLE } from "$lib/domain/agent";

  // 디자인이 쓰는 타일 지름은 다섯 가지다. 라운드와 글리프 크기는 지름에
  // 비례하지 않고 각각 따로 정해져 있어(24→7, 28·30→9, 32→10, 40→12) 표로 둔다.
  // cap 은 큰 타일에서 글리프가 과하게 커지지 않게 누르는 상한이다.
  const SIZES = {
    24: { radius: 7, font: "sm", cap: Infinity },
    28: { radius: 9, font: "sm", cap: Infinity },
    30: { radius: 9, font: "md", cap: 15 },
    32: { radius: 10, font: "md", cap: Infinity },
    40: { radius: 12, font: "md", cap: Infinity },
  } as const;

  type BadgeSize = keyof typeof SIZES;

  // fontSize 는 표에서 벗어나야 하는 호출부를 위한 탈출구다. 지금 쓰는 곳은
  // 트레이 한도 카드 하나뿐이다(같은 24px 타일인데 글리프만 2px 크다).
  let {
    agent,
    size = 32,
    fontSize,
  }: { agent: AgentId; size?: BadgeSize; fontSize?: number } = $props();

  const style = $derived(AGENT_STYLE[agent]);
  const spec = $derived(SIZES[size]);
  const font = $derived(
    fontSize ??
      Math.min(spec.font === "sm" ? style.fontSm : style.fontMd, spec.cap),
  );
</script>

<div
  class="flex flex-none items-center justify-center"
  style="width:{size}px;height:{size}px;border-radius:{spec.radius}px;background:{style.bg};color:{style.fg};font-size:{font}px;font-weight:{style.weight}"
>
  {style.glyph}
</div>
