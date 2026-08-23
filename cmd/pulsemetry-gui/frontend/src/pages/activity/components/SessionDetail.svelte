<script lang="ts">
  import { fade, fly } from "svelte/transition";
  import { cubicOut } from "svelte/easing";
  import { detailDisplay } from "../data";
  import KpiGrid from "./KpiGrid.svelte";
  import StageFlow from "./StageFlow.svelte";
  import EventTimeline from "./EventTimeline.svelte";
  import FileChanges from "./FileChanges.svelte";

  let {
    open = false,
    index,
    onClose,
    onPrev,
    onNext,
  }: {
    open?: boolean;
    index: number | null;
    onClose?: () => void;
    onPrev?: () => void;
    onNext?: () => void;
  } = $props();

  let n = $state(0);
  $effect(() => {
    if (index !== null) n = index;
  });

  const d = $derived(detailDisplay(n));

  function onOverlayKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") onClose?.();
  }
  function onCloseKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" || e.key === " ") onClose?.();
  }
</script>

{#if open}
  <div
    class="fixed inset-0"
    style="z-index:60;background:rgba(27,26,24,0.32)"
    role="button"
    tabindex="-1"
    aria-label="닫기"
    onclick={() => onClose?.()}
    onkeydown={onOverlayKeydown}
    transition:fade={{ duration: 180 }}
  ></div>
  <div
    class="bg-surface fixed top-0 right-0 bottom-0 flex flex-col"
    style="z-index:61;width:620px;border-left:1px solid var(--color-border);border-radius:16px 0 0 16px;box-shadow:-28px 0 60px rgba(27,26,24,0.14)"
    transition:fly={{ x: 620, duration: 240, opacity: 1, easing: cubicOut }}
  >
    <div class="flex-none" style="padding:22px 24px 16px;border-bottom:1px solid #f1ece4">
      <div class="flex items-start" style="gap:13px">
        <div
          class="flex flex-none items-center justify-center"
          style="width:44px;height:44px;border-radius:13px;background:{d.agentTile
            .bg};color:{d.agentTile.fg};font-size:{d.agentTile
            .size}px;font-weight:{d.agentTile.weight}"
        >
          {d.agentTile.glyph}
        </div>
        <div class="min-w-0 flex-1">
          <div
            class="text-text overflow-hidden font-bold text-ellipsis whitespace-nowrap"
            style="font-size:21px;letter-spacing:-0.02em;margin-bottom:6px"
          >
            {d.title}
          </div>
          <div
            class="flex items-baseline"
            style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12.5px;margin-bottom:8px"
          >
            <span class="text-text flex-none">{d.repo}</span>
            <span class="text-text-muted flex-none">&nbsp;/&nbsp;</span>
            <span
              class="text-text-muted min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap"
              style="direction:rtl;text-align:left"><bdi>{d.path}</bdi></span
            >
          </div>
          <div
            class="text-text-muted flex items-center overflow-hidden whitespace-nowrap"
            style="gap:9px;font-size:12px"
          >
            <span class="text-text-secondary flex-none" style="font-weight:500"
              >{d.agentName}</span
            >
            <span
              class="flex-none font-semibold"
              style="font-size:11px;border-radius:6px;padding:3px 7px;background:{d
                .badge.bg};color:{d.badge.fg}">{d.badge.label}</span
            >
            <span class="overflow-hidden text-ellipsis">{d.range}</span>
          </div>
        </div>
        <div
          class="border-border text-text-secondary hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border"
          style="width:30px;height:30px;border-radius:9px"
          role="button"
          tabindex="0"
          aria-label="닫기"
          onclick={() => onClose?.()}
          onkeydown={onCloseKeydown}
        >
          <svg
            viewBox="0 0 24 24"
            style="width:15px;height:15px"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"><path d="m7 7 10 10M17 7 7 17"></path></svg
          >
        </div>
      </div>
      <div class="flex" style="gap:8px;margin-top:16px">
        <button
          type="button"
          class="hover:bg-accent-hover flex cursor-pointer items-center border-none font-semibold whitespace-nowrap text-white"
          style="gap:7px;background:var(--color-accent);font-size:12.5px;padding:9px 14px;border-radius:9px"
        >
          <svg
            viewBox="0 0 24 24"
            style="width:13px;height:13px"
            fill="none"
            stroke="#fffdfc"
            stroke-width="1.8"
            stroke-linejoin="round"><path d="M8 5.5 18 12 8 18.5z"></path></svg
          > 계속하기</button
        >
        <button
          type="button"
          class="bg-surface border-border text-text hover:border-border-strong flex cursor-pointer items-center border font-semibold whitespace-nowrap"
          style="gap:8px;font-size:12.5px;padding:9px 14px;border-radius:9px"
        >
          다른 에이전트로 넘기기
          <svg
            viewBox="0 0 24 24"
            style="width:13px;height:13px"
            fill="none"
            stroke="var(--color-text-muted)"
            stroke-width="2.2"
            stroke-linecap="round"
            stroke-linejoin="round"><path d="m6 9 6 6 6-6"></path></svg
          ></button
        >
        <button
          type="button"
          class="bg-surface border-border text-text hover:border-border-strong flex cursor-pointer items-center border font-semibold whitespace-nowrap"
          style="gap:7px;font-size:12.5px;padding:9px 14px;border-radius:9px"
        >
          <svg
            viewBox="0 0 24 24"
            style="width:13px;height:13px"
            fill="none"
            stroke="var(--color-text-secondary)"
            stroke-width="1.9"
            stroke-linejoin="round"><path d="M4 6h6l2 2.5h8V19H4z"></path></svg
          > 폴더 열기</button
        >
        <div class="flex-1"></div>
        <div
          class="border-border text-text-muted hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border"
          style="width:30px;height:30px;border-radius:9px;font-size:14px"
        >
          •••
        </div>
      </div>
    </div>
    <div
      class="flex min-h-0 flex-1 flex-col overflow-y-auto"
      style="padding:18px 24px 22px;gap:14px"
    >
      <div class="text-text" style="font-size:13.5px;line-height:1.75">
        {d.summary}
      </div>
      <KpiGrid kpi={d.kpi} />
      <StageFlow stages={d.stages} />
      <div
        class="grid items-start"
        style="grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:12px"
      >
        <EventTimeline events={d.events} />
        <FileChanges files={d.files} />
      </div>
    </div>
    <div
      class="flex flex-none items-center"
      style="gap:10px;padding:12px 24px;border-top:1px solid #f1ece4;background:#faf7f2;border-radius:0 0 0 16px"
    >
      <button
        type="button"
        class="bg-surface border-border text-text hover:border-border-strong flex cursor-pointer items-center border font-semibold whitespace-nowrap"
        style="gap:7px;font-size:12.5px;padding:8px 13px;border-radius:9px"
        onclick={() => onPrev?.()}
      >
        <svg
          viewBox="0 0 24 24"
          style="width:13px;height:13px"
          fill="none"
          stroke="var(--color-text-secondary)"
          stroke-width="2.2"
          stroke-linecap="round"
          stroke-linejoin="round"><path d="m15 5-7 7 7 7"></path></svg
        > 이전</button
      >
      <button
        type="button"
        class="bg-surface border-border text-text hover:border-border-strong flex cursor-pointer items-center border font-semibold whitespace-nowrap"
        style="gap:7px;font-size:12.5px;padding:8px 13px;border-radius:9px"
        onclick={() => onNext?.()}
      >
        다음
        <svg
          viewBox="0 0 24 24"
          style="width:13px;height:13px"
          fill="none"
          stroke="var(--color-text-secondary)"
          stroke-width="2.2"
          stroke-linecap="round"
          stroke-linejoin="round"><path d="m9 5 7 7-7 7"></path></svg
        ></button
      >
      <span class="text-text-muted" style="font-size:12px">{d.position}</span>
      <span class="flex-1"></span>
      <span
        class="text-text-muted flex items-center whitespace-nowrap"
        style="gap:6px;font-size:11.5px"
      >
        <span
          class="bg-surface border-border border"
          style="font-family:ui-monospace,Menlo,monospace;border-radius:5px;padding:2px 6px"
          >J</span
        >
        <span
          class="bg-surface border-border border"
          style="font-family:ui-monospace,Menlo,monospace;border-radius:5px;padding:2px 6px"
          >K</span
        > 이동
        <span
          class="bg-surface border-border border"
          style="font-family:ui-monospace,Menlo,monospace;border-radius:5px;padding:2px 6px;margin-left:6px"
          >Esc</span
        > 닫기</span
      >
    </div>
  </div>
{/if}
