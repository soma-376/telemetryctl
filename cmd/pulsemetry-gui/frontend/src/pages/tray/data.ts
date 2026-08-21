import type { AgentId } from "../../lib/types";

// 트레이 퀵뷰 목데이터 — 디자인(Tray Quick View) 수치 그대로.
// 벤더별 한도는 pages/home/data.ts 의 vendorUsage 를 그대로 쓴다.

export interface TraySession {
  id: string;
  agentId: AgentId;
  title: string;
  sub: string;
  live: boolean;
}

export const TRAY_SESSIONS: TraySession[] = [
  { id: "t1", agentId: "claude", title: "OTLP authentication proxy", sub: "Claude Code · 42m · 디버깅 중", live: true },
  { id: "t2", agentId: "codex", title: "Metrics ingestion pipeline", sub: "Codex · 1h 09m · 구현 중", live: true },
  { id: "t3", agentId: "claude", title: "Wails IPC bridge", sub: "Claude Code · 51m · 완료", live: false },
  { id: "t4", agentId: "gemini", title: "Dashboard UI polish", sub: "Gemini CLI · 38m · 완료", live: false },
];

export const TRAY_SYNCED_TEXT = "40초 전";
