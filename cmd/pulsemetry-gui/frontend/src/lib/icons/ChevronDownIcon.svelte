<script lang="ts">
  // style 은 여백 같은 배치를 넘기기 위한 탈출구다.
  // 색은 currentColor 를 타므로 class="text-..." 나 style="color:..." 로 준다.
  //
  // rotated 는 그 탈출구에서 승격된 prop 이다. 펼침 표시로 회전시키는 호출부가
  // 넷이라 같은 transform·이징 문자열이 네 벌로 흩어졌었다 — 이징을 여기 한 곳에만
  // 두어 호출부끼리 어긋날 수 없게 한다. rotated 를 넘기지 않으면 transform 을
  // 아예 붙이지 않는다(rotate(0deg) 도 스태킹 컨텍스트를 만들기 때문).
  let {
    size = 14,
    strokeWidth = 1.8,
    class: klass = "",
    style = "",
    rotated,
  }: {
    size?: number;
    strokeWidth?: number;
    class?: string;
    style?: string;
    rotated?: boolean;
  } = $props();

  const composed = $derived(
    rotated === undefined
      ? style
      : `transform:rotate(${rotated ? 180 : 0}deg);` +
          `transition:transform 200ms cubic-bezier(0.32,0.72,0,1);${style}`,
  );
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
  style={composed}
>
  <path d="M6 9l6 6 6-6" />
</svg>
