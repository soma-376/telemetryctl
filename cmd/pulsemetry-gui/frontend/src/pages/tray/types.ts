import type { AgentId } from "$lib/domain/agent.types";

export interface TrayLimitWindow {
  label: string;
  /** 남은 비율(%) */
  pct: number;
  remain: string;
  reset: string;
  /** 공급자가 대표로 꼽은 윈도우. 없으면 첫 항목이 대표가 된다 */
  head?: boolean;
}

export interface TrayVendor {
  id: AgentId;
  plan: string;
  spend: string;
  tokens: string;
  credential: string;
  windows: TrayLimitWindow[];
}

export interface TraySession {
  id: string;
  agentId: AgentId;
  title: string;
  sub: string;
  live: boolean;
}

export type TrayOptionKey = "notify" | "launch";

export interface TrayOption {
  key: TrayOptionKey;
  name: string;
  desc: string;
}
