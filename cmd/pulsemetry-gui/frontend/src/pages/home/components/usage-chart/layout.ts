import type { HeroBar } from "../../types";

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
    tickIndexes: chooseTickIndexes(source.length),
    xAt,
    yAt,
  };
}

function chooseTickIndexes(count: number): number[] {
  if (count <= 12) return Array.from({ length: count }, (_, index) => index);
  const indexes = new Set<number>([0, count - 1]);
  for (let i = 1; i < 5; i++) {
    indexes.add(Math.round((i * (count - 1)) / 5));
  }
  return [...indexes].sort((a, b) => a - b);
}
