<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLSelectAttributes } from "svelte/elements";
  import ChevronDownIcon from "$lib/icons/ChevronDownIcon.svelte";

  // 디자인의 드롭다운은 버튼 모양이지만 실제로는 native select 를 쓴다. 값 바인딩·키보드
  // 조작·화면 낭독기를 그대로 얻으려면 select 여야 하고, 생김새만 appearance:none 으로
  // 벗겨 맞춘다. 화살표는 select 안에 넣을 수 없어 겹쳐 놓는다.
  //
  // 나머지 속성(aria-label 등)은 그대로 흘려보낸다 — 호출부가 쓰던 것을 여기서 다시
  // 정의하지 않는다.
  let {
    value = $bindable(""),
    children,
    class: klass = "",
    ...rest
  }: HTMLSelectAttributes & { value?: string; children?: Snippet } = $props();
</script>

<span class="relative inline-flex items-center {klass}">
  <select
    bind:value
    class="field bg-surface border-border text-text w-full cursor-pointer appearance-none border whitespace-nowrap"
    style="border-radius:10px;padding:10px 35px 10px 14px;font-size:13px;font-family:inherit"
    {...rest}
  >
    {@render children?.()}
  </select>
  <ChevronDownIcon
    size={13}
    strokeWidth={2.2}
    class="text-text-muted pointer-events-none absolute"
    style="right:14px"
  />
</span>

<style>
  .field:hover {
    border-color: var(--color-border-strong);
  }
</style>
