import type { BucketUnit, HeroBar } from "../../types";

export interface ChartMargin {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

export interface ChartSegment {
  value: number;
  seriesIndex: number;
  y: number;
  height: number;
}

export interface ChartBar extends HeroBar {
  index: number;
  x: number;
  segments: ChartSegment[];
}

export interface UsageChartLayout {
  width: number;
  height: number;
  margin: ChartMargin;
  plotHeight: number;
  slotWidth: number;
  barWidth: number;
  niceMax: number;
  bars: ChartBar[];
  tickIndexes: number[];
  xAt: (index: number) => number;
  yAt: (value: number) => number;
}

const HEIGHT = 190;
const MARGIN: ChartMargin = { top: 10, right: 8, bottom: 30, left: 42 };

export function createUsageChartLayout(
  source: HeroBar[],
  availableWidth: number,
  unit: BucketUnit,
  bucketSize: number,
): UsageChartLayout {
  const width = Math.max(availableWidth, 320);
  const plotWidth = width - MARGIN.left - MARGIN.right;
  const plotHeight = HEIGHT - MARGIN.top - MARGIN.bottom;
  const maxValue = Math.max(1, ...source.map((bar) => bar.totalValue));
  const niceMax = Math.max(10, Math.ceil(maxValue / 10) * 10);
  const slotWidth = plotWidth / Math.max(source.length, 1);
  const barWidth = Math.max(3, slotWidth * (source.length > 18 ? 0.68 : 0.58));
  const xAt = (index: number) => MARGIN.left + slotWidth * (index + 0.5);
  const yAt = (value: number) =>
    MARGIN.top + plotHeight * (1 - value / niceMax);

  const bars = source.map((bar, index): ChartBar => {
    let sum = 0;
    const segments = bar.values.map((value, seriesIndex) => {
      const start = sum;
      sum += value;
      return {
        value,
        seriesIndex,
        y: yAt(sum),
        height: yAt(start) - yAt(sum),
      };
    });
    return { ...bar, index, x: xAt(index) - barWidth / 2, segments };
  });

  return {
    width,
    height: HEIGHT,
    margin: MARGIN,
    plotHeight,
    slotWidth,
    barWidth,
    niceMax,
    bars,
    tickIndexes: chooseTickIndexes(unit, bucketSize, source.length),
    xAt,
    yAt,
  };
}

/**
 * X축 tick은 개수를 억지로 맞추지 않고 규칙적인 시간 간격을 사용한다.
 * 마지막 날짜를 강제로 추가하지 않아 2일/3일 간격이 섞이지 않게 한다.
 */
function chooseTickIndexes(
  unit: BucketUnit,
  bucketSize: number,
  count: number,
): number[] {
  let step = 1;

  if (unit === "hour") {
    // 2시간 버킷은 6시간마다, 6시간 버킷은 모두 표시한다.
    step = bucketSize === 2 ? 3 : 1;
  } else if (unit === "day") {
    if (count <= 7) step = 1;
    else if (count <= 12) step = 2;
    else if (count <= 14) step = 3;
    else step = 5;
  } else if (unit === "week") {
    step = niceStep(count, [1, 2, 3, 4], 6);
  } else {
    step = niceStep(count, [1, 2, 3, 4, 6], 6);
  }

  return Array.from(
    { length: Math.ceil(count / step) },
    (_, index) => index * step,
  ).filter((index) => index < count);
}

function niceStep(count: number, candidates: number[], maxTicks: number): number {
  return candidates.find((step) => Math.ceil(count / step) <= maxTicks)
    ?? candidates[candidates.length - 1];
}
