<script lang="ts">
  import { AGENT_STYLE } from "../../lib/agents";
  import Mascot from "../../lib/components/Mascot.svelte";
  import SlidersIcon from "../../lib/icons/SlidersIcon.svelte";
  import {
    openMainWindow,
    openMainSettings,
    quitApp,
  } from "../../lib/backend";
  import { vendorUsage } from "../home/data";
  import { TRAY_SESSIONS, TRAY_SYNCED_TEXT } from "./data";

  // 새로고침 — 실데이터 연동 전까지는 1.8초 스핀 목업
  let pulling = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  const pull = () => {
    if (pulling) return;
    pulling = true;
    timer = setTimeout(() => (pulling = false), 1800);
  };
  $effect(() => () => clearTimeout(timer));

  // 남은 비율이 줄수록 벤더색 → 경고 → 위험 (VendorCard 와 동일 규칙)
  const barFg = (pct: number, fg: string) =>
    pct >= 50 ? fg : pct >= 25 ? "var(--color-warning)" : "var(--color-danger)";
  const valueFg = (pct: number) =>
    pct >= 25 ? "var(--color-text)" : "var(--color-danger-strong)";
</script>

<div
  class="bg-bg flex h-screen flex-col overflow-hidden"
  style="animation:trayIn 180ms cubic-bezier(0.32,0.72,0,1)"
