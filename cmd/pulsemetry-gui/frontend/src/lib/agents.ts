import type { AgentId } from "./types";

export interface AgentStyle {
  name: string;
  glyph: string;
  fg: string;
  bg: string;
  weight: number;
  fontMd: number;
  fontSm: number;
}

export const AGENT_STYLE: Record<AgentId, AgentStyle> = {
  claude: { name: "Claude Code", glyph: "✳", fg: "var(--color-agent-claude)", bg: "var(--color-agent-claude-soft)", weight: 400, fontMd: 16, fontSm: 13 },
  codex: { name: "Codex", glyph: "</>", fg: "var(--color-agent-codex)", bg: "var(--color-agent-codex-soft)", weight: 700, fontMd: 11, fontSm: 10 },
  gemini: { name: "Gemini CLI", glyph: "◇", fg: "var(--color-agent-gemini)", bg: "var(--color-agent-gemini-soft)", weight: 400, fontMd: 14, fontSm: 11 },
  cursor: { name: "Cursor", glyph: "▣", fg: "var(--color-agent-cursor)", bg: "var(--color-agent-cursor-soft)", weight: 400, fontMd: 14, fontSm: 11 },
  other: { name: "Others", glyph: "•••", fg: "var(--color-inactive)", bg: "var(--color-inactive-soft)", weight: 400, fontMd: 14, fontSm: 11 },
};

export const AGENT_NAMES: Record<AgentId, string> = {
  claude: "Claude Code",
  codex: "Codex",
  gemini: "Gemini CLI",
  cursor: "Cursor",
  other: "Others",
};
