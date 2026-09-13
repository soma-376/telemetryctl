<script lang="ts">
  // 색은 currentColor 를 타므로 class="text-..." 로 준다 (ChevronDownIcon 과 같은 규약).
  let {
    size = 15,
    strokeWidth = 1.8,
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
  <circle cx="11" cy="11" r="6.5" />
  <path d="m16 16 4 4" />
</svg>
