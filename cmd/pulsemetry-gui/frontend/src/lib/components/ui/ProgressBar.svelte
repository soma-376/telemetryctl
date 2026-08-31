<script lang="ts">
  // 트랙 + 채움 두 겹짜리 막대.
  // div 안에서도 span 안에서도 쓰이므로 span 으로 그리고 block 을 명시한다
  // (span 안의 div 는 유효하지 않은 HTML 이다).
  //
  // pct 는 숫자(45)와 문자열("45%") 을 모두 받는다 — 호출부마다 이미 계산된
  // 형태가 달라서, 한쪽으로 강제하면 호출부에서 변환이 다시 흩어진다.
  let {
    pct,
    color,
    height = 5,
    animate = false,
    class: klass = "",
    style = "",
  }: {
    pct: number | string;
    color: string;
    height?: number;
    animate?: boolean;
    class?: string;
    style?: string;
  } = $props();

  const width = $derived(typeof pct === "number" ? `${pct}%` : pct);
</script>

<span
  class="bg-track block {klass}"
  style="height:{height}px;border-radius:999px;{style}"
>
  <span
    class="block"
    style="height:100%;border-radius:999px;background:{color};width:{width}{animate
      ? ';transition:width 300ms ease'
      : ''}"
  ></span>
</span>
