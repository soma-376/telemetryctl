<script lang="ts">
  let {
    size = 16,
    strokeWidth = 1.8,
    class: klass = "",
  }: { size?: number; strokeWidth?: number; class?: string } = $props();
</script>

<svg
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-linecap="round"
  stroke-linejoin="round"
  width={size}
  height={size}
  stroke-width={strokeWidth}
  class={klass}
>
  <!-- 화면에 실제로 쓰이던 인라인 사본(가로 7·세로 14)에 맞춘 좌표.
       기존 M15 6l-6 6 6 6 은 한 단계 작아 렌더된 적이 없었다. -->
  <path d="m15 5-7 7 7 7" />
</svg>
