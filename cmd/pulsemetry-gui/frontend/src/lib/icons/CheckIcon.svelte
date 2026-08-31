<script lang="ts">
  let {
    size = 16,
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
  <path d="M20 7 10 17l-6-6" />
</svg>
