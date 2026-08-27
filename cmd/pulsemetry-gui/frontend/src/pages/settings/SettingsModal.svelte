<script lang="ts">
  import { getAppInfo, type AppInfo } from "../../lib/backend";
  import { AGENT_STYLE } from "../../lib/agents";
  import Mascot from "../../lib/components/Mascot.svelte";
  import AgentBadge from "../../lib/components/AgentBadge.svelte";
  import Dot from "../../lib/components/Dot.svelte";
  import Pill from "../../lib/components/Pill.svelte";
  import SlidersIcon from "../../lib/icons/SlidersIcon.svelte";
  import RefreshIcon from "../../lib/icons/RefreshIcon.svelte";
  import ChevronDownIcon from "../../lib/icons/ChevronDownIcon.svelte";
  import XIcon from "../../lib/icons/XIcon.svelte";
  import CheckIcon from "../../lib/icons/CheckIcon.svelte";
  import {
    HEALTH,
    PREFS,
    PREF_DEFAULTS,
    CONNECTIONS,
    CONN_STATUS,
    COLLECTION,
    TRANSPORT,
    POLICY,
  } from "./data";

  let { open, onClose }: { open: boolean; onClose: () => void } = $props();

  let toggles = $state({ ...PREF_DEFAULTS });

  let appInfo = $state<AppInfo>({ name: "Pulsemetry", version: "" });
  getAppInfo().then((i) => (appInfo = i));

  // 마지막 행은 구분선 없음
  const line = (i: number, n: number) =>
    i === n - 1 ? "transparent" : "#f5f1ea";
</script>

<svelte:window
  onkeydown={(e) => {
    if (open && e.key === "Escape") {
      e.preventDefault();
      onClose();
    }
  }}
/>

