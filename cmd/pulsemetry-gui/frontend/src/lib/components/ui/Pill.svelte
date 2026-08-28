<script lang="ts">
  import Dot from "./Dot.svelte";

  // 라벨 하나짜리 상태 칩. 구조(굵은 글씨 + 줄바꿈 금지 + 라운드 + 패딩 +
  // fg/bg)는 공통이고 치수만 호출부마다 다르다.
  //
  // 치수를 prop 으로 열어둔 이유: 현재 값이 라운드 5~7px, 패딩 4종,
  // 글씨 10.5~11.5px 으로 흩어져 있다. 여기서 임의로 통일하면 화면이 바뀌므로
  // 일단 그대로 받는다(정리는 별도 판단).
  //
  // 테두리 + 사각 dot 을 쓰는 "작업 유형" 배지(TurnCard, SessionDetail 의
  // character)는 다른 계열이라 여기 넣지 않았다.
  let {
    label,
    fg,
    bg,
    fontSize = 11,
    radius = 7,
    padding = "4px 9px",
    gap = 6,
    dot = false,
    pulse = false,
    class: klass = "",
  }: {
    label: string;
    fg: string;
    bg: string;
    fontSize?: number;
    radius?: number;
    padding?: string;
    gap?: number;
    dot?: boolean;
    pulse?: boolean;
    class?: string;
  } = $props();
</script>

<!-- dot 이 없을 때는 원래대로 inline 박스로 둔다. inline-flex 로 바꾸면
     grid·flex 부모 안에서 baseline 정렬이 미세하게 달라진다. -->
<span
  class="font-semibold whitespace-nowrap {dot
    ? 'inline-flex items-center'
    : ''} {klass}"
  style="{dot
    ? `gap:${gap}px;`
    : ''}font-size:{fontSize}px;border-radius:{radius}px;padding:{padding};color:{fg};background:{bg}"
>
  {#if dot}<Dot size={6} color={fg} {pulse} />{/if}{label}
</span>
