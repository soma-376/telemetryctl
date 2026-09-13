<script lang="ts">
  import type { HTMLInputAttributes } from "svelte/elements";
  import SearchIcon from "$lib/icons/SearchIcon.svelte";

  // 테두리·배경은 바깥 상자가 갖고 input 자체는 투명하다. 디자인이 아이콘과 글자를 한
  // 테두리 안에 나란히 두기 때문이다.
  //
  // 상자가 테두리를 가지므로 input 의 기본 포커스 링은 상자 안쪽에 그려져 잘린다. 그래서
  // outline 을 끄고 focus-within 으로 상자에 표시한다 — 끄기만 하면 포커스가 어디 있는지
  // 보이지 않는다.
  let {
    value = $bindable(""),
    icon = "none",
    class: klass = "",
    ...rest
  }: HTMLInputAttributes & {
    value?: string;
    icon?: "none" | "search";
  } = $props();
</script>

<span
  class="box bg-surface border-border inline-flex items-center border {klass}"
  style="gap:9px;border-radius:10px;padding:10px 12px"
>
  {#if icon === "search"}
    <SearchIcon class="text-text-muted flex-none" />
  {/if}
  <input
    bind:value
    class="field text-text w-full min-w-0 flex-1 border-none bg-transparent outline-none"
    style="font-size:13px"
    {...rest}
  />
</span>

<style>
  .box:hover,
  .box:focus-within {
    border-color: var(--color-border-strong);
  }

  .field::placeholder {
    color: var(--color-text-muted);
  }
</style>
