<script lang="ts">
  import { fade, fly } from "svelte/transition";
  import { sheetEase, easeOut } from "../../../lib/motion";
  import { detailDisplay } from "../data";
  import KpiGrid from "./KpiGrid.svelte";
  import TurnFlow from "./TurnFlow.svelte";
  import TurnCard from "./TurnCard.svelte";
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
  let turnOpen = $state<Record<number, boolean>>({});
  let turnSel = $state<number | null>(null);
  let filesOpen = $state(false);

  // 세션이 바뀌면 턴 펼침·선택·파일 목록 상태를 초기화한다.
  $effect(() => {
    if (index === null) return;
    n = index;
    turnOpen = {};
    turnSel = null;
    filesOpen = false;
  });

  const d = $derived(detailDisplay(n));
  const openCount = $derived(Object.values(turnOpen).filter(Boolean).length);
  const collapseLabel = $derived(openCount ? "모두 접기" : "모두 펼치기");

  function toggleTurn(k: number) {
    const next = !turnOpen[k];
    turnOpen = { ...turnOpen, [k]: next };
    if (next) turnSel = k;
  }

  function pickTurn(k: number) {
    turnSel = k;
    turnOpen = { ...turnOpen, [k]: true };
  }

  function collapseAll() {
    if (openCount) {
      turnOpen = {};
      return;
    }
    const all: Record<number, boolean> = {};
    d.turns.forEach((t) => (all[t.n] = true));
    turnOpen = all;
  }

  function onOverlayKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") onClose?.();
  }
  function onCloseKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" || e.key === " ") onClose?.();
  }
  function onCollapseKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      collapseAll();
    }
  }
</script>

