<script lang="ts">
  let {
    size = 15,
    strokeWidth = 1.7,
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
  <rect x="3" y="5" width="18" height="16" rx="3" />
  <path d="M8 3v4M16 3v4M3 10h18" />
</svg>
