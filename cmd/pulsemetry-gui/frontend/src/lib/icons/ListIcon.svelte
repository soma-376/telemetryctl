<script lang="ts">
  let {
    size = 19,
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
  <path d="M9 6h11M9 12h11M9 18h11" />
  <circle cx="4.5" cy="6" r="1.3" fill="currentColor" stroke="none" />
  <circle cx="4.5" cy="12" r="1.3" fill="currentColor" stroke="none" />
  <circle cx="4.5" cy="18" r="1.3" fill="currentColor" stroke="none" />
</svg>
