<script lang="ts">
  // 상태 점. 지름과 색만 다를 뿐 같은 도형이 화면 곳곳에 반복된다.
  // pulse 는 "진행 중"을 나타내는 livePulse 링 애니메이션(app.css)을 켠다.
  let {
    size = 7,
    color,
    pulse = false,
    class: klass = "",
    style = "",
  }: {
    size?: number;
    color: string;
    pulse?: boolean;
    class?: string;
    style?: string;
  } = $props();
</script>

<span
  class="block flex-none {klass}"
  style="width:{size}px;height:{size}px;border-radius:50%;background:{color}{pulse
    ? ';animation:livePulse 2s ease-out infinite'
    : ''};{style}"
></span>
