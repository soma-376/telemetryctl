import type {
  AgentUsage,
  Session,
  Summary,
  Insight,
  MascotHeadline,
  Connection,
  PeriodRange,
} from "../../lib/types";
import { periodBucket } from "../../lib/period.svelte";

export { AGENT_NAMES } from "../../lib/agents";

type Bucket = "today" | "7d" | "month";

const SUMMARY: Record<Bucket, Summary> = {
  today: { activeTime: "2h 41m", activeTimeDelta: 18, tokens: 148_000, tokensDelta: 12, cost: 3.82, costDelta: -6, sessions: 6, sessionsDelta: 0 },
  "7d": { activeTime: "18h 20m", activeTimeDelta: 9, tokens: 1_020_000, tokensDelta: 15, cost: 26.4, costDelta: 4, sessions: 41, sessionsDelta: 12 },
  month: { activeTime: "72h 05m", activeTimeDelta: 6, tokens: 4_180_000, tokensDelta: 22, cost: 108.9, costDelta: -3, sessions: 168, sessionsDelta: 8 },
};

const AGENTS: Record<Bucket, AgentUsage[]> = {
  today: [
    { id: "claude", name: "Claude Code", pct: 52, tokens: 102_000 },
    { id: "codex", name: "Codex", pct: 28, tokens: 55_000 },
    { id: "gemini", name: "Gemini CLI", pct: 12, tokens: 24_000 },
    { id: "other", name: "Others", pct: 8, tokens: 15_000 },
  ],
  "7d": [
    { id: "claude", name: "Claude Code", pct: 48, tokens: 490_000 },
    { id: "codex", name: "Codex", pct: 31, tokens: 316_000 },
    { id: "gemini", name: "Gemini CLI", pct: 14, tokens: 143_000 },
    { id: "other", name: "Others", pct: 7, tokens: 71_000 },
  ],
  month: [
    { id: "claude", name: "Claude Code", pct: 50, tokens: 2_090_000 },
    { id: "codex", name: "Codex", pct: 30, tokens: 1_254_000 },
    { id: "gemini", name: "Gemini CLI", pct: 13, tokens: 543_000 },
    { id: "other", name: "Others", pct: 7, tokens: 293_000 },
  ],
};

const SESSIONS: Record<Bucket, Session[]> = {
  today: [
    { id: "t1", time: "14:32", agentId: "claude", title: "Authentication refactor", durationMin: 42, tokens: 31_000, status: "active" },
    { id: "t2", time: "11:20", agentId: "claude", title: "Wails IPC bridge", durationMin: 51, tokens: 44_000, status: "complete" },
    { id: "t3", time: "12:24", agentId: "gemini", title: "Dashboard UI", durationMin: 38, tokens: 21_000, status: "complete" },
    { id: "t4", time: "13:08", agentId: "codex", title: "Integration tests", durationMin: 23, tokens: 18_000, status: "complete" },
  ],
  "7d": [
    { id: "w1", time: "화 15:10", agentId: "claude", title: "Payment gateway 통합", durationMin: 184, tokens: 210_000, status: "complete" },
    { id: "w2", time: "수 10:02", agentId: "codex", title: "E2E 테스트 스위트", durationMin: 132, tokens: 150_000, status: "complete" },
    { id: "w3", time: "목 16:40", agentId: "gemini", title: "검색 인덱싱 리팩터", durationMin: 96, tokens: 98_000, status: "complete" },
    { id: "w4", time: "금 09:30", agentId: "codex", title: "CI 파이프라인", durationMin: 74, tokens: 76_000, status: "complete" },
    { id: "w5", time: "금 14:32", agentId: "claude", title: "Authentication refactor", durationMin: 42, tokens: 44_000, status: "active" },
  ],
  month: [
    { id: "m1", time: "8/4", agentId: "claude", title: "마이그레이션: NestJS 전환", durationMin: 940, tokens: 620_000, status: "complete" },
    { id: "m2", time: "8/2", agentId: "codex", title: "결제 시스템 v2", durationMin: 810, tokens: 540_000, status: "complete" },
    { id: "m3", time: "8/6", agentId: "gemini", title: "온보딩 플로우", durationMin: 620, tokens: 410_000, status: "complete" },
    { id: "m4", time: "8/7", agentId: "claude", title: "관측성 파이프라인", durationMin: 560, tokens: 380_000, status: "warning" },
    { id: "m5", time: "8/5", agentId: "codex", title: "리포트 엔진", durationMin: 430, tokens: 300_000, status: "complete" },
  ],
};

const INSIGHTS: Record<Bucket, Insight> = {
  today: {
    weeklyPattern: [22, 34, 28, 52, 70, 100, 64, 40],
    patternLabel: "시간대 패턴",
    patternBody: "오후 1~5시에\n집중도가 높아요.",
    tiredMsg: "비슷한 요청을\n5번 반복했어.\n시간을 많이 썼네.",
  },
  "7d": {
    weeklyPattern: [40, 55, 48, 72, 88, 100, 76, 60],
    patternLabel: "요일 패턴",
    patternBody: "화·수에\n생산성이 높았어요.",
    tiredMsg: "테스트 재실행에\n시간을 꽤 썼어.",
  },
  month: {
    weeklyPattern: [50, 62, 70, 84, 92, 100, 80, 66],
    patternLabel: "주차 패턴",
    patternBody: "월초에\n집중도가 높았어요.",
    tiredMsg: "반복 작업이\n전체의 30%야.",
  },
};

const HEADLINES: Record<Bucket, MascotHeadline> = {
  today: { pose: "normal", msg: "오늘도\n열심히 했네." },
  "7d": { pose: "normal", msg: "이번 주도\n잘 달렸어." },
  month: { pose: "normal", msg: "이번 달\n페이스 좋아." },
};

export const connection: Connection = { online: true, activeAgents: 3 };
export const liveTokensToday = SUMMARY.today.tokens;

export const summaryFor = (p: PeriodRange): Summary => SUMMARY[periodBucket(p)];
export const agentsFor = (p: PeriodRange): AgentUsage[] => AGENTS[periodBucket(p)];
export const insightsFor = (p: PeriodRange): Insight => INSIGHTS[periodBucket(p)];
export const headlineFor = (p: PeriodRange): MascotHeadline => HEADLINES[periodBucket(p)];

export function topSessions(p: PeriodRange, n = 4): Session[] {
  return [...SESSIONS[periodBucket(p)]].sort((a, b) => b.tokens - a.tokens).slice(0, n);
}