>
  <div
    class="bg-surface flex flex-none items-center"
    style="gap:11px;padding:12px 16px;border-bottom:1px solid #ede7de"
  >
    <Mascot pose="view-front" height={28} />
    <span
      class="text-text flex-none font-bold"
      style="font-size:14px;letter-spacing:-0.01em"
    >
      Pulsemetry
    </span>
    <span
      class="text-text-secondary flex flex-none items-center"
      style="gap:6px;font-size:11.5px"
    >
      <span
        class="flex-none"
        style="width:6px;height:6px;border-radius:50%;background:var(--color-success)"
      ></span>모니터링 중
    </span>
    <span style="flex:1"></span>
    <span class="whitespace-nowrap" style="font-size:11.5px;color:#b3aba0">
      {pulling ? "조회 중" : TRAY_SYNCED_TEXT}
    </span>
    <button
      type="button"
      title={pulling ? "조회 중" : "새로고침"}
      onclick={pull}
      class="flex flex-none items-center justify-center border bg-transparent transition-[opacity,border-color] duration-[180ms] ease-in-out {pulling
        ? 'text-text-muted cursor-default'
        : 'text-accent hover:border-border-strong hover:bg-surface-hover cursor-pointer'}"
      style="width:26px;height:26px;border-radius:8px;border-color:{pulling
        ? '#efe9e1'
        : 'var(--color-border)'};opacity:{pulling ? '0.6' : '1'}"
    >
      <svg
        viewBox="0 0 24 24"
        style="width:13px;height:13px;animation:{pulling
          ? 'spin 900ms linear infinite'
          : 'none'};transform-origin:50% 50%"
        fill="none"
        stroke="currentColor"
        stroke-width="2.2"
        stroke-linecap="round"
        stroke-linejoin="round"
      >
        <path d="M20 12a8 8 0 1 1-2.4-5.7" />
        <path d="M20 4.5V9h-4.5" />
      </svg>
    </button>
  </div>

  <div class="overflow-y-auto" style="flex:1">
    <div style="padding:12px 16px 4px">
      <div
        class="grid items-start"
        style="grid-template-columns:repeat(3,minmax(0,1fr));gap:12px"
      >
        {#each vendorUsage as v (v.id)}
          {@const style = AGENT_STYLE[v.id]}
          <div
            class="bg-surface border-border flex flex-col border"
            style="border-radius:12px;padding:15px 17px"
          >
            <div class="flex items-center" style="gap:10px;margin-bottom:13px">
              <span
                class="flex flex-none items-center justify-center"
                style="width:30px;height:30px;border-radius:9px;background:{style.bg};color:{style.fg};font-size:{Math.min(
                  style.fontMd,
                  15,
                )}px;font-weight:{style.weight}"
              >
                {style.glyph}
              </span>
              <span
                class="text-text truncate font-bold"
                style="font-size:15px;letter-spacing:-0.01em;min-width:0"
              >
                {style.name}
              </span>
              <span style="flex:1"></span>
              <span
                class="text-text-muted flex-none whitespace-nowrap"
                style="font-size:11.5px"
              >
                {v.plan}
              </span>
              <span
                class="flex-none"
                style="width:8px;height:8px;border-radius:50%;background:var(--color-success)"
              ></span>
            </div>

            <div
              class="border-track flex items-baseline border-b"
              style="gap:8px;margin-bottom:13px;padding-bottom:12px"
            >
              <span
                class="text-text font-bold"
                style="font-size:22px;letter-spacing:-0.025em;font-variant-numeric:tabular-nums"
              >
                {v.spend}
              </span>
              <span
                class="text-text-secondary whitespace-nowrap"
                style="font-size:12px"
              >
                오늘
              </span>
              <span style="flex:1"></span>
              <span
                class="text-text-muted whitespace-nowrap"
                style="font-size:11.5px"
              >
                {v.tokens}
              </span>
            </div>

            {#each v.limits as l (l.scope)}
              <div style="margin-bottom:12px">
                <div
                  class="flex items-baseline"
                  style="gap:10px;margin-bottom:5px"
                >
                  <span
                    class="text-text-secondary truncate"
                    style="font-size:11.5px;min-width:0"
                  >
                    {l.scope}
                  </span>
                  <span style="flex:1"></span>
                  <span
                    class="text-text-muted flex-none whitespace-nowrap"
                    style="font-size:11px"
                  >
                    {l.reset}
                  </span>
                </div>
                <div
                  class="flex items-baseline"
                  style="gap:7px;margin-bottom:6px"
                >
                  <span
                    class="font-bold whitespace-nowrap"
                    style="font-size:15px;font-variant-numeric:tabular-nums;color:{valueFg(
                      l.pct,
                    )}"
                  >
                    {l.remain}
                  </span>
                  <span
                    class="text-text-muted whitespace-nowrap"
                    style="font-size:11.5px"
                  >
                    {l.used}
                  </span>
                </div>
                <div class="bg-track" style="height:6px;border-radius:999px">
                  <div
                    style="height:100%;border-radius:999px;background:{barFg(
                      l.pct,
                      style.fg,
                    )};width:{l.pct}%"
                  ></div>
                </div>
              </div>
            {/each}

            <div
              class="mt-auto truncate"
              style="font-size:11px;color:#b3aba0;padding-top:2px"
            >
              {v.credential}
            </div>
          </div>
        {/each}
      </div>
    </div>

    <div
      class="grid items-stretch"
      style="grid-template-columns:minmax(0,1fr) 296px;gap:12px;padding:12px 16px 14px"
    >
      <div class="flex flex-col">
        <div
          class="text-text-muted font-semibold"
          style="font-size:11.5px;letter-spacing:0.02em;margin-bottom:9px"
        >
          최근 세션
        </div>
        <div
          class="grid"
          style="grid-template-columns:repeat(2,minmax(0,1fr));gap:10px"
        >
          {#each TRAY_SESSIONS as s (s.id)}
            {@const style = AGENT_STYLE[s.agentId]}
            <button
              type="button"
              onclick={openMainWindow}
              class="bg-surface hover:bg-surface-hover grid cursor-pointer items-center text-left"
              style="grid-template-columns:8px 26px minmax(0,1fr);gap:10px;border:1px solid var(--color-border);border-left:3px solid {s.live
                ? 'var(--color-sand)'
                : 'var(--color-border)'};border-radius:11px;padding:9px 12px"
            >
              <span
                style="width:7px;height:7px;border-radius:50%;background:{s.live
                  ? 'var(--color-sand)'
                  : 'var(--color-border-strong)'};animation:{s.live
                  ? 'livePulse 2s ease-out infinite'
                  : 'none'}"
              ></span>
              <span
                class="flex items-center justify-center"
                style="width:26px;height:26px;border-radius:8px;background:{style.bg};color:{style.fg};font-size:{style.fontSm}px;font-weight:{style.weight}"
              >
                {style.glyph}
              </span>
              <span style="min-width:0">
                <span
                  class="text-text block truncate font-semibold"
                  style="font-size:12.5px;margin-bottom:3px"
                >
                  {s.title}
                </span>
                <span
                  class="text-text-muted block truncate"
                  style="font-size:11px"
                >
                  {s.sub}
                </span>
              </span>
            </button>
          {/each}
        </div>
      </div>

      <div class="flex flex-col">
        <div
          class="invisible font-semibold"
          style="font-size:11.5px;letter-spacing:0.02em;margin-bottom:9px"
        >
          코치
        </div>
        <div
          class="flex items-center"
          style="flex:1;background:#fdf9f4;border:1px solid #efe7dc;border-radius:12px;padding:10px 14px;gap:11px;min-height:0"
        >
          <span class="flex-none self-end">
            <Mascot pose="normal" height={48} />
          </span>
          <div
            style="flex:1;min-width:0;font-size:12px;line-height:1.65;color:#5d5852;word-break:keep-all"
          >
            Codex 주간 한도가 <strong
              class="text-accent"
              style="font-weight:700">55%</strong
            >로<br />가장 빠듯해. 오늘은 여유 있어.
          </div>
        </div>
      </div>
    </div>
  </div>

  <div
    class="bg-surface flex flex-none items-center"
    style="gap:9px;padding:11px 16px;border-top:1px solid #ede7de"
  >
    <button
      type="button"
      onclick={openMainWindow}
      class="bg-accent hover:bg-accent-hover flex flex-none cursor-pointer items-center justify-center border-none font-semibold whitespace-nowrap transition-colors duration-[120ms] ease-in-out"
      style="gap:8px;border-radius:9px;padding:10px 20px;font-size:12.5px;color:var(--color-surface)"
    >
      Pulsemetry 열기
    </button>
    <span style="flex:1"></span>
    <button
      type="button"
      title="트레이 설정"
      onclick={openMainSettings}
      class="border-border text-text-secondary hover:border-border-strong hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border bg-transparent transition-colors duration-[120ms] ease-in-out"
      style="width:34px;height:34px;border-radius:9px"
    >
      <SlidersIcon size={15} strokeWidth={1.8} knobFill="var(--color-surface)" />
    </button>
    <button
      type="button"
      title="종료"
      onclick={quitApp}
      class="border-border text-text-secondary hover:border-border-strong hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border bg-transparent transition-colors duration-[120ms] ease-in-out"
      style="width:34px;height:34px;border-radius:9px"
    >
      <svg
        viewBox="0 0 24 24"
        style="width:15px;height:15px"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
      >
        <path d="M12 4v8" />
        <path d="M7.5 7A7 7 0 1 0 16.5 7" />
      </svg>
    </button>
  </div>
</div>
