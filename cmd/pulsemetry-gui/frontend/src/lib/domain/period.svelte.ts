import type { PeriodRange } from "./period.types";
export type { PeriodRange } from "./period.types";

const MS_PER_DAY = 86_400_000;

function iso(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

export function toDate(s: string): Date {
  const [y, m, d] = s.split("-").map(Number);
  return new Date(y, m - 1, d);
}

function today0(): Date {
  const n = new Date();
  return new Date(n.getFullYear(), n.getMonth(), n.getDate());
}

export function addDays(d: Date, n: number): Date {
  const r = new Date(d);
  r.setDate(r.getDate() + n);
  return r;
}

export function weekStart(d: Date): Date {
  const back = (d.getDay() + 6) % 7;
  return addDays(new Date(d.getFullYear(), d.getMonth(), d.getDate()), -back);
}

export function monthStart(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), 1);
}

export function monthEnd(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth() + 1, 0);
}

function diffDays(a: Date, b: Date): number {
  return Math.round((b.getTime() - a.getTime()) / MS_PER_DAY);
}

export function todayRange(): PeriodRange {
  const e = iso(today0());
  return { start: e, end: e };
}

export function weekRange(): PeriodRange {
  const w = weekStart(today0());
  return { start: iso(w), end: iso(addDays(w, 6)) };
}

export function monthRange(): PeriodRange {
  const t = today0();
  return { start: iso(monthStart(t)), end: iso(monthEnd(t)) };
}

export const period = $state<{ value: PeriodRange }>({ value: todayRange() });

export function periodDays(p: PeriodRange): number {
  return diffDays(toDate(p.start), toDate(p.end)) + 1;
}

export function periodBucket(p: PeriodRange): "today" | "7d" | "month" {
  const d = periodDays(p);
  return d <= 1 ? "today" : d <= 10 ? "7d" : "month";
}

export function periodLabel(p: PeriodRange): string {
  const eq = (r: PeriodRange) => r.start === p.start && r.end === p.end;
  if (eq(todayRange())) return "오늘";
  if (eq(weekRange())) return "이번 주";
  if (eq(monthRange())) return "이번 달";
  return "기간 선택";
}

export function periodDateText(p: PeriodRange): string {
  const label = periodLabel(p);
  if (label !== "기간 선택") return label;
  const fmt = (s: string) => {
    const d = toDate(s);
    return `${d.getMonth() + 1}.${d.getDate()}`;
  };
  return p.start === p.end ? fmt(p.start) : `${fmt(p.start)} ~ ${fmt(p.end)}`;
}

export function deltaNoun(p: PeriodRange): string {
  return periodDays(p) === 1 ? "어제" : "이전 기간";
}

export function isoDate(d: Date): string {
  return iso(d);
}

export function periodRangeText(p: PeriodRange): string {
  return p.start === p.end ? p.start : `${p.start} ~ ${p.end}`;
}

// monthGridDays 는 표시 월(displayMonth)의 1일이 속한 주의 월요일부터 42칸(6주)을 만든다.
export function monthGridDays(displayMonth: Date): Date[] {
  const first = weekStart(monthStart(displayMonth));
  return Array.from({ length: 42 }, (_, i) => addDays(first, i));
}
