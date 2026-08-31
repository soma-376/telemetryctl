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
  <rect x="3.5" y="4" width="17" height="16" rx="3" />
  <path d="M8.5 15.5v-3M12 15.5v-6M15.5 15.5v-4" />
</svg>
