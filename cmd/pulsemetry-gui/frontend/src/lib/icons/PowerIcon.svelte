<script lang="ts">
  let {
    size = 19,
    strokeWidth = 1.9,
    class: klass = "",
  }: {
    size?: number;
    strokeWidth?: number;
    class?: string;
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
>
  <path d="M12 4v8" />
  <path d="M7.5 7A7 7 0 1 0 16.5 7" />
</svg>
