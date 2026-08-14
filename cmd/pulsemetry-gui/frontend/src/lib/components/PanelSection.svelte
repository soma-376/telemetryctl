<script lang="ts">
  import type { Snippet } from "svelte";
  import InfoIcon from "../icons/InfoIcon.svelte";

  let {
    title,
    info = false,
    headerActionLabel,
    footerActionLabel,
    headMb = 16,
    footMt = 16,
    onHeaderAction,
    onFooterAction,
    children,
  }: {
    title: string;
    info?: boolean;
    headerActionLabel?: string;
    footerActionLabel?: string;
    headMb?: number;
    footMt?: number;
    onHeaderAction?: () => void;
    onFooterAction?: () => void;
    children: Snippet;
  } = $props();
</script>

<section
  class="bg-surface border-border rounded-[14px] border"
  style="padding:18px 20px"
>
  <div
    class="flex items-center justify-between"
    style="margin-bottom:{headMb}px"
  >
    <div class="flex items-center" style="gap:8px">
      <h2
        class="text-text font-bold"
        style="font-size:15px;letter-spacing:-0.01em"
      >
        {title}
      </h2>
      {#if info}
        <InfoIcon class="text-text-muted" />
      {/if}
    </div>
    {#if headerActionLabel}
      <button
        type="button"
        class="text-text-secondary cursor-pointer border-none bg-transparent font-medium"
        style="font-size:12.5px"
        onclick={onHeaderAction}
      >
        {headerActionLabel} →
      </button>
    {/if}
  </div>

  {@render children()}

  {#if false && footerActionLabel}
    <div class="flex justify-end" style="margin-top:{footMt}px">
      <button
        type="button"
        class="text-text-secondary cursor-pointer border-none bg-transparent font-medium"
        style="font-size:12.5px"
        onclick={onFooterAction}
      >
        {footerActionLabel} →
      </button>
    </div>
  {/if}
</section>
