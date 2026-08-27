<script lang="ts">
  let {
    size = 14,
    strokeWidth = 2.2,
    class: klass = "",
    style = "",
  }: {
    size?: number;
    strokeWidth?: number;
    class?: string;
    style?: string;
  } = $props();
</script>

<svg
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-linecap="round"
  width={size}
  height={size}
  stroke-width={strokeWidth}
  class={klass}
  {style}
>
  <path d="m7 7 10 10M17 7 7 17" />
</svg>
