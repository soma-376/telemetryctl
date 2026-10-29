import type { ConnectionRow, ConnState, HealthItem, Pref } from "./types";

export type * from "./types";

// PROJ-63 전까지의 목 데이터 — 디자인(Overview v2 설정 모달) 수치 그대로.

export const HEALTH: HealthItem[] = [
  { name: "Agent", state: "Running" },
  { name: "Claude Code", state: "Receiving" },
  { name: "Codex", state: "Receiving" },
  { name: "Network", state: "Connected" },
  { name: "Cloud", state: "Connected" },
];

export const PREFS: Pref[] = [
  {
    key: "launch",
    icon: "⏻",
    name: "시작 프로그램",
    desc: "로그인 시 Pulsemetry 자동 실행",
    kind: "toggle",
  },
  {
    key: "retention",
    icon: "▤",
    name: "로컬 데이터 보관 기간",
    desc: "기간이 지난 기록은 자동 정리됩니다",
    kind: "select",
    value: "30일",
    dbPath: "~/.pulsemetry/pulsemetry.db",
    dbSize: "18.4 MB",
  },
  {
    key: "notify",
    icon: "◔",
    name: "알림",
    desc: "반복 실패·연결 끊김을 알려줍니다",
    kind: "toggle",
  },
];

export const PREF_DEFAULTS: Record<string, boolean> = {
  launch: true,
  notify: true,
};

export const CONNECTIONS: ConnectionRow[] = [
  { id: "claude", seen: "3분 전 활동", state: "on" },
  { id: "codex", seen: "18분 전 활동", state: "on" },
  { id: "gemini", seen: "6시간 전 활동", state: "idle" },
  { id: "cursor", seen: "연결된 적 없음", state: "off" },
];

// 상태 칩 색 — 12px 미만 텍스트 대비 확보를 위해 시맨틱 토큰보다 어두운 값.
export const CONN_STATUS: Record<
  ConnState,
  { label: string; fg: string; bg: string; action: boolean }
> = {
  on: {
    label: "연결됨",
    fg: "#2f7e55",
    bg: "var(--color-success-soft)",
    action: false,
  },
  idle: {
    label: "대기 중",
    fg: "#8b6b36",
    bg: "var(--color-sand-soft)",
    action: false,
  },
  off: {
    label: "연결 안됨",
    fg: "var(--color-inactive)",
    bg: "var(--color-inactive-soft)",
    action: true,
  },
};
