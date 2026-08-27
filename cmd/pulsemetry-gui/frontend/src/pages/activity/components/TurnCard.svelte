<script lang="ts">
  import type { TurnDisplay } from "../data";

  let {
    turn,
    open,
    sel,
    onToggle,
  }: {
    turn: TurnDisplay;
    open: boolean;
    sel: boolean;
    onToggle?: () => void;
  } = $props();

  function onRowKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onToggle?.();
    }
  }

  function copyPrompt() {
    navigator.clipboard?.writeText(turn.prompt);
  }
  function onCopyKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      copyPrompt();
    }
  }
</script>

<div
  class="border"
  style="border-color:{sel
    ? 'var(--color-border-strong)'
    : '#efe9e1'};border-radius:12px;background:{sel
    ? 'var(--color-surface-hover)'
    : 'var(--color-surface)'};padding:12px 14px;margin-bottom:8px"
>
  <div
    class="grid cursor-pointer items-center"
    style="grid-template-columns:26px 44px 60px minmax(0,1fr) auto 14px;gap:11px"
    role="button"
    tabindex="0"
    aria-expanded={open}
    onclick={() => onToggle?.()}
    onkeydown={onRowKeydown}
  >
    <span
      class="flex items-center justify-center font-bold"
      style="width:26px;height:26px;border-radius:8px;background:{sel
        ? turn.labelDot
        : '#f4f0e9'};color:{sel
        ? 'var(--color-surface)'
        : 'var(--color-accent-hover)'};font-size:11.5px;font-variant-numeric:tabular-nums"
      >{turn.n}</span
    >
    <span
      class="text-text-secondary whitespace-nowrap"
      style="font-size:12px;font-variant-numeric:tabular-nums">{turn.time}</span
    >
    <span
      class="inline-flex items-center justify-center border font-semibold whitespace-nowrap"
      style="gap:5px;font-size:11px;border-radius:6px;padding:4px 0;color:{turn.labelFg};background:{turn.labelBg};border-color:{turn.labelBorder}"
    >
      <span
        class="flex-none"
        style="width:6px;height:6px;border-radius:2px;background:{turn.labelDot}"
      ></span>{turn.label}
    </span>
    <span
      class="text-text min-w-0 overflow-hidden text-ellipsis whitespace-nowrap"
      style="font-size:13px">{turn.preview}</span
    >
    <span
      class="text-text-muted flex-none whitespace-nowrap"
      style="font-size:11px;font-variant-numeric:tabular-nums">{turn.meta}</span
    >
    <svg
      viewBox="0 0 24 24"
      class="text-text-muted flex-none"
      style="width:12px;height:12px;transform:rotate({open
        ? 180
        : 0}deg);transition:transform 200ms cubic-bezier(0.32,0.72,0,1)"
      fill="none"
      stroke="currentColor"
      stroke-width="2.4"
      stroke-linecap="round"
      stroke-linejoin="round"><path d="m6 9 6 6 6-6"></path></svg
    >
  </div>

  {#if open}
    <div style="animation:rowIn 180ms ease-out">
      <div
        class="bg-surface"
        style="margin-top:12px;padding:11px 13px;border:1px solid #efe9e1;border-left:3px solid {turn.labelFg};border-radius:10px"
      >
        <div class="flex items-baseline" style="gap:8px;margin-bottom:7px">
          <span
            class="text-text-muted font-semibold"
            style="font-size:10.5px;letter-spacing:0.02em"
            >보낸 프롬프트 원문</span
          >
          <span class="flex-1"></span>
          <span
            class="text-text-muted whitespace-nowrap"
            style="font-size:10.5px">{turn.chars}</span
          >
          <span
            class="cursor-pointer font-semibold whitespace-nowrap"
            style="font-size:10.5px;color:var(--color-accent)"
            role="button"
            tabindex="0"
            onclick={copyPrompt}
            onkeydown={onCopyKeydown}>복사</span
          >
        </div>
        <div
          class="text-text"
          style="font-size:13px;line-height:1.75;white-space:pre-wrap;word-break:break-word"
        >
          {turn.prompt}
        </div>
      </div>

      <div
        class="grid"
        style="grid-template-columns:repeat(4,minmax(0,1fr));gap:8px;margin-top:10px"
      >
        {#each turn.stats as stat (stat.name)}
          <div
            class="bg-surface-hover"
            style="border:1px solid #efe9e1;border-radius:10px;padding:9px 11px"
          >
            <div
              style="font-size:14px;font-weight:700;font-variant-numeric:tabular-nums;margin-bottom:3px;color:{stat.fg}"
            >
              {stat.value}
            </div>
            <div
              class="text-text-muted whitespace-nowrap"
              style="font-size:10.5px"
            >
              {stat.name}
            </div>
          </div>
        {/each}
      </div>

      <div
        class="bg-surface-hover"
        style="margin-top:10px;border:1px solid #efe9e1;border-radius:10px;padding:11px 13px"
      >
        <div class="flex items-baseline" style="gap:8px;margin-bottom:9px">
          <span
            class="text-text-muted font-semibold"
            style="font-size:10.5px;letter-spacing:0.02em">도구 호출</span
          >
          <span class="flex-1"></span>
          <span
            class="text-text-muted whitespace-nowrap"
            style="font-size:10.5px">{turn.callNote}</span
          >
        </div>
        {#each turn.calls as call (call.time + call.tool + call.arg)}
          <div
            class="grid items-center"
            style="grid-template-columns:42px 62px minmax(0,1fr) 46px 14px;gap:10px;padding:5px 0"
          >
            <span
              class="text-text-muted whitespace-nowrap"
              style="font-size:11px;font-variant-numeric:tabular-nums"
              >{call.time}</span
            >
            <span
              class="text-text-secondary overflow-hidden font-semibold text-ellipsis whitespace-nowrap"
              style="font-size:11px">{call.tool}</span
            >
            <span
              class="text-text min-w-0 overflow-hidden text-ellipsis whitespace-nowrap"
              style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:11px"
              >{call.arg}</span
            >
            <span
              class="text-text-muted whitespace-nowrap"
              style="font-size:11px;font-variant-numeric:tabular-nums;text-align:right"
              >{call.dur}</span
            >
            <span
              class="flex items-center justify-center font-bold"
              style="width:14px;height:14px;border-radius:4px;font-size:9px;background:{call.ok
                ? 'var(--color-success-soft)'
                : 'var(--color-danger-soft)'};color:{call.ok
                ? '#2f7e55'
                : 'var(--color-danger-strong)'}">{call.ok ? "✓" : "✕"}</span
            >
          </div>
        {/each}
        <div
          class="text-text-muted"
          style="font-size:10.5px;margin-top:8px;padding-top:8px;border-top:1px solid #f1ece4;line-height:1.6"
        >
          출력 본문은 저장하지 않아요. 종료 코드 · 실패 수 · 오류 지문만
          남습니다.
        </div>
      </div>
    </div>
  {/if}
</div>
