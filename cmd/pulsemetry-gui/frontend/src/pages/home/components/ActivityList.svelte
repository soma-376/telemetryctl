<script lang="ts">
  import { AGENT_STYLE } from "../../../lib/agents";
  import type { ActivityData } from "../chart";

  let {
    activity,
    onViewAll,
  }: { activity: ActivityData; onViewAll?: () => void } = $props();

  // 종료는 성공/실패 판단 없이 "끝났다"는 사실만 담는다 — 초록은 성공으로 읽히고,
  // 작업 유형(검증) 배지가 이미 쓰고 있어 색이 겹친다. Activity 의 STATE_STYLE 과 같은 규칙.
  const STATUS = {
    running: { label: "진행 중", fg: "#8b6b36", bg: "var(--color-sand-soft)" },
    done: {
      label: "종료",
      fg: "var(--color-text-secondary)",
      bg: "var(--color-inactive-soft)",
    },
  } as const;

  const note = $derived(
    `${activity.total} 세션${activity.running ? ` · 진행 중 ${activity.running}` : ""}`,
  );
</script>

<div
  class="bg-surface border-border border"
  style="border-radius:14px;padding:14px 22px 8px"
>
  <div class="flex items-baseline" style="gap:10px;margin-bottom:2px">
    <div class="text-text font-bold" style="font-size:16px;letter-spacing:-0.01em">
      활동
    </div>
    <div class="text-text-muted" style="font-size:12.5px">{note}</div>
    <div style="flex:1"></div>
    <button
      type="button"
      onclick={onViewAll}
      class="text-text-secondary hover:text-text cursor-pointer border-none bg-transparent font-medium whitespace-nowrap"
      style="font-size:13px"
    >
      전체 보기 →
    </button>
  </div>

  {#each activity.rows as t, i (t.date + t.time + t.title)}
    {@const style = AGENT_STYLE[t.agent]}
    {@const st = STATUS[t.state]}
    <div
      class="grid items-center"
      style="grid-template-columns:74px 28px minmax(0,1fr) 46px 60px;gap:12px;padding:9px 0;border-bottom:1px solid {i ===
      activity.rows.length - 1
        ? 'transparent'
        : '#f5f1ea'}"
    >
      <span class="whitespace-nowrap" style="font-variant-numeric:tabular-nums">
        <span class="text-text-muted block" style="font-size:11px;margin-bottom:2px">
          {t.date}
        </span>
        <span class="text-text block font-semibold" style="font-size:12.5px">
          {t.time}
        </span>
      </span>
      <span
        class="flex items-center justify-center"
        style="width:28px;height:28px;border-radius:9px;background:{style.bg};color:{style.fg};font-size:{style.fontSm}px;font-weight:{style.weight}"
      >
        {style.glyph}
      </span>
      <span style="min-width:0">
        <span
          class="text-text block truncate font-semibold"
          style="font-size:13.5px;margin-bottom:3px"
        >
          {t.title}
        </span>
        <span class="text-text-muted block truncate" style="font-size:11px">
          {t.sub}
        </span>
      </span>
      <span
        class="text-text-secondary text-right whitespace-nowrap"
        style="font-size:12.5px;font-variant-numeric:tabular-nums"
      >
        {t.tokens}
      </span>
      <span
        class="justify-self-end font-semibold whitespace-nowrap"
        style="font-size:11px;border-radius:7px;padding:4px 9px;color:{st.fg};background:{st.bg}"
      >
        {st.label}
      </span>
    </div>
  {/each}
</div>
