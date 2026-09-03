import type { NonEmpty } from "$lib/utils/array";

import type { AgentId } from "$lib/domain/agent.types";

export interface TrayLimitWindow {
  label: string;
  /** 남은 비율(%) */
  pct: number;
  remain: string;
  reset: string;
  /** 대표 윈도우. 주간 → 5시간 → 첫 항목 순으로 선택한다 */
  head?: boolean;
}

export interface TrayVendor {
  id: AgentId;
  plan: string;
  spend: string;
  tokens: string;
  credential: string;
  /**
   * 비어 있지 않다. adapter 의 toVendor 가 창이 없는 벤더를 카드로 만들지 않으므로
   * 이 배열이 빈 채로 화면에 닿는 경로가 없다 — headOf 가 그 사실에 기댄다.
   */
  windows: NonEmpty<TrayLimitWindow>;
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
