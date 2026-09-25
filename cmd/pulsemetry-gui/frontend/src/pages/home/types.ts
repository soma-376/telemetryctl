import type { AgentId } from "$lib/domain/agent.types";

export type BucketUnit = "hour" | "day" | "week" | "month";
export interface HeroBar {
  label: string;
  tooltipLabel?: string;
  values: number[];
  totalValue: number;
}
export interface HeroData {
  unit: BucketUnit;
  bucketSize: number;
  caption: string;
  avgLabel: string;
  avgValue: string;
  totalTokens: string;
  totalCost: string;
  totalTime: string;
  peakNote: string;
  legend: { name: string; color: string }[];
  bars: HeroBar[];
  grandTotal: number;
  costNote: string;
}
export interface VendorRow {
  id: string;
  agent: AgentId;
  name: string;
  plan: string;
  spend: string;
  tokens: string;
  share: string;
  topModel: string;
}
export interface ActivityRow {
  /// 목록 재조정용 안정 키. 날짜+시각+제목은 같은 분에 같은 제목이면 충돌한다.
  id: string;
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
