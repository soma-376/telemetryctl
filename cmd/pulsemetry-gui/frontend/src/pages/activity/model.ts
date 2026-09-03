import type { ActivitySession, FileChange, SessionState, StageStyle, StateStyle, ToolCall, TurnDisplay, TurnKind, TurnSegment, TurnStyle } from "./types";
export type * from "./types";
import { AGENT_NAMES } from "$lib/domain/agent";
import { formatDuration } from "$lib/utils/format";

// 파일 변경 목록은 이 개수까지만 접어서 보여준다.
export const FILE_CAP = 4;

// 에이전트 id → 표시 이름 (AGENT_NAMES 재사용)
const AGENT_LABELS = AGENT_NAMES;

// oo: 세션 상태 스타일 맵 — 진행 중은 모래빛+dot, 종료는 중립 회색+dot 없음
const STATE_STYLE: Record<SessionState, StateStyle> = {
  running: { label: "진행 중", bg: "var(--color-sand-soft)", fg: "#8b6b36" },
  done: { label: "종료", bg: "var(--color-inactive-soft)", fg: "var(--color-text-secondary)" },
};

// so: 스테이지 스타일 맵 (디버깅 bar 색은 리터럴 #FF9A5C)
const STAGE_STYLE: Record<string, StageStyle> = {
  Exploring: { ko: "탐색 중", bar: "var(--color-border)", label: "var(--color-text-secondary)" },
  Implementing: { ko: "구현 중", bar: "var(--color-info)", label: "var(--color-info)" },
  Debugging: { ko: "디버깅 중", bar: "#FF9A5C", label: "var(--color-text-secondary)" },
  Verifying: { ko: "검증 중", bar: "var(--color-success)", label: "var(--color-success)" },
};

// xl: 스테이지 이름 → 한국어 라벨
const stageKo = (name: string): string => STAGE_STYLE[name]?.ko ?? "";

// 턴 라벨 스타일 맵 (Activity v2 드로어 전용 팔레트 — 디버깅 계열은 리터럴)
const TURN_STYLE: Record<TurnKind, TurnStyle> = {
  explore: { name: "탐색", bar: "var(--color-inactive)", fg: "#5e5a54", bg: "#f1efeb", border: "#ddd8d0" },
  implement: { name: "구현", bar: "var(--color-sand)", fg: "var(--color-accent)", bg: "var(--color-sand-soft)", border: "#e6d5b8" },
  debug: { name: "디버깅", bar: "#e08a3c", fg: "#9a5a14", bg: "#fbeee0", border: "#f0d2ae" },
  verify: { name: "검증", bar: "var(--color-success)", fg: "#2f7e55", bg: "var(--color-success-soft)", border: "#c9e7d6" },
};

const TURN_KINDS = Object.keys(TURN_STYLE) as TurnKind[];

// ml: 세션 행 표시 데이터 계산
export function rowDisplay(e: ActivitySession, selected: boolean) {
  const n = STATE_STYLE[e.state];
  const running = e.state === "running";
  const stage = running ? e.stages[e.active] : undefined;
  return {
    dot: running ? "var(--color-accent)" : "var(--color-border-strong)",
    running,
    rail: running ? "inset 3px 0 0 var(--color-accent)" : "none",
    bg: selected
      ? running
        ? "var(--color-accent-soft)"
        : "var(--color-inactive-soft)"
      : running
        ? "#faf6ef" /* 모래빛 틴트 — 토큰 없음(sand-soft보다 옅음) */
        : "transparent",
    time: e.time,
    agentId: e.agentId,
    title: e.title,
    agentName: AGENT_LABELS[e.agentId],
    stageText: stage ? " · " + stageKo(stage.name) : "",
    repo: e.repo,
    path: e.path,
    dur: e.dur,
    durColor: running ? "var(--color-accent-hover)" : "var(--color-text)",
    durWeight: running ? 600 : 400,
    tokens: e.tokens,
    cost: e.cost,
    badge: { label: n.label, bg: n.bg, fg: n.fg, dot: running },
  };
}

// bl: 세션 상세(드로어) 표시 데이터 계산
//
// 세션을 인자로 받는다. 예전에는 mock 의 SESSIONS 를 직접 인덱싱했는데, 그러면 이 함수가
// 데이터 출처를 알아야 하고 index 의 의미가 그 배열에 묶인다. position 도 같은 이유로
// 호출부가 만든다 — 전체 개수를 아는 것은 목록이지 이 함수가 아니다.
export function detailDisplay(t: ActivitySession, position: string) {
  const n = STATE_STYLE[t.state];

  const turns = t.turns;
  const totalMins = turns.reduce((sum, u) => sum + u.mins, 0) || 1;
  const minsByKind: Partial<Record<TurnKind, number>> = {};
  turns.forEach((u) => {
    minsByKind[u.kind] = (minsByKind[u.kind] ?? 0) + u.mins;
  });
  // 가장 오래 머문 턴 분류가 세션 성격이 된다.
  const topKind = TURN_KINDS.reduce((a, b) =>
    (minsByKind[b] ?? 0) > (minsByKind[a] ?? 0) ? b : a,
  );
  const cl = TURN_STYLE[topKind];

  return {
    title: t.title,
    repo: t.repo,
    path: t.path,
    agentName: AGENT_LABELS[t.agentId],
    agentId: t.agentId,
    badge: { label: n.label, bg: n.bg, fg: n.fg },
    character: { label: cl.name + "형 세션", fg: cl.fg, bg: cl.bg, border: cl.border, dot: cl.bar },
    range: t.range,
    kpi: t.kpi,
    turnCount:
      turns.length + "턴 · " + formatDuration(totalMins),
    legend: TURN_KINDS.map((k) => ({
      name: TURN_STYLE[k].name,
      color: TURN_STYLE[k].bar,
      pct: Math.round(((minsByKind[k] ?? 0) / totalMins) * 100) + "%",
    })),
    segments: turns.map(
      (u, i): TurnSegment => ({
        grow: u.mins,
        color: TURN_STYLE[u.kind].bar,
        radius: i === 0 ? "4px 0 0 4px" : i === turns.length - 1 ? "0 4px 4px 0" : "0",
        tip: i + 1 + "턴 · " + TURN_STYLE[u.kind].name + " · " + u.mins + "분",
      }),
    ),
    turns: turns.map((u, i): TurnDisplay => {
      const s = TURN_STYLE[u.kind];
      return {
        n: i + 1,
        time: u.time,
        label: s.name,
        labelFg: s.fg,
        labelBg: s.bg,
        labelBorder: s.border,
        labelDot: s.bar,
        preview: u.prompt.split("\n")[0] ?? "",
        prompt: u.prompt,
        chars: u.prompt.length + "자",
        meta: u.actions + " Action · " + u.tokens,
        stats: [
          { name: "Agent Action", value: String(u.actions), fg: "var(--color-text)" },
          { name: "변경 파일", value: String(u.filesChanged), fg: "var(--color-text)" },
          { name: "토큰", value: u.tokens, fg: "var(--color-text)" },
          { name: "재시도", value: String(u.retries), fg: u.retries ? "#9a6a14" : "var(--color-text)" },
        ],
        callNote: u.calls.length + "회 · 실패 " + u.calls.filter((c) => !c.ok).length,
        calls: u.calls,
      };
    }),
    files: t.files,
    position,
  };
}
