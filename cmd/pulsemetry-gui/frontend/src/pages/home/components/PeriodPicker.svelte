<script lang="ts">
  import {
    period,
    todayRange,
    weekRange,
    monthRange,
    periodDateText,
    isoDate,
    toDate,
    monthStart,
    monthGridDays,
  } from "../../../lib/period.svelte";
  import type { PeriodRange } from "../../../lib/types";
  import CalendarIcon from "../../../lib/icons/CalendarIcon.svelte";
  import ChevronDownIcon from "../../../lib/icons/ChevronDownIcon.svelte";
  import ChevronLeftIcon from "../../../lib/icons/ChevronLeftIcon.svelte";
  import ChevronRightIcon from "../../../lib/icons/ChevronRightIcon.svelte";

  // align: 팝업을 트리거의 어느 모서리에 붙일지. 헤더(우측 배치)는 right,
  // Activity 필터처럼 좌측에 놓일 때는 left 로 화면 밖 클리핑을 막는다.
  let { align = "right" }: { align?: "left" | "right" } = $props();

  let open = $state(false);
  let anchor = $state<string | null>(null);
  let hover = $state<string | null>(null);
  let displayMonth = $state(monthStart(toDate(period.value.start)));

  const PRESETS: { label: string; make: () => PeriodRange }[] = [
    { label: "오늘", make: todayRange },
    { label: "이번 주", make: weekRange },
    { label: "이번 달", make: monthRange },
  ];
  const WEEKDAYS = ["월", "화", "수", "목", "금", "토", "일"];
  const today = isoDate(new Date());

  const grid = $derived(monthGridDays(displayMonth));

  const range = $derived.by(() => {
    if (anchor) {
      const other = hover ?? anchor;
      return anchor <= other
        ? { s: anchor, e: other }
        : { s: other, e: anchor };
    }
    return { s: period.value.start, e: period.value.end };
  });

  const isPreset = (make: () => PeriodRange) => {
    const r = make();
    return r.start === period.value.start && r.end === period.value.end;
  };

  function selectPreset(make: () => PeriodRange) {
    period.value = make();
    anchor = null;
    hover = null;
    open = false;
  }

  function selectDay(iso: string) {
    if (!anchor) {
      anchor = iso;
      return;
    }
    const [start, end] = anchor <= iso ? [anchor, iso] : [iso, anchor];
    period.value = { start, end };
    anchor = null;
    hover = null;
    open = false;
  }

  function toggle() {
    if (!open) displayMonth = monthStart(toDate(period.value.start));
    open = !open;
    anchor = null;
  }

  const shiftMonth = (delta: number) =>
    (displayMonth = new Date(
      displayMonth.getFullYear(),
      displayMonth.getMonth() + delta,
      1,
    ));
</script>

<div class="relative">
  <button
    type="button"
    onclick={toggle}
    class="bg-surface border-border text-text hover:border-border-strong flex items-center rounded-[10px] border whitespace-nowrap transition-colors duration-[120ms] ease-in-out"
    style="gap:8px;padding:9px 14px;font-size:13px"
  >
    <CalendarIcon size={15} strokeWidth={1.7} class="text-text-secondary" />
    <span class="font-semibold">{periodDateText(period.value)}</span>
    <ChevronDownIcon
      size={13}
      class="text-text-muted {open
        ? 'rotate-180 transition-transform'
        : 'transition-transform'}"
    />
  </button>

  {#if open}
    <button
      type="button"
      class="fixed inset-0 z-10 cursor-default"
      aria-label="닫기"
      onclick={() => (open = false)}
    ></button>
    <div
      class="bg-surface border-border absolute z-20 border {align === 'left'
        ? 'left-0'
        : 'right-0'}"
      style="top:calc(100% + 8px);border-radius:14px;padding:14px;width:288px"
    >
      <div class="flex" style="gap:6px;margin-bottom:12px">
        {#each PRESETS as p (p.label)}
          <button
            type="button"
            onclick={() => selectPreset(p.make)}
            class="flex-1 cursor-pointer rounded-[8px] border-none transition-colors duration-[120ms] ease-in-out {isPreset(
              p.make,
            )
              ? 'bg-accent-soft text-accent font-semibold'
              : 'text-text-secondary hover:bg-surface-hover bg-transparent'}"
            style="padding:8px 0;font-size:12.5px">{p.label}</button
          >
        {/each}
      </div>

      <div class="flex items-center justify-between" style="margin-bottom:8px">
        <button
          type="button"
          aria-label="이전 달"
          onclick={() => shiftMonth(-1)}
          class="text-text-secondary hover:bg-surface-hover flex cursor-pointer items-center justify-center rounded-[8px] border-none bg-transparent"
          style="width:28px;height:28px"
        >
          <ChevronLeftIcon size={16} strokeWidth={1.8} />
        </button>
        <div class="text-text font-semibold" style="font-size:13px">
          {displayMonth.getFullYear()}년 {displayMonth.getMonth() + 1}월
        </div>
        <button
          type="button"
          aria-label="다음 달"
          onclick={() => shiftMonth(1)}
          class="text-text-secondary hover:bg-surface-hover flex cursor-pointer items-center justify-center rounded-[8px] border-none bg-transparent"
          style="width:28px;height:28px"
        >
          <ChevronRightIcon size={16} strokeWidth={1.8} />
        </button>
      </div>

      <div
        class="grid"
        style="grid-template-columns:repeat(7,1fr);margin-bottom:4px"
      >
        {#each WEEKDAYS as d}
          <div class="text-text-muted text-center" style="font-size:11px">
            {d}
          </div>
        {/each}
      </div>

      <div
        class="grid"
        style="grid-template-columns:repeat(7,1fr);gap:2px"
        role="grid"
        tabindex="-1"
        onmouseleave={() => (hover = null)}
      >
        {#each grid as day (day.getTime())}
          {@const iso = isoDate(day)}
          {@const inMonth = day.getMonth() === displayMonth.getMonth()}
          {@const inRange = iso >= range.s && iso <= range.e}
          {@const isEdge = iso === range.s || iso === range.e}
          {@const isToday = iso === today}
          <button
            type="button"
            onclick={() => selectDay(iso)}
            onmouseenter={() => (hover = iso)}
            class="relative flex cursor-pointer items-center justify-center border-none {inRange
              ? ''
              : 'hover:bg-surface-hover bg-transparent'}"
            style="height:32px;border-radius:8px;font-size:12.5px;{inMonth
              ? ''
              : 'opacity:0.35;'}{isEdge
              ? 'background:var(--color-accent);color:var(--color-surface);font-weight:600;'
              : inRange
                ? 'background:var(--color-accent-soft);color:var(--color-accent);'
                : 'color:var(--color-text);'}"
          >
            {day.getDate()}
            {#if isToday && !isEdge}
              <span
                class="absolute"
                style="bottom:4px;width:3px;height:3px;border-radius:50%;background:var(--color-accent)"
              ></span>
            {/if}
          </button>
        {/each}
      </div>
    </div>
  {/if}
</div>
