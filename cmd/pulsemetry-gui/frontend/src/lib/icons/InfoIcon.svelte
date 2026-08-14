<script lang="ts">
  let {
    size = 15,
    strokeWidth = 1.8,
    class: klass = "",
  }: { size?: number; strokeWidth?: number; class?: string } = $props();
</script>

<svg
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  width={size}
  height={size}
  stroke-width={strokeWidth}
  class={klass}
>
  <circle cx="12" cy="12" r="9" />
  <path d="M12 11v5.5" stroke-linecap="round" />
  <circle cx="12" cy="7.8" r="0.9" fill="currentColor" stroke="none" />
</svg>
