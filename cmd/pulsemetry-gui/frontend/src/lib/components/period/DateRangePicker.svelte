<script lang="ts">
  import {
    period,
    todayRange,
    weekRange,
    monthRange,
    isoDate,
    toDate,
    addDays,
    monthGridDays,
    periodDays,
  } from "../../period.svelte";
  import type { PeriodRange } from "../../types";
  import { TODAY, RETAIN_FROM } from "../../../pages/home/chart";
  import ChevronDownIcon from "../../icons/ChevronDownIcon.svelte";

  // 날짜 범위 선택 — 팝업 안에서는 draft만 바꾸고 「적용」할 때 전역 period에 반영한다.

  let open = $state(false);
  let draft = $state<PeriodRange>({ ...period.value });
  // 첫 클릭이 앵커, 두 번째 클릭이 범위를 닫는다 — 순서 무관
  let anchor = $state<string | null>(null);
  let viewY = $state(toDate(period.value.start).getFullYear());
  let viewM = $state(toDate(period.value.start).getMonth());

  const committed = $derived(period.value);
  const p = $derived(draft);

  const WEEKDAYS = ["월", "화", "수", "목", "금", "토", "일"];

  const fmt = (s: string) => {
    const d = toDate(s);
    return `${d.getMonth() + 1}.${d.getDate()}`;
  };
  const long = (s: string) => {
    const d = toDate(s);
    return `${d.getMonth() + 1}월 ${d.getDate()}일`;
  };
  const rangeShort = $derived(
    committed.start === committed.end
      ? long(committed.start)
      : `${fmt(committed.start)} ~ ${fmt(committed.end)}`,
  );

  function setRange(a: string, b: string) {
    draft = { start: a, end: b };
    anchor = null;
    viewY = toDate(a).getFullYear();
    viewM = toDate(a).getMonth();
  }

  function pickDay(iso: string) {
    if (!anchor) {
      anchor = iso;
      draft = { start: iso, end: iso };
      return;
    }
    const a = anchor < iso ? anchor : iso;
    const b = anchor < iso ? iso : anchor;
    draft = { start: a, end: b };
    anchor = null;
  }

  function toggle() {
    if (!open) {
      draft = { ...period.value };
      anchor = null;
      viewY = toDate(period.value.start).getFullYear();
      viewM = toDate(period.value.start).getMonth();
    }
    open = !open;
  }

  function close() {
    open = false;
    anchor = null;
  }

  function apply() {
    period.value = { ...draft };
    close();
  }

  function shiftMonth(n: number) {
    const m = viewM + n;
    viewY += Math.floor(m / 12);
    viewM = ((m % 12) + 12) % 12;
  }

  const yearRange = () => {
    const t = toDate(TODAY);
    return { start: isoDate(addDays(t, -364)), end: TODAY };
  };

  const presets = $derived(
    (
      [
        ["오늘", todayRange()],
        ["이번 주", weekRange()],
        ["이번 달", monthRange()],
        ["최근 1년", yearRange()],
      ] as const
    ).map(([name, r]) => ({
      name,
      on: p.start === r.start && p.end === r.end,
      pick: () => setRange(r.start, r.end),
    })),
  );

  interface Cell {
    day: number;
    iso: string;
    fg: string;
    bg: string;
    weight: number;
    radius: string;
    tooOld: boolean;
    isToday: boolean;
    edge: boolean;
  }

  const cells = $derived.by((): Cell[] => {
    return monthGridDays(new Date(viewY, viewM, 1)).map((d) => {
      const iso = isoDate(d);
      const inMonth = d.getMonth() === viewM;
      const tooOld = iso < RETAIN_FROM;
      const inRange = iso >= p.start && iso <= p.end;
      const isStart = iso === p.start;
      const isEnd = iso === p.end;
      const edge = isStart || isEnd;
      const weekend = (d.getDay() + 6) % 7 >= 5;
      let fg = "var(--color-text)";
      if (edge) fg = "var(--color-surface)";
      else if (!inMonth || tooOld) fg = "#c9c3ba";
      else if (weekend && !inRange) fg = "#7e8cb8";
      else if (!inRange) fg = "var(--color-text-secondary)";
      return {
        day: d.getDate(),
        iso,
        fg,
        bg: edge ? "var(--color-accent)" : inRange ? "#f2ebdd" : "transparent",
        weight: edge ? 700 : inRange ? 600 : 400,
        radius:
          isStart && isEnd
            ? "9px"
            : isStart
              ? "9px 4px 4px 9px"
              : isEnd
                ? "4px 9px 9px 4px"
                : inRange
                  ? "4px"
                  : "9px",
        tooOld,
        isToday: iso === TODAY,
        edge,
      };
    });
  });
</script>

