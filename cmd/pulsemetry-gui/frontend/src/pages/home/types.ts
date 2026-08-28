import type { AgentId } from "$lib/domain/agent.types";
export type SeriesKey = "claude" | "codex" | "gemini";
export type UsageParts = [number, number, number];
export type BucketUnit = "hour" | "day" | "week" | "month";
export interface BucketRung {
  maxDays: number;
  unit: BucketUnit;
  size: number;
}
export interface ChartBucket {
  label: string;
  key: string;
  parts: UsageParts;
  elapsed: boolean;
}
export interface BucketSet {
  unit: BucketUnit;
  size: number;
  items: ChartBucket[];
}
export interface HeroBar {
  label: string;
  values: number[];
  totalValue: number;
  total: string;
  valueSize: string;
  valueFg: string;
  labelFg: string;
  labelWeight: number;
  fillPct: number;
  parts: { color: string; weight: number; radius: string }[];
}
export interface HeroData {
  unit: BucketUnit;
  bucketSize: number;
  caption: string;
  avgLabel: string;
  avgValue: string;
  totalTokens: number;
  totalCost: string;
  totalTime: string;
  peakNote: string;
  gridCols: string;
  gridGap: string;
  legend: { name: string; color: string }[];
  bars: HeroBar[];
  perVendor: number[];
  grandTotal: number;
  cents: number[];
}
export interface VendorRow {
  id: SeriesKey;
  plan: string;
  spend: string;
  tokens: string;
  share: string;
  topModel: string;
}
export interface ActivityRow {
  date: string;
  time: string;
  agent: AgentId;
  title: string;
  sub: string;
  tokens: string;
  state: "running" | "done";
}
export interface ActivityData {
  rows: ActivityRow[];
  total: number;
  running: number;
}
