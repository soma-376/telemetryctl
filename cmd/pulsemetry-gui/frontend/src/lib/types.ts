export type AgentId = "claude" | "codex" | "gemini" | "cursor" | "other";

export type MascotPose =
  | "view-front"
  | "normal"
  | "tired"
  | "watching"
  | "collecting-alt"
  | "found-alt"
  | "warning"
  | "no-data"
  | "offline"
  | "done"
  | "icon-plain";

export type SessionStatus = "active" | "complete" | "warning" | "error" | "idle";

export interface AgentUsage {
  id: AgentId;
  name: string;
  pct: number;
  tokens: number;
}

export interface Session {
  id: string;
  time: string;
  agentId: AgentId;
  title: string;
  durationMin: number;
  tokens: number;
  status: SessionStatus;
}

export interface Summary {
  activeTime: string;
  activeTimeDelta: number;
  tokens: number;
  tokensDelta: number;
  cost: number;
  costDelta: number;
  sessions: number;
  sessionsDelta: number;
}

export interface Insight {
  weeklyPattern: number[];
  patternLabel: string;
  patternBody: string;
  tiredMsg: string;
}

export interface MascotHeadline {
  pose: MascotPose;
  msg: string;
}

export interface Connection {
  online: boolean;
  activeAgents: number;
}

export interface PeriodRange {
  start: string;
  end: string;
}