{#if open}
  <div class="fixed inset-0" style="z-index:60">
    <div
      class="absolute inset-0"
      style="background:rgba(27,26,24,0.28)"
      transition:fade={{ duration: 180, easing: easeOut }}
      role="button"
      tabindex="-1"
      aria-label="닫기"
      onclick={() => onClose?.()}
      onkeydown={onOverlayKeydown}
    ></div>

    <div
      class="bg-surface absolute top-0 right-0 bottom-0 flex flex-col"
      style="width:min(720px,70vw);border-left:1px solid var(--color-border);box-shadow:-18px 0 44px rgba(27,26,24,0.13)"
      transition:fly={{ x: "100%", duration: 280, opacity: 1, easing: sheetEase }}
    >
      <div class="flex-none" style="padding:20px 24px 16px;border-bottom:1px solid #f1ece4">
        <div class="flex items-start" style="gap:14px;margin-bottom:14px">
          <div
            class="flex flex-none items-center justify-center"
            style="width:40px;height:40px;border-radius:12px;background:{d.agentTile
              .bg};color:{d.agentTile.fg};font-size:{d.agentTile
              .size}px;font-weight:{d.agentTile.weight}"
          >
            {d.agentTile.glyph}
          </div>
          <div class="min-w-0 flex-1">
            <div
              class="text-text overflow-hidden font-bold text-ellipsis whitespace-nowrap"
              style="font-size:21px;letter-spacing:-0.02em;margin-bottom:7px"
            >
              {d.title}
            </div>
            <div
              class="flex items-baseline"
              style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12.5px;margin-bottom:8px"
            >
              <span class="text-text flex-none">{d.repo}</span>
              <span class="text-text-muted flex-none" style="padding:0 5px">/</span>
              <span
                class="text-text-muted min-w-0 overflow-hidden text-ellipsis whitespace-nowrap"
                style="flex:0 1 auto;direction:rtl;text-align:left"><bdi>{d.path}</bdi></span
              >
            </div>
            <div
              class="text-text-muted flex items-center whitespace-nowrap"
              style="gap:9px;font-size:12.5px"
            >
              <span class="text-text-secondary flex-none" style="font-weight:500"
                >{d.agentName}</span
              >
              <span
                class="flex-none font-semibold"
                style="font-size:11.5px;border-radius:7px;padding:4px 8px;background:{d
                  .badge.bg};color:{d.badge.fg}">{d.badge.label}</span
              >
              <span
                class="inline-flex flex-none items-center border font-semibold"
                style="gap:6px;font-size:11.5px;border-radius:7px;padding:4px 9px;color:{d
                  .character.fg};background:{d.character.bg};border-color:{d.character
                  .border}"
              >
                <span
                  class="flex-none"
                  style="width:7px;height:7px;border-radius:2px;background:{d.character
                    .dot}"
                ></span>{d.character.label}
              </span>
              <span class="overflow-hidden text-ellipsis">{d.range}</span>
            </div>
          </div>
          <div
            class="border-border text-text-secondary hover:border-border-strong flex flex-none cursor-pointer items-center justify-center border"
            style="width:30px;height:30px;border-radius:9px"
            role="button"
            tabindex="0"
            aria-label="닫기"
            onclick={() => onClose?.()}
            onkeydown={onCloseKeydown}
          >
            <svg
              viewBox="0 0 24 24"
              style="width:14px;height:14px"
              fill="none"
              stroke="currentColor"
              stroke-width="2.2"
              stroke-linecap="round"><path d="m7 7 10 10M17 7 7 17"></path></svg
            >
          </div>
        </div>

        <div class="flex items-center" style="gap:9px">
          <button
            type="button"
            class="hover:bg-accent-hover flex cursor-pointer items-center border-none font-semibold whitespace-nowrap text-white"
            style="gap:8px;background:var(--color-accent);font-size:13px;padding:10px 16px;border-radius:9px"
          >
            <svg
              viewBox="0 0 24 24"
              style="width:13px;height:13px"
              fill="none"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linejoin="round"><path d="M8 5.5 18 12 8 18.5z"></path></svg
            > 계속하기</button
          >
          <button
            type="button"
            class="bg-surface border-border text-text hover:border-border-strong flex cursor-pointer items-center border font-semibold whitespace-nowrap"
            style="gap:9px;font-size:13px;padding:10px 15px;border-radius:9px"
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
            style="gap:8px;font-size:13px;padding:10px 15px;border-radius:9px"
          >
            <svg
              viewBox="0 0 24 24"
              style="width:14px;height:14px"
              fill="none"
              stroke="var(--color-text-secondary)"
              stroke-width="1.7"
              stroke-linejoin="round"
              ><path
                d="M3 7a2 2 0 0 1 2-2h4l2 2.5h8a2 2 0 0 1 2 2V18a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"
              ></path></svg
            > 폴더 열기</button
          >
          <div class="flex-1"></div>
          <div
            class="border-border text-text-muted hover:border-border-strong flex flex-none cursor-pointer items-center justify-center border"
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
        <KpiGrid kpi={d.kpi} />

        <TurnFlow
          turnCount={d.turnCount}
          segments={d.segments}
          legend={d.legend}
          selected={turnSel}
          onPick={pickTurn}
        />

        <div>
          <div class="flex items-baseline" style="gap:9px;margin-bottom:9px">
            <span class="text-text-secondary font-semibold" style="font-size:12.5px"
              >턴별 프롬프트</span
            >
            <span class="flex-1"></span>
            <span
              class="text-text-secondary hover:text-text cursor-pointer font-semibold whitespace-nowrap"
              style="font-size:11.5px"
              role="button"
              tabindex="0"
              onclick={collapseAll}
              onkeydown={onCollapseKeydown}>{collapseLabel}</span
            >
          </div>
          {#each d.turns as turn (turn.n)}
            <TurnCard
              {turn}
              open={!!turnOpen[turn.n]}
              sel={turnSel === turn.n}
              onToggle={() => toggleTurn(turn.n)}
            />
          {/each}
        </div>

        <FileChanges
          files={d.files}
          open={filesOpen}
          onToggle={() => (filesOpen = !filesOpen)}
        />
      </div>

      <div
        class="bg-surface flex flex-none items-center"
        style="gap:10px;padding:14px 24px;border-top:1px solid #f1ece4"
      >
        <button
          type="button"
          class="bg-surface border-border text-text hover:border-border-strong flex cursor-pointer items-center border font-semibold whitespace-nowrap"
          style="gap:8px;font-size:13px;padding:10px 15px;border-radius:9px"
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
          style="gap:8px;font-size:13px;padding:10px 15px;border-radius:9px"
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
        <span
          class="text-text-muted"
          style="font-size:12.5px;font-variant-numeric:tabular-nums">{d.position}</span
        >
        <span class="flex-1"></span>
        <span
          class="text-text-secondary hover:text-text cursor-pointer font-semibold whitespace-nowrap"
          style="font-size:12px;margin-right:4px"
          role="button"
          tabindex="0"
          onclick={collapseAll}
          onkeydown={onCollapseKeydown}>{collapseLabel}</span
        >
        <span
          class="text-text-muted flex items-center whitespace-nowrap"
          style="gap:7px;font-size:11.5px"
        >
          <span
            class="border-border border"
            style="font-family:ui-monospace,Menlo,monospace;border-radius:5px;padding:3px 6px"
            >J</span
          >
          <span
            class="border-border border"
            style="font-family:ui-monospace,Menlo,monospace;border-radius:5px;padding:3px 6px"
            >K</span
          > 이동
          <span
            class="border-border border"
            style="font-family:ui-monospace,Menlo,monospace;border-radius:5px;padding:3px 6px;margin-left:6px"
            >Esc</span
          > 닫기</span
        >
      </div>
    </div>
  </div>
{/if}