{#if open}
  <div
    class="fixed inset-0 flex items-center justify-center"
    style="z-index:60"
  >
    <button
      type="button"
      aria-label="닫기"
      onclick={onClose}
      class="absolute inset-0 cursor-default border-none"
      style="background:rgba(27,26,24,0.22);animation:fadeIn 160ms ease-out"
    ></button>

    <div
      class="bg-surface border-border relative flex flex-col border"
      style="width:560px;max-width:calc(100vw - 64px);max-height:calc(100vh - 96px);border-radius:16px;box-shadow:0 18px 48px rgba(27,26,24,0.16);animation:popIn 200ms cubic-bezier(0.32,0.72,0,1)"
    >
      <div
        class="border-track flex flex-none items-center border-b"
        style="gap:12px;padding:18px 22px 14px"
      >
        <span
          class="bg-accent-soft text-accent flex flex-none items-center justify-center"
          style="width:30px;height:30px;border-radius:9px"
        >
          <SlidersIcon size={16} strokeWidth={1.8} knobFill="var(--color-accent-soft)" />
        </span>
        <span
          class="text-text font-bold"
          style="font-size:16px;letter-spacing:-0.01em;flex:1"
        >
          설정
        </span>
        <button
          type="button"
          aria-label="닫기"
          onclick={onClose}
          class="border-border text-text-secondary hover:border-border-strong flex flex-none cursor-pointer items-center justify-center border bg-transparent transition-colors duration-[120ms] ease-in-out"
          style="width:28px;height:28px;border-radius:9px"
        >
          <XIcon size={13} />
        </button>
      </div>

      <div class="overflow-y-auto" style="flex:1;padding:16px 22px 20px">
        <div
          class="bg-surface-hover"
          style="border:1px solid #efe9e1;border-radius:12px;padding:14px 16px;margin-bottom:20px"
        >
          <div class="flex items-center" style="gap:12px;margin-bottom:14px">
            <Mascot pose="found" height={44} />
            <div style="min-width:0;flex:1">
              <div
                class="text-text font-bold whitespace-nowrap"
                style="font-size:13.5px;margin-bottom:3px"
              >
                모든 연결이 정상이에요
              </div>
              <div
                class="text-text-muted whitespace-nowrap"
                style="font-size:11.5px"
              >
                마지막 확인 2분 전
              </div>
            </div>
            <button
              type="button"
              class="bg-surface border-border text-text hover:border-border-strong flex flex-none cursor-pointer items-center border whitespace-nowrap transition-colors duration-[120ms] ease-in-out"
              style="gap:7px;border-radius:9px;padding:8px 12px;font-size:12px"
            >
              <RefreshIcon size={13} class="text-text-secondary" />
              상태 새로고침
            </button>
          </div>
          <div
            class="grid"
            style="grid-template-columns:repeat(2,minmax(0,1fr));gap:0 20px;border-top:1px solid #efe9e1;padding-top:4px"
          >
            {#each HEALTH as h (h.name)}
              <span
                class="grid items-center"
                style="grid-template-columns:7px minmax(0,1fr) auto;gap:9px;padding:6px 0"
              >
                <Dot color="var(--color-success)" />
                <span class="text-text truncate font-semibold" style="font-size:12px">
                  {h.name}
                </span>
                <span
                  class="text-text-muted whitespace-nowrap"
                  style="font-size:11.5px"
                >
                  {h.state}
                </span>
              </span>
            {/each}
          </div>
        </div>

        <div
          class="text-text-muted font-semibold"
          style="font-size:11.5px;letter-spacing:0.02em;margin-bottom:2px"
        >
          설정
        </div>
        {#each PREFS as p, i (p.key)}
          <div
            class="grid items-center"
            style="grid-template-columns:32px minmax(0,1fr) auto;gap:12px;padding:13px 0;border-bottom:1px solid {line(
              i,
              PREFS.length,
            )}"
          >
            <span
              class="text-accent flex items-center justify-center"
              style="width:32px;height:32px;border-radius:10px;background:#f4f0e9;font-size:14px"
            >
              {p.icon}
            </span>
            <span style="min-width:0">
              <span
                class="text-text block truncate font-semibold"
                style="font-size:13.5px;margin-bottom:3px"
              >
                {p.name}
              </span>
              <span
                class="text-text-muted block truncate"
                style="font-size:11.5px"
              >
                {p.desc}
              </span>
              {#if p.dbPath}
                <span
                  class="flex items-center"
                  style="gap:8px;margin-top:6px;min-width:0"
                >
                  <span
                    class="truncate"
                    style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:10.5px;color:#a8a29a;min-width:0;direction:rtl;text-align:left"
                    ><bdi>{p.dbPath}</bdi></span
                  >
                  <span class="flex-none" style="font-size:10.5px;color:#a8a29a"
                    >· {p.dbSize}</span
                  >
                </span>
              {/if}
            </span>
            {#if p.kind === "toggle"}
              <button
                type="button"
                role="switch"
                aria-checked={toggles[p.key]}
                aria-label={p.name}
                onclick={() => (toggles[p.key] = !toggles[p.key])}
                class="flex flex-none cursor-pointer items-center border-none"
                style="width:40px;height:23px;border-radius:999px;padding:3px;background:{toggles[
                  p.key
                ]
                  ? 'var(--color-accent)'
                  : '#ddd6cc'};transition:background 260ms cubic-bezier(0.32,0.72,0,1)"
              >
                <span
                  class="block"
                  style="width:17px;height:17px;border-radius:50%;background:#fff;box-shadow:0 1px 2px rgba(27,26,24,0.2);transform:translateX({toggles[
                    p.key
                  ]
                    ? '17px'
                    : '0px'});transition:transform 260ms cubic-bezier(0.32,0.72,0,1)"
                ></span>
              </button>
            {:else}
              <button
                type="button"
                class="border-border text-text hover:border-border-strong flex flex-none cursor-pointer items-center border bg-transparent whitespace-nowrap transition-colors duration-[120ms] ease-in-out"
                style="gap:9px;border-radius:9px;padding:7px 11px;font-size:12.5px"
              >
                {p.value}
                <ChevronDownIcon size={12} strokeWidth={2.4} class="text-text-muted" />
              </button>
            {/if}
          </div>
        {/each}

        <div
          class="text-text-muted font-semibold"
          style="font-size:11.5px;letter-spacing:0.02em;margin:22px 0 2px"
        >
          연결 상태
        </div>
        {#each CONNECTIONS as c, i (c.id)}
          {@const st = CONN_STATUS[c.state]}
          <div
            class="grid items-center"
            style="grid-template-columns:32px minmax(0,1fr) auto;gap:12px;padding:12px 0;border-bottom:1px solid {line(
              i,
              CONNECTIONS.length,
            )}"
          >
            <AgentBadge agent={c.id} size={32} />
            <span style="min-width:0">
              <span
                class="block truncate font-semibold"
                style="font-size:13.5px;margin-bottom:3px;color:{c.state === 'off'
                  ? 'var(--color-text-secondary)'
                  : 'var(--color-text)'}"
              >
                {AGENT_STYLE[c.id].name}
              </span>
              <span
                class="text-text-muted block truncate"
                style="font-size:11.5px"
              >
                {c.seen}
              </span>
            </span>
            <span class="flex flex-none items-center" style="gap:9px">
              {#if st.action}
                <button
                  type="button"
                  class="text-accent hover:text-accent-hover cursor-pointer border-none bg-transparent font-semibold whitespace-nowrap"
                  style="font-size:12px"
                >
                  연결
                </button>
              {/if}
              <Pill
                label={st.label}
                fg={st.fg}
                bg={st.bg}
                fontSize={11.5}
                padding="5px 10px"
              />
            </span>
          </div>
        {/each}

        <div
          class="text-text-muted font-semibold"
          style="font-size:11.5px;letter-spacing:0.02em;margin:22px 0 8px"
        >
          보안 및 데이터 수집
        </div>

        <div
          class="flex items-start"
          style="gap:10px;background:#fdf6ec;border:1px solid #efdfc4;border-radius:11px;padding:11px 14px;margin-bottom:14px"
        >
          <svg
            viewBox="0 0 24 24"
            class="flex-none"
            style="width:15px;height:15px;margin-top:2px"
            fill="none"
            stroke="#8b6b36"
            stroke-width="1.9"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M12 4.5 21 19.5H3z" />
            <path d="M12 10v4" />
            <circle cx="12" cy="16.8" r="0.9" fill="#8b6b36" />
          </svg>
          <div style="font-size:12px;line-height:1.6;color:#5d5852">
            아래 항목은 <strong
              class="text-text"
              style="font-weight:600">조직의 중앙 서버로 전송</strong
            >됩니다. 조직 정책으로 관리되며 이 기기에서 변경할 수 없어요.
          </div>
        </div>

        {#each COLLECTION as c, i (c.key)}
          <div
            class="grid items-center"
            style="grid-template-columns:26px minmax(0,1fr) auto;gap:11px;padding:9px 0;border-bottom:1px solid {line(
              i,
              COLLECTION.length,
            )}"
          >
            <span
              class="flex items-center justify-center"
              style="width:26px;height:26px;border-radius:8px;font-size:12px;background:{c.sent
                ? '#f4f0e9'
                : 'var(--color-inactive-soft)'};color:{c.sent
                ? 'var(--color-accent)'
                : '#a8a29a'}"
            >
              {c.icon}
            </span>
            <span style="min-width:0">
              <span
                class="text-text block truncate font-semibold"
                style="font-size:12.5px;margin-bottom:3px"
              >
                {c.label}
              </span>
              <span
                class="block truncate"
                style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:10.5px;color:#a8a29a"
              >
                {c.key}
              </span>
            </span>
            <span
              class="inline-flex items-center font-semibold whitespace-nowrap justify-self-end"
              style="gap:6px;font-size:11px;border-radius:7px;padding:5px 9px;color:{c.sent
                ? 'var(--color-accent)'
                : 'var(--color-inactive)'};background:{c.sent
                ? 'var(--color-accent-soft)'
                : 'var(--color-inactive-soft)'}"
            >
              {#if c.sent}
                <CheckIcon size={10} strokeWidth={2.8} class="flex-none" />
              {:else}
                <XIcon size={10} strokeWidth={2.8} class="flex-none" />
              {/if}
              {c.sent ? "전송" : "제외"}
            </span>
          </div>
        {/each}

        <div
          class="grid"
          style="grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:10px;margin-top:14px"
        >
          <div
            class="bg-surface-hover"
            style="border:1px solid #efe9e1;border-radius:11px;padding:12px 14px"
          >
            <div class="text-text-muted" style="font-size:11px;margin-bottom:7px">
              전송 대상
            </div>
            <div
              class="text-text"
              style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:11.5px;margin-bottom:9px;overflow-wrap:anywhere"
            >
              {TRANSPORT.target}
            </div>
            <div
              class="text-text-secondary flex items-center"
              style="gap:7px;font-size:11.5px"
            >
              <Dot size={6} color="var(--color-success)" />
              {TRANSPORT.status}
            </div>
          </div>
          <div
            class="bg-surface-hover"
            style="border:1px solid #efe9e1;border-radius:11px;padding:12px 14px"
          >
            <div class="text-text-muted" style="font-size:11px;margin-bottom:7px">
              정책 출처
            </div>
            <div
              class="text-text truncate font-semibold"
              style="font-size:12.5px;margin-bottom:6px"
            >
              {POLICY.org}
            </div>
            <div
              class="text-text-muted whitespace-pre-line"
              style="font-size:11.5px;line-height:1.5"
            >
              {POLICY.detail}
            </div>
          </div>
        </div>

        <div
          class="text-text-muted"
          style="font-size:11.5px;line-height:1.6;margin-top:14px"
        >
          수집 항목 변경은 조직 관리자에게 문의하세요.
        </div>
      </div>

      <div
        class="border-track bg-surface-hover flex flex-none items-center border-t"
        style="gap:12px;padding:12px 22px;border-radius:0 0 16px 16px"
      >
        <span
          class="text-text-muted truncate"
          style="font-size:12px;min-width:0"
        >
          {appInfo.name} {appInfo.version}
        </span>
      </div>
    </div>
  </div>
{/if}