<span class="relative flex-none">
  <button
    type="button"
    onclick={toggle}
    class="bg-bg text-text hover:border-border-strong flex cursor-pointer items-center border whitespace-nowrap"
    style="gap:8px;border-radius:10px;padding:9px 14px;font-size:13px;border-color:{open
      ? 'var(--color-border-strong)'
      : 'var(--color-border)'}"
  >
    <svg
      viewBox="0 0 24 24"
      style="width:15px;height:15px"
      fill="none"
      stroke="var(--color-text-secondary)"
      stroke-width="1.7"
      stroke-linecap="round"
    >
      <rect x="3" y="5" width="18" height="16" rx="3" />
      <path d="M8 3v4M16 3v4M3 10h18" />
    </svg>
    {rangeShort}
    <ChevronDownIcon
      size={13}
      strokeWidth={2.2}
      class="text-text-muted"
      rotated={open}
    />
  </button>

  {#if open}
    <button
      type="button"
      aria-label="달력 닫기"
      onclick={close}
      class="fixed cursor-default border-none bg-transparent"
      style="inset:0;z-index:40"
    ></button>
    <div
      class="bg-surface absolute border"
      style="right:0;top:44px;z-index:50;width:308px;border-color:#dfd8ce;border-radius:14px;box-shadow:0 14px 36px rgba(27,26,24,0.16);padding:14px;animation:popIn 180ms cubic-bezier(0.32,0.72,0,1)"
    >
      <div
        class="grid"
        style="grid-template-columns:repeat(2,minmax(0,1fr));gap:6px;margin-bottom:12px"
      >
        {#each presets as pr (pr.name)}
          <button
            type="button"
            onclick={pr.pick}
            class="cursor-pointer border-none text-center font-semibold whitespace-nowrap hover:bg-[#f4f0e9]"
            style="padding:8px 4px;border-radius:9px;font-size:12.5px;color:{pr.on
              ? 'var(--color-accent)'
              : 'var(--color-text-secondary)'};background:{pr.on
              ? 'var(--color-accent-soft)'
              : 'transparent'}"
          >
            {pr.name}
          </button>
        {/each}
      </div>

      <div
        class="grid items-center"
        style="grid-template-columns:26px minmax(0,1fr) 26px;margin-bottom:8px"
      >
        <button
          type="button"
          aria-label="이전 달"
          onclick={() => shiftMonth(-1)}
          class="text-text-secondary flex cursor-pointer items-center justify-center border-none bg-transparent hover:bg-[#f4f0e9]"
          style="width:26px;height:26px;border-radius:8px"
        >
          <svg
            viewBox="0 0 24 24"
            style="width:13px;height:13px"
            fill="none"
            stroke="currentColor"
            stroke-width="2.4"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="m15 5-7 7 7 7" />
          </svg>
        </button>
        <span class="text-center font-bold whitespace-nowrap" style="font-size:13px">
          {viewY}년 {viewM + 1}월
        </span>
        <button
          type="button"
          aria-label="다음 달"
          onclick={() => shiftMonth(1)}
          class="text-text-secondary flex cursor-pointer items-center justify-center border-none bg-transparent hover:bg-[#f4f0e9]"
          style="width:26px;height:26px;border-radius:8px"
        >
          <svg
            viewBox="0 0 24 24"
            style="width:13px;height:13px"
            fill="none"
            stroke="currentColor"
            stroke-width="2.4"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="m9 5 7 7-7 7" />
          </svg>
        </button>
      </div>

      <div
        class="grid"
        style="grid-template-columns:repeat(7,minmax(0,1fr));gap:2px;margin-bottom:4px"
      >
        {#each WEEKDAYS as w, i (w)}
          <span
            class="text-center"
            style="font-size:11px;color:{i >= 5
              ? '#7e8cb8'
              : 'var(--color-text-muted)'};padding:5px 0"
          >
            {w}
          </span>
        {/each}
      </div>

      <div class="grid" style="grid-template-columns:repeat(7,minmax(0,1fr));gap:2px">
        {#each cells as c (c.iso)}
          <button
            type="button"
            onclick={() => !c.tooOld && pickDay(c.iso)}
            class="relative flex items-center justify-center border-none {c.tooOld
              ? 'cursor-default'
              : 'cursor-pointer'} {c.edge
              ? 'hover:bg-accent-hover'
              : c.tooOld
                ? ''
                : 'hover:bg-[#f4f0e9]'}"
            style="height:30px;border-radius:{c.radius};font-size:12px;font-weight:{c.weight};font-variant-numeric:tabular-nums;color:{c.fg};background:{c.bg}"
          >
            {c.day}
            {#if c.isToday && !c.edge}
              <span
                class="absolute"
                style="bottom:3px;left:50%;width:3px;height:3px;margin-left:-1.5px;border-radius:50%;background:var(--color-sand)"
              ></span>
            {/if}
          </button>
        {/each}
      </div>

      <div
        class="flex items-center"
        style="gap:8px;margin-top:12px;padding-top:11px;border-top:1px solid #f1ece4"
      >
        <span class="text-text-muted truncate" style="font-size:11.5px;flex:1;min-width:0">
          {anchor ? "종료일을 선택하세요" : `${periodDays(p)}일 선택됨`}
        </span>
        <button
          type="button"
          onclick={apply}
          class="bg-accent hover:bg-accent-hover flex-none cursor-pointer border-none font-semibold whitespace-nowrap"
          style="font-size:12px;color:var(--color-surface);border-radius:8px;padding:7px 14px"
        >
          적용
        </button>
      </div>
    </div>
  {/if}
</span>
