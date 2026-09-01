import type { AgentId } from "$lib/domain/agent.types";
export type SessionState = "running" | "done";
type StageName = "Exploring" | "Implementing" | "Debugging" | "Verifying" | "";
export interface Stage {
  name: StageName;
  dur: string;
  weight: number;
}
export type TurnKind = "explore" | "implement" | "debug" | "verify";
export interface ToolCall {
  time: string;
  tool: string;
  arg: string;
  dur: string;
  ok: boolean;
}
export interface Turn {
  time: string;
  kind: TurnKind;
  mins: number;
  prompt: string;
  actions: number;
  filesChanged: number;
  tokens: string;
  retries: number;
  calls: ToolCall[];
}
export interface FileChange {
  dir: string;
  name: string;
  add: string;
  del: string;
}
export interface ActivitySession {
  time: string;
  agentId: AgentId;
  state: SessionState;
  title: string;
  repo: string;
  path: string;
  dur: string;
  tokens: string;
  cost: string;
  range: string;
  kpi: string[];
  stages: Stage[];
  active: number;
  turns: Turn[];
  files: FileChange[];
}
export interface StateStyle {
  label: string;
  bg: string;
  fg: string;
}
export interface StageStyle {
  ko: string;
  bar: string;
  label: string;
}
export interface TurnStyle {
  name: string;
  bar: string;
  fg: string;
  bg: string;
  border: string;
}
export interface TurnSegment {
  grow: number;
  color: string;
  radius: string;
  tip: string;
}
export interface TurnDisplay {
  n: number;
  time: string;
  label: string;
  labelFg: string;
  labelBg: string;
  labelBorder: string;
  labelDot: string;
  preview: string;
  prompt: string;
  chars: string;
  meta: string;
  stats: { name: string; value: string; fg: string }[];
  callNote: string;
  calls: ToolCall[];
}
