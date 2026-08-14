<script lang="ts">
  let {
    size = 21,
    strokeWidth = 1.7,
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
  <path d="M6 16v-5a6 6 0 0 1 12 0v5l1.5 2.5h-15L6 16Z" />
  <path d="M10 21h4" />
</svg>
