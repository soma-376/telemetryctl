import {
  addDays,
  isoDate,
  monthGridDays,
  monthRange,
  period,
  periodDays,
  toDate,
  todayRange,
  usePeriod,
  weekRange,
} from "$lib/domain/period";
import type { PeriodRange } from "$lib/domain/period.types";
import { RETAIN_FROM, TODAY } from "$lib/domain/retention";
import { cssStyle } from "$lib/react-utils";
import { Fragment, useState } from "react";
import CalendarIcon from "../../icons/CalendarIcon";
import ChevronDownIcon from "../../icons/ChevronDownIcon";
import ChevronLeftIcon from "../../icons/ChevronLeftIcon";
import ChevronRightIcon from "../../icons/ChevronRightIcon";

export default function DateRangePicker() {
  usePeriod();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<PeriodRange>(period.value);
  const [anchor, setAnchor] = useState<string | null>(null);
  const [viewY, setViewY] = useState(toDate(period.value.start).getFullYear());
  const [viewM, setViewM] = useState(toDate(period.value.start).getMonth());
  const committed = period.value;
  const p = draft;
  const WEEKDAYS = ["월", "화", "수", "목", "금", "토", "일"];
  const fmt = (s: string) => {
    const d = toDate(s);

    return `${d.getMonth() + 1}.${d.getDate()}`;
  };
  const long = (s: string) => {
    const d = toDate(s);

    return `${d.getMonth() + 1}월 ${d.getDate()}일`;
  };
  const thisYear = toDate(TODAY).getFullYear();
  const ymd = (s: string) => `${toDate(s).getFullYear()}.${fmt(s)}`;
  const rangeShort = (() => {
    const a = toDate(committed.start).getFullYear();
    const b = toDate(committed.end).getFullYear();

    if (committed.start === committed.end) {
      return a === thisYear
        ? long(committed.start)
        : `${a}년 ${long(committed.start)}`;
    }
    if (a !== b) return `${ymd(committed.start)} ~ ${ymd(committed.end)}`;
    if (a !== thisYear)
      return `${ymd(committed.start)} ~ ${fmt(committed.end)}`;

    return `${fmt(committed.start)} ~ ${fmt(committed.end)}`;
  })();

  function setRange(a: string, b: string) {
    setDraft({ start: a, end: b });
    setAnchor(null);
    setViewY(toDate(a).getFullYear());
    setViewM(toDate(a).getMonth());
  }

  function pickDay(iso: string) {
    if (!anchor) {
      setAnchor(iso);
      setDraft({ start: iso, end: iso });

      return;
    }
    const a = anchor < iso ? anchor : iso;
    const b = anchor < iso ? iso : anchor;

    setDraft({ start: a, end: b });
    setAnchor(null);
  }

  function toggle() {
    if (!open) {
      setDraft(period.value);
      setAnchor(null);
      setViewY(toDate(period.value.start).getFullYear());
      setViewM(toDate(period.value.start).getMonth());
    }
    setOpen(!open);
  }

  function close() {
    setOpen(false);
    setAnchor(null);
  }

  function apply() {
    period.value = draft;
    close();
  }

  function shiftMonth(n: number) {
    const m = viewM + n;

    setViewY((y) => y + Math.floor(m / 12));
    setViewM(((m % 12) + 12) % 12);
  }

  const yearRange = () => {
    const t = toDate(TODAY);

    return { start: isoDate(addDays(t, -364)), end: TODAY };
  };
  const presets = (
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
  }));

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
  const cells = ((): Cell[] => {
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
  })();

  return (
    <>
      <span className="relative flex-none">
        <button
          type="button"
          onClick={toggle}
          style={cssStyle(
            "border-color:" +
              String(
                open ? "var(--color-border-strong)" : "var(--color-border)",
              ),
          )}
          className="bg-bg text-text hover:border-border-strong flex cursor-pointer items-center border whitespace-nowrap gap-[8px] rounded-[10px] p-[9px_14px] text-[13px]"
        >
          <CalendarIcon className="text-text-secondary" />
          {rangeShort}
          <ChevronDownIcon
            size={13}
            strokeWidth={2.2}
            rotated={open}
            className="text-text-muted"
          />
        </button>
        {open ? (
          <>
            <button
              type="button"
              aria-label="달력 닫기"
              onClick={close}
              className="fixed cursor-default border-none bg-transparent [inset:0] z-[40]"
            />
            <div className="bg-surface absolute border [right:0] [top:44px] z-[50] w-[308px] [border-color:#dfd8ce] rounded-[14px] [box-shadow:0_14px_36px_rgba(27,26,24,0.16)] p-[14px] animate-[popIn_180ms_cubic-bezier(0.32,0.72,0,1)]">
              <div className="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-[6px] mb-[12px]">
                {presets.map((pr) => (
                  <Fragment key={pr.name}>
                    <button
                      type="button"
                      onClick={pr.pick}
                      style={cssStyle(
                        "color:" +
                          String(
                            pr.on
                              ? "var(--color-accent)"
                              : "var(--color-text-secondary)",
                          ) +
                          ";background:" +
                          String(
                            pr.on ? "var(--color-accent-soft)" : "transparent",
                          ),
                      )}
                      className="cursor-pointer border-none text-center font-semibold whitespace-nowrap hover:bg-[#f4f0e9] p-[8px_4px] rounded-[9px] text-[12.5px]"
                    >
                      {pr.name}
                    </button>
                  </Fragment>
                ))}
              </div>
              <div className="grid items-center grid-cols-[26px_minmax(0,1fr)_26px] mb-[8px]">
                <button
                  type="button"
                  aria-label="이전 달"
                  onClick={() => shiftMonth(-1)}
                  className="text-text-secondary flex cursor-pointer items-center justify-center border-none bg-transparent hover:bg-[#f4f0e9] w-[26px] h-[26px] rounded-[8px]"
                >
                  <ChevronLeftIcon size={13} strokeWidth={2.4} />
                </button>
                <span className="text-center font-bold whitespace-nowrap text-[13px]">
                  {viewY}년 {viewM + 1}월
                </span>
                <button
                  type="button"
                  aria-label="다음 달"
                  onClick={() => shiftMonth(1)}
                  className="text-text-secondary flex cursor-pointer items-center justify-center border-none bg-transparent hover:bg-[#f4f0e9] w-[26px] h-[26px] rounded-[8px]"
                >
                  <ChevronRightIcon size={13} strokeWidth={2.4} />
                </button>
              </div>
              <div className="grid grid-cols-[repeat(7,minmax(0,1fr))] gap-[2px] mb-[4px]">
                {WEEKDAYS.map((w, i) => (
                  <Fragment key={w}>
                    <span
                      style={cssStyle(
                        "color:" +
                          String(
                            i >= 5 ? "#7e8cb8" : "var(--color-text-muted)",
                          ),
                      )}
                      className="text-center text-[11px] p-[5px_0]"
                    >
                      {w}
                    </span>
                  </Fragment>
                ))}
              </div>
              <div className="grid grid-cols-[repeat(7,minmax(0,1fr))] gap-[2px]">
                {cells.map((c) => (
                  <Fragment key={c.iso}>
                    <button
                      type="button"
                      onClick={() => !c.tooOld && pickDay(c.iso)}
                      style={cssStyle(
                        "border-radius:" +
                          String(c.radius) +
                          ";font-weight:" +
                          String(c.weight) +
                          ";color:" +
                          String(c.fg) +
                          ";background:" +
                          String(c.bg),
                      )}
                      className={[
                        "relative flex items-center justify-center border-none " +
                          String(
                            c.tooOld ? "cursor-default" : "cursor-pointer",
                          ) +
                          " " +
                          String(
                            c.edge
                              ? "hover:bg-accent-hover"
                              : c.tooOld
                                ? ""
                                : "hover:bg-[#f4f0e9]",
                          ),
                        "h-[30px] text-[12px] tabular-nums",
                      ]
                        .filter(Boolean)
                        .join(" ")}
                    >
                      {c.day}
                      {c.isToday && !c.edge ? (
                        <>
                          <span className="absolute [bottom:3px] [left:50%] w-[3px] h-[3px] ml-[-1.5px] rounded-[50%] [background:var(--color-sand)]" />
                        </>
                      ) : null}
                    </button>
                  </Fragment>
                ))}
              </div>
              <div className="flex items-center gap-[8px] mt-[12px] pt-[11px] [border-top:1px_solid_#f1ece4]">
                <span className="text-text-muted truncate text-[11.5px] flex-1 min-w-0">
                  {anchor ? "종료일을 선택하세요" : `${periodDays(p)}일 선택됨`}
                </span>
                <button
                  type="button"
                  onClick={apply}
                  className="bg-accent hover:bg-accent-hover flex-none cursor-pointer border-none font-semibold whitespace-nowrap text-[12px] text-[var(--color-surface)] rounded-[8px] p-[7px_14px]"
                >
                  적용
                </button>
              </div>
            </div>
          </>
        ) : null}
      </span>
    </>
  );
}
