<script lang="ts">
  // knobFill 은 노브 원의 속을 바탕색으로 메워 라인이 끊겨 보이게 한다.
  let {
    size = 19,
    strokeWidth = 1.7,
    knobFill = "var(--color-bg)",
    class: klass = "",
  }: {
    size?: number;
    strokeWidth?: number;
    knobFill?: string;
    class?: string;
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
>
  <path d="M4 8h16M4 16h16" />
  <circle cx="10" cy="8" r="2.4" fill={knobFill} />
  <circle cx="15" cy="16" r="2.4" fill={knobFill} />
</svg>
