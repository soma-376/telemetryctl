import type { PeriodRange } from "./period.types";

const MS_PER_DAY = 86_400_000;

function iso(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

export function toDate(s: string): Date {
  // "YYYY-MM-DD" 만 들어온다 (iso 가 만든 값이거나 RETAIN_FROM/TODAY).
  const p = s.split("-");
  return new Date(Number(p[0]), Number(p[1]) - 1, Number(p[2]));
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

function weekStart(d: Date): Date {
  const back = (d.getDay() + 6) % 7;
  return addDays(new Date(d.getFullYear(), d.getMonth(), d.getDate()), -back);
}

function monthStart(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), 1);
}

function monthEnd(d: Date): Date {
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
