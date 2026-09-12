import type { AgentId } from "$lib/domain/agent.types";
export type SessionState = "running" | "done";
type StageName = "Exploring" | "Implementing" | "Debugging" | "Verifying" | "";
export interface Stage {
  name: StageName;
  dur: string;
  weight: number;
}
export type TurnKind = "explore" | "implement" | "debug" | "verify" | "unknown";
export interface ToolCall {
	 id?: number;
  time: string;
  tool: string;
  arg: string;
  dur: string;
  ok: boolean | null;
}
export interface Turn {
	 id?: number;
	 promptTruncated?: boolean;
  time: string;
  kind: TurnKind;
  mins: number | null;
  prompt: string;
  actions: number;
  filesChanged: number;
  tokens: string;
  retries: number | null;
  calls: ToolCall[];
}
export interface FileChange {
  dir: string;
  name: string;
  add: string;
  del: string;
}
export interface ActivitySession {
	workType?: TurnKind;
	notice?: string;
  /// 목록 재조정용 안정 키. 실데이터에서는 store 의 세션 id 가 들어온다.
  id: string;
  time: string;
  /// 날짜 구분용 로컬 날짜 키(YYYY-MM-DD). 시작 시각이 없으면 빈 문자열이다.
  day?: string;
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
