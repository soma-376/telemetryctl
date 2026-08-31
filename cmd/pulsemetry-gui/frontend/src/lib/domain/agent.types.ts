export type AgentId = "claude" | "codex" | "gemini" | "cursor" | "other";

export interface AgentStyle {
  name: string;
  glyph: string;
  fg: string;
  bg: string;
  weight: number;
  fontMd: number;
  fontSm: number;
}