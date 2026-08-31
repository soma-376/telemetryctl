<script lang="ts">
  let {
    size = 19,
    strokeWidth = 1.9,
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
  stroke-linejoin="round"
  width={size}
  height={size}
  stroke-width={strokeWidth}
  class={klass}
  {style}
>
  <path d="M3 13h3l2.5-7 4 14 2.5-7H21" />
</svg>
