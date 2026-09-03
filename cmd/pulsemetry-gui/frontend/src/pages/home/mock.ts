import { AGENT_STYLE } from "$lib/domain/agent";
import type { AgentId } from "$lib/domain/agent.types";
import { HOURLY_DAYS, RETAIN_DAYS, TODAY } from "$lib/domain/retention";
import { formatDuration } from "$lib/utils/format";
import { at } from "$lib/utils/array";
import type {
  ActivityData,
  ActivityRow,
  BucketRung,
  BucketSet,
  ChartBucket,
  HeroBar,
  HeroData,
  SeriesKey,
  UsageParts,
  VendorRow,
} from "./types";
// 홈 히어로 차트 — 기간에 따라 버킷 밀도를 바꾸는 스택 바 (디자인 Overview v3).
// 실데이터 연동 전까지는 날짜 시드 해시로 안정적인 합성값을 만든다:
// 같은 날짜는 항상 같은 사용량을 돌려주므로 화면 간 수치가 서로 모순되지 않는다.

const SERIES = ["claude", "codex", "gemini"] as const;

// ── ISO 날짜 유틸 ────────────────────────────────────────────────────────────
const pad = (n: number) => (n < 10 ? `0${n}` : `${n}`);
const toIso = (d: Date) =>
  `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const parseIso = (s: string) => {
  const p = s.split("-");
  return new Date(Number(p[0]), Number(p[1]) - 1, Number(p[2]));
};
const addDays = (s: string, n: number) => {
  const d = parseIso(s);
  d.setDate(d.getDate() + n);
  return toIso(d);
};
const dayCount = (a: string, b: string) =>
  Math.round((parseIso(b).getTime() - parseIso(a).getTime()) / 86_400_000) + 1;
const mdLabel = (s: string) => {
  const d = parseIso(s);
  return `${d.getMonth() + 1}.${d.getDate()}`;
};
/** 월요일 시작 요일 인덱스 */
const dowIndex = (s: string) => (parseIso(s).getDay() + 6) % 7;

const NOW_HOUR = new Date().getHours();

/** 달력 선택 하한 — 보존 정책 밖의 날짜는 데이터가 없다 */

// 보존 하한은 공용 정책(lib/domain/retention)이 소유한다.

// ── 합성 사용량 (날짜 시드 해시 — 결정적) ───────────────────────────────────
function hash(s: string): number {
  let x = 2166136261;
  for (let i = 0; i < s.length; i++) {
    x ^= s.charCodeAt(i);
    x = Math.imul(x, 16777619);
  }
  x ^= x >>> 15;
  x = Math.imul(x, 2246822507);
  x ^= x >>> 13;
  x = Math.imul(x, 3266489909);
  x ^= x >>> 16;
  return (x >>> 0) / 4294967295;
}

type Parts = UsageParts;

function splitUsage(seed: string, total: number): Parts {
  if (total <= 0) return [0, 0, 0];
  const c = Math.round(total * (0.42 + hash(seed + "c") * 0.22));
  const x = Math.round((total - c) * (0.5 + hash(seed + "x") * 0.3));
  return [c, x, Math.max(0, total - c - x)];
}

function dayUsage(isoDate: string): Parts {
  if (isoDate > TODAY) return [0, 0, 0];
  const weekend = dowIndex(isoDate) >= 5;
  const base = weekend ? 6 : 22;
  return splitUsage(isoDate, Math.round(base * (0.5 + hash(isoDate) * 1.0)));
}

function hourUsage(isoDate: string, hour: number): Parts {
  if (isoDate > TODAY || (isoDate === TODAY && hour > NOW_HOUR))
    return [0, 0, 0];
  const peakish = hour >= 12 && hour <= 18 ? 1.6 : 0.55;
  const seed = `${isoDate}h${hour}`;
  return splitUsage(seed, Math.round(4 * peakish * (0.3 + hash(seed) * 1.3)));
}

function sumDays(from: string, span: number): Parts {
  const acc: Parts = [0, 0, 0];
  for (let j = 0; j < span; j++) {
    const u = dayUsage(addDays(from, j));
    acc[0] += u[0];
    acc[1] += u[1];
    acc[2] += u[2];
  }
  return acc;
}

// ── 집계 버킷 정책 ────────────────────────────────────────────────────────────
// 1일은 2시간, 2일은 6시간, 3~31일은 일, 32~120일은 7일,
// 121~400일은 달력 월 단위로 집계한다. X축 tick 간격은 렌더 계층에서 별도로 정한다.
const LADDER: BucketRung[] = [
  { maxDays: 1, unit: "hour", size: 2 },
  { maxDays: 2, unit: "hour", size: 6 },
  { maxDays: 31, unit: "day", size: 1 },
  { maxDays: 120, unit: "week", size: 7 },
  { maxDays: 400, unit: "month", size: 0 },
];

function buildBuckets(start: string, end: string): BucketSet {
  const n = Math.min(dayCount(start, end), RETAIN_DAYS);
  let rung = LADDER.find((r) => n <= r.maxDays) ?? at(LADDER, LADDER.length - 1);

  // 보존 강등 — 시간 단위 롤업 지평(HOURLY_DAYS)보다 오래된 구간은 시간 버킷
  // 데이터가 없으므로 일 단위로 내려간다.
  if (rung.unit === "hour" && start < addDays(TODAY, -(HOURLY_DAYS - 1))) {
    rung = { maxDays: rung.maxDays, unit: "day", size: 1 };
  }

  const out: ChartBucket[] = [];

  if (rung.unit === "hour") {
    const step = rung.size;
    for (let i = 0; i < n; i++) {
      const d = addDays(start, i);
      for (let hh = 0; hh < 24; hh += step) {
        const acc: Parts = [0, 0, 0];
        for (let k = 0; k < step; k++) {
          const u = hourUsage(d, hh + k);
          acc[0] += u[0];
          acc[1] += u[1];
          acc[2] += u[2];
        }
        out.push({
          // 날짜 경계의 00시는 시각만 쓰면 여러 날에서 같은 라벨이 반복된다.
          // 자정에는 날짜를 표시하고, 그 외에는 시각을 표시해 경계를 드러낸다.
          label: hh === 0 ? mdLabel(d) : `${pad(hh)}시`,
          key: `${d} ${pad(hh)}`,
          parts: acc,
          elapsed: d < TODAY || (d === TODAY && hh <= NOW_HOUR),
        });
      }
    }
    return { unit: "hour", size: step, items: out };
  }

  if (rung.unit === "month") {
    let cur = parseIso(start);
    cur = new Date(cur.getFullYear(), cur.getMonth(), 1);
    const last = parseIso(addDays(start, n - 1));
    while (cur <= last) {
      const from = toIso(cur);
      const monthEnd = new Date(cur.getFullYear(), cur.getMonth() + 1, 0);
      const span = monthEnd.getDate() - cur.getDate() + 1;
      out.push({
        label: `${cur.getMonth() + 1}월`,
        key: from,
        parts: sumDays(from, span),
        elapsed: from <= TODAY,
      });
      cur = new Date(cur.getFullYear(), cur.getMonth() + 1, 1);
    }
    return { unit: "month", size: 1, items: out };
  }

  const step = rung.size;
  for (let i = 0; i < n; i += step) {
    const from = addDays(start, i);
    out.push({
      label: mdLabel(from),
      key: from,
      parts: sumDays(from, Math.min(step, n - i)),
      elapsed: from <= TODAY,
    });
  }
  return { unit: rung.unit, size: step, items: out };
}

/** 캡션과 평균 라벨은 선택된 단계를 따른다 */
function unitText(unit: BucketRung["unit"], size: number) {
  if (unit === "hour")
    return { caption: `${size}시간 단위 · 벤더 구성`, avg: `${size}시간 평균` };
  if (unit === "day")
    return {
      caption: `${size > 1 ? `${size}일` : "일별"} 단위 · 벤더 구성`,
      avg: `${size > 1 ? `${size}일` : "일"} 평균`,
    };
  if (unit === "week")
    return { caption: "주 단위 · 벤더 구성", avg: "주 평균" };
  return { caption: "월 단위 · 벤더 구성", avg: "월 평균" };
}

const VENDOR_META: Record<
  SeriesKey,
  { plan: string; topModel: string; rate: number }
> = {
  claude: { plan: "Max 20x", topModel: "Sonnet 4.5", rate: 0.0274 },
  codex: { plan: "Pro", topModel: "GPT-5.3-Codex", rate: 0.0287 },
  gemini: { plan: "Ultra", topModel: "Gemini 3.1 Pro", rate: 0.015 },
};

const MIN_PER_K = 3.73;
// ── 히어로 집계 ──────────────────────────────────────────────────────────────

/**
 * 라벨을 몇 개 걸러 보여줄지. 막대 개수가 아니라 실제로 들어갈 폭으로 정한다 —
 * 개수로 어림하면 긴 라벨("12월 31일")이 겹치거나 자리가 남는데도 지워진다.
 * 컬럼 폭은 렌더 시점에만 알 수 있어 컴포넌트가 재서 넘긴다.
 */
function labelStep(
  labels: string[],
  colWidth: number,
  fontSize = 11,
): number {
  if (colWidth <= 0) return 1;
  const widest = labels.reduce(
    (m, s) => Math.max(m, textWidth(s, fontSize)),
    0,
  );
  if (widest === 0) return 1;
  return Math.max(1, Math.ceil((widest + 6) / colWidth));
}

// 한글·CJK 는 글자당 약 1em, 그 외는 약 0.6em 으로 근사한다.
function textWidth(s: string, fontSize: number): number {
  let w = 0;
  for (const ch of s) {
    w += /[　-鿿가-힯]/.test(ch) ? fontSize : fontSize * 0.6;
  }
  return w;
}

export function heroData(start: string, end: string): HeroData {
  const bk = buildBuckets(start, end);
  const items = bk.items;
  const text = unitText(bk.unit, bk.size);

  const maxBucket =
    items.reduce(
      (m, b) => Math.max(m, b.parts[0] + b.parts[1] + b.parts[2]),
      0,
    ) || 1;
  const perVendor = SERIES.map((_, i) =>
    items.reduce((s, b) => s + at(b.parts, i), 0),
  );
  const grandTotal = perVendor.reduce((s, v) => s + v, 0);
  const cents = SERIES.map((k, i) =>
    Math.round(at(perVendor, i) * VENDOR_META[k].rate * 100),
  );
  const cost = cents.reduce((s, c) => s + c, 0) / 100;
  const minutes = Math.round(grandTotal * MIN_PER_K);
  const peakIdx = items.reduce(
    (bi, b, i) =>
      b.parts[0] + b.parts[1] + b.parts[2] >
      at(items, bi).parts[0] + at(items, bi).parts[1] + at(items, bi).parts[2]
        ? i
        : bi,
    0,
  );

  // 바가 많으면 라벨을 솎아낸다 — 마지막 라벨은 항상 남긴다.
  // 값 라벨은 의도적으로 선별한다 — 모든 막대에 숫자를 얹으면 읽히지 않는다.
  const dense = items.length > 14;
  // 한 달 안에서는 월 접두가 소음이다.
  const sameMonth = parseIso(start).getMonth() === parseIso(end).getMonth();
  const shortLabel = bk.unit === "day" && sameMonth && items.length > 11;

  return {
    unit: bk.unit,
    bucketSize: bk.size,
    caption: text.caption,
    avgLabel: text.avg,
    avgValue: `${Math.round(grandTotal / (items.filter((b) => b.elapsed).length || 1))}k`,
    totalTokens: grandTotal,
    totalCost: `$${cost.toFixed(2)}`,
    totalTime: formatDuration(minutes),
    peakNote: `최다 ${at(items, peakIdx).label || at(items, peakIdx).key} · ${
      at(items, peakIdx).parts[0] +
      at(items, peakIdx).parts[1] +
      at(items, peakIdx).parts[2]
    }k`,
    gridCols: `repeat(${items.length},minmax(0,1fr))`,
    gridGap: `${items.length > 18 ? 4 : items.length > 11 ? 6 : 10}px`,
    legend: SERIES.map((k) => ({
      name: AGENT_STYLE[k].name,
      color: AGENT_STYLE[k].fg,
    })),
    perVendor,
    grandTotal,
    cents,
    bars: items.map((b, bi) => {
      const total = b.parts[0] + b.parts[1] + b.parts[2];
      const peak = bi === peakIdx;
      // 높이는 픽셀이 아니라 비율로 넘긴다. 렌더는 flex-grow 로 배분하므로
      // 플롯이 커지면 같이 커지고, 조각 합이 막대 높이를 넘을 수 없다.
      const parts = SERIES.map((k, i) => ({ k, v: at(b.parts, i) }))
        .filter((p) => p.v > 0)
        .map((p, i, arr) => ({
          color: AGENT_STYLE[p.k].fg,
          weight: p.v,
          radius:
            i === 0
              ? "4px 4px 0 0"
              : i === arr.length - 1
                ? "0 0 3px 3px"
                : "0",
        }));
      return {
        // 라벨 솎아내기는 컬럼 폭을 아는 렌더 시점에 정한다(labelStep).
        label: shortLabel ? `${parseIso(b.key).getDate()}일` : b.label,
        values: [...b.parts],
        totalValue: total,
        fillPct: total ? (total / maxBucket) * 100 : 0,
        total: !total || (dense && !peak) ? "" : `${total}k`,
        valueSize: dense ? "10px" : "11px",
        valueFg: peak ? "var(--color-text)" : "var(--color-text-muted)",
        labelFg: peak ? "var(--color-text)" : "var(--color-text-muted)",
        labelWeight: peak ? 600 : 400,
        parts,
      };
    }),
  };
}

// ── 벤더 표 ──────────────────────────────────────────────────────────────────

export function vendorRows(hero: HeroData): VendorRow[] {
  return SERIES.map((k, i) => {
    const tok = at(hero.perVendor, i);
    const share = hero.grandTotal
      ? Math.round((tok / hero.grandTotal) * 100)
      : 0;
    return {
      id: k,
      plan: VENDOR_META[k].plan,
      spend: `$${(at(hero.cents, i) / 100).toFixed(2)}`,
      tokens: `${tok}k`,
      share: `${share}%`,
      topModel: `${VENDOR_META[k].topModel} · ${Math.round(tok * 0.67)}k`,
    };
  });
}

// ── 활동 목록 (차트와 같은 시드 → 수치가 서로 모순되지 않는다) ───────────────

const TASKS: [string, AgentId][] = [
  ["OTLP authentication proxy", "claude"],
  ["Metrics ingestion pipeline", "codex"],
  ["Dashboard UI polish", "gemini"],
  ["Wails IPC bridge", "claude"],
  ["Refactor token middleware", "codex"],
  ["Integration tests", "claude"],
  ["Env configuration", "gemini"],
  ["Collector batch tuning", "codex"],
  ["Session drawer states", "gemini"],
  ["Quota window parser", "claude"],
];
const STAGES = ["디버깅 중", "구현 중", "테스트 중"];

function sessionsOn(isoDate: string): ActivityRow[] {
  if (isoDate > TODAY) return [];
  const weekend = dowIndex(isoDate) >= 5;
  const n = weekend
    ? Math.round(hash(isoDate + "n") * 1.4)
    : 1 + Math.round(hash(isoDate + "n") * 2.4);
  const out: ActivityRow[] = [];
  for (let i = 0; i < n; i++) {
    const seed = `${isoDate}s${i}`;
    const task = at(TASKS, Math.floor(hash(seed + "t") * TASKS.length));
    const hh = 9 + Math.floor(hash(seed + "h") * 9);
    const mm = Math.floor(hash(seed + "m") * 60);
    const mins = 12 + Math.floor(hash(seed + "d") * 60);
    const running = isoDate === TODAY && i < 2;
    const dur = formatDuration(mins);
    const stage = running
      ? at(STAGES, Math.floor(hash(seed + "g") * STAGES.length))
      : "";
    out.push({
      id: seed,
      date: isoDate,
      time: `${pad(hh)}:${pad(mm)}`,
      agent: task[1],
      title: task[0],
      sub: `${AGENT_STYLE[task[1]].name} · ${dur}${stage ? ` · ${stage}` : ""}`,
      tokens: `${6 + Math.round(hash(seed + "k") * 38)}k`,
      state: running ? "running" : "done",
    });
  }
  return out.sort((a, b) => (a.time < b.time ? 1 : -1));
}

export function buildActivity(start: string, end: string): ActivityData {
  const rows: ActivityRow[] = [];
  let total = 0;
  let running = 0;
  let d = end > TODAY ? TODAY : end;
  while (d >= start) {
    const day = sessionsOn(d);
    total += day.length;
    for (const s of day) if (s.state === "running") running++;
    if (rows.length < 7) rows.push(...day.slice(0, 7 - rows.length));
    d = addDays(d, -1);
  }
  return { rows, total, running };
}
