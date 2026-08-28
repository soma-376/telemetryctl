import type { TrayOption, TraySession, TrayVendor } from "./types";

export type {
  TrayLimitWindow,
  TrayOption,
  TrayOptionKey,
  TraySession,
  TrayVendor,
} from "./types";

// 트레이 퀵뷰 목데이터 — 디자인(Tray Quick View) 수치 그대로.
//
// 벤더마다 한도 윈도우가 N개다. 접힌 카드는 공급자가 대표로 꼽은(head) 윈도우 —
// 실제로 막히는 그 한도 — 만 보여주고, 클릭하면 나머지가 펼쳐진다.
// 새 윈도우 종류가 생겨도 템플릿 변경이 필요 없다.

export const TRAY_VENDORS: TrayVendor[] = [
  {
    id: "claude",
    plan: "Max 20x",
    spend: "$2.14",
    tokens: "102k tokens",
    credential: "OAuth · claude.ai 계정",
    windows: [
      { label: "5시간 한도", pct: 10, remain: "10%", reset: "오늘 20:18" },
      {
        label: "주간 한도",
        pct: 64,
        remain: "64%",
        reset: "8월 23일",
        head: true,
      },
      { label: "Opus 주간", pct: 38, remain: "38%", reset: "8월 23일" },
    ],
  },
  {
    id: "codex",
    plan: "Pro",
    spend: "$1.32",
    tokens: "55k tokens",
    credential: "API key · sk-…f2a9",
    windows: [
      { label: "5시간 한도", pct: 90, remain: "90%", reset: "오늘 20:06" },
      {
        label: "주간 한도",
        pct: 55,
        remain: "55%",
        reset: "8월 23일",
        head: true,
      },
    ],
  },
  {
    id: "gemini",
    plan: "Ultra",
    spend: "$0.36",
    tokens: "24k tokens",
    credential: "OAuth · Google Cloud",
    windows: [
      {
        label: "일일 한도",
        pct: 98,
        remain: "98%",
        reset: "내일 09:00",
        head: true,
      },
    ],
  },
];

export const TRAY_SESSIONS: TraySession[] = [
  {
    id: "t1",
    agentId: "claude",
    title: "OTLP authentication proxy",
    sub: "Claude Code · 42m · 디버깅 중",
    live: true,
  },
  {
    id: "t2",
    agentId: "codex",
    title: "Metrics ingestion pipeline",
    sub: "Codex · 1h 09m · 구현 중",
    live: true,
  },
];

export const TRAY_OPTIONS: TrayOption[] = [
  { key: "notify", name: "한도 알림", desc: "20% 아래로 떨어지면 알림" },
  { key: "launch", name: "로그인 시 자동 실행", desc: "" },
];

export const TRAY_SYNCED_TEXT = "40초 전";
