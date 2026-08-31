<script lang="ts">
  import { FILE_CAP } from "../model";
  import type { FileChange } from "../types";
  import ChevronDownIcon from "$lib/icons/ChevronDownIcon.svelte";

  let {
    files,
    open,
    onToggle,
  }: {
    files: FileChange[];
    open: boolean;
    onToggle?: () => void;
  } = $props();

  const visible = $derived(open ? files : files.slice(0, FILE_CAP));
</script>

<div
  class="bg-surface border-border flex flex-col border"
  style="border-radius:12px;padding:14px 16px"
>
  <div
    class="text-text-secondary font-semibold"
    style="font-size:12.5px;margin-bottom:10px"
  >
    파일 변경
  </div>
  {#each visible as file (file.dir + file.name)}
    <div
      class="grid items-center"
      style="grid-template-columns:minmax(0,1fr) auto 32px;gap:9px;padding:7px 0"
    >
      <span
        class="flex min-w-0 items-baseline"
        style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:11.5px"
      >
        <span
          class="text-text-muted min-w-0 overflow-hidden text-ellipsis whitespace-nowrap"
          style="flex:0 1 auto;direction:rtl;text-align:left"
          ><bdi>{file.dir}</bdi></span
        >
        <span class="text-text flex-none">{file.name}</span>
      </span>
      <span
        style="font-size:12px;color:var(--color-success);font-variant-numeric:tabular-nums"
        >{file.add}</span
      >
      <span
        style="font-size:12px;color:var(--color-danger);font-variant-numeric:tabular-nums;text-align:right"
        >{file.del}</span
      >
    </div>
  {/each}
  {#if files.length > FILE_CAP}
    <button
      type="button"
      class="bg-surface border-border text-text hover:border-border-strong flex w-full cursor-pointer items-center justify-center border"
      style="gap:7px;font-size:12px;font-weight:500;padding:9px;border-radius:9px;margin-top:auto"
      onclick={() => onToggle?.()}
    >
      {open ? "접기" : `파일 ${files.length}개 모두 보기`}
      <ChevronDownIcon
        size={12}
        strokeWidth={2.4}
        class="text-text-muted"
        rotated={open}
      />
    </button>
  {/if}
</div>
