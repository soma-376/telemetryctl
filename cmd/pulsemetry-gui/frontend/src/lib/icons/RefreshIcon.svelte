<script lang="ts">
  let {
    size = 14,
    strokeWidth = 2,
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
  <path d="M20 12a8 8 0 1 1-2.4-5.7" />
  <path d="M20 4.5V9h-4.5" />
</svg>
