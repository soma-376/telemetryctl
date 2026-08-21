import type { AgentId } from "../../lib/types";
import { AGENT_STYLE, AGENT_NAMES } from "../../lib/agents";

// 세션 상태(진행/완료/중단/실패)와 스테이지(탐색/구현/디버깅/검증) 정의.
export type SessionState = "running" | "done" | "stopped" | "failed";
export type StageName =
  | "Exploring"
  | "Implementing"
  | "Debugging"
  | "Verifying"
  | "";

export interface Stage {
  name: StageName;
  dur: string;
  weight: number;
}

export interface ActivityEvent {
  time: string;
  text: string;
  dot: string;
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
  summary: string;
  kpi: string[];
  stages: Stage[];
  active: number;
  events: ActivityEvent[];
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

// 에이전트 id → 표시 이름 (AGENT_NAMES 재사용)
export const AGENT_LABELS = AGENT_NAMES;

// oo: 세션 상태 스타일 맵
export const STATE_STYLE: Record<SessionState, StateStyle> = {
  running: { label: "진행 중", bg: "var(--color-accent-soft)", fg: "var(--color-accent-hover)" },
  done: { label: "완료", bg: "var(--color-success-soft)", fg: "var(--color-success)" },
  stopped: { label: "중단됨", bg: "var(--color-info-soft)", fg: "var(--color-info)" },
  failed: { label: "실패", bg: "var(--color-danger-soft)", fg: "var(--color-danger)" },
};

// so: 스테이지 스타일 맵 (디버깅 bar 색은 리터럴 #FF9A5C)
export const STAGE_STYLE: Record<string, StageStyle> = {
  Exploring: { ko: "탐색 중", bar: "var(--color-border)", label: "var(--color-text-secondary)" },
  Implementing: { ko: "구현 중", bar: "var(--color-info)", label: "var(--color-info)" },
  Debugging: { ko: "디버깅 중", bar: "#FF9A5C", label: "var(--color-text-secondary)" },
  Verifying: { ko: "검증 중", bar: "var(--color-success)", label: "var(--color-success)" },
};

// xl: 스테이지 이름 → 한국어 라벨
export const stageKo = (name: string): string => STAGE_STYLE[name]?.ko ?? "";

// an: Activity 헤더 데이터
export const HEADER = { online: true, activeAgents: 3, tokensToday: "148k" };

// Yr: 세션 목록 (번들에서 그대로 복사)
export const SESSIONS: ActivitySession[] = [
  {
    time: "14:32",
    agentId: "claude",
    state: "running",
    title: "OTLP authentication proxy",
    repo: "telemetryctl",
    path: "cmd/pulsemetry-gui",
    dur: "42m",
    tokens: "31k",
    cost: "$0.82",
    range: "14:32 – 진행 중 (42분 경과)",
    summary:
      "OTLP 요청에 대한 Bearer 토큰 검증을 구현하고, 검증된 요청을 Collector로 전달하는 프록시 서버를 개발하고 있습니다.",
    kpi: ["42m", "31k", "18", "3", "$0.82", "84k", "12k", "$1.24"],
    stages: [
      { name: "Exploring", dur: "6m", weight: 6 },
      { name: "Implementing", dur: "19m", weight: 19 },
      { name: "Debugging", dur: "14m", weight: 14 },
      { name: "Verifying", dur: "3m", weight: 3 },
      { name: "", dur: "", weight: 2 },
    ],
    active: 2,
    events: [
      { time: "14:32", text: "세션 시작", dot: "var(--color-text-muted)" },
      { time: "14:35", text: "auth.middleware.ts 읽기", dot: "var(--color-text-muted)" },
      { time: "14:41", text: "auth.middleware.ts 수정", dot: "var(--color-info)" },
      { time: "14:48", text: "테스트 실행 (2 실패)", dot: "var(--color-danger)" },
      { time: "14:51", text: "token.service.ts 수정", dot: "var(--color-info)" },
      { time: "15:02", text: "테스트 실행 (2 실패)", dot: "var(--color-danger)" },
    ],
    files: [
      { dir: "internal/auth/", name: "auth.middleware.ts", add: "+58", del: "-12" },
      { dir: "internal/auth/", name: "token.service.ts", add: "+37", del: "-8" },
      { dir: "cmd/pulsemetry-gui/", name: "otlp.proxy.ts", add: "+112", del: "-24" },
      { dir: "internal/schema/", name: "validation.ts", add: "+23", del: "-3" },
    ],
  },
  {
    time: "14:05",
    agentId: "codex",
    state: "running",
    title: "Metrics ingestion pipeline",
    repo: "telemetryctl",
    path: "internal/ingest",
    dur: "1h 09m",
    tokens: "38k",
    cost: "$0.71",
    range: "14:05 – 진행 중 (69분 경과)",
    summary:
      "수집된 메트릭을 배치 단위로 정규화해 저장소에 적재하는 파이프라인을 구현하고 있습니다.",
    kpi: ["1h 09m", "38k", "22", "1", "$0.71", "96k", "14k", "$1.52"],
    stages: [
      { name: "Exploring", dur: "9m", weight: 9 },
      { name: "Implementing", dur: "60m", weight: 60 },
      { name: "", dur: "", weight: 8 },
    ],
    active: 1,
    events: [
      { time: "14:05", text: "세션 시작", dot: "var(--color-text-muted)" },
      { time: "14:12", text: "ingest/pipeline.go 읽기", dot: "var(--color-text-muted)" },
      { time: "14:24", text: "normalizer.go 생성", dot: "var(--color-info)" },
      { time: "14:47", text: "batch_writer.go 생성", dot: "var(--color-info)" },
      { time: "15:03", text: "pipeline.go 수정", dot: "var(--color-info)" },
    ],
    files: [
      { dir: "internal/ingest/", name: "normalizer.go", add: "+142", del: "0" },
      { dir: "internal/ingest/", name: "batch_writer.go", add: "+88", del: "0" },
      { dir: "internal/ingest/", name: "pipeline.go", add: "+34", del: "-16" },
    ],
  },
  {
    time: "13:08",
    agentId: "codex",
    state: "done",
    title: "Integration tests",
    repo: "telemetryctl",
    path: "internal/collector",
    dur: "23m",
    tokens: "18k",
    cost: "$0.41",
    range: "13:08 – 13:31 (23분)",
    summary:
      "Collector 파이프라인의 통합 테스트를 추가하고, 실패하던 배치 플러시 케이스를 수정했습니다.",
    kpi: ["23m", "18k", "11", "1", "$0.41", "42k", "6k", "$0.58"],
    stages: [
      { name: "Exploring", dur: "3m", weight: 3 },
      { name: "Implementing", dur: "12m", weight: 12 },
      { name: "Debugging", dur: "4m", weight: 4 },
      { name: "Verifying", dur: "4m", weight: 4 },
    ],
    active: 3,
    events: [
      { time: "13:08", text: "세션 시작", dot: "var(--color-text-muted)" },
      { time: "13:11", text: "collector_test.go 읽기", dot: "var(--color-text-muted)" },
      { time: "13:16", text: "batch_test.go 생성", dot: "var(--color-info)" },
      { time: "13:24", text: "테스트 실행 (전체 통과)", dot: "var(--color-success)" },
      { time: "13:31", text: "세션 종료", dot: "var(--color-text-muted)" },
    ],
    files: [
      { dir: "internal/collector/", name: "batch_test.go", add: "+96", del: "0" },
      { dir: "internal/collector/", name: "batch.go", add: "+14", del: "-6" },
      { dir: "internal/collector/", name: "flush.go", add: "+8", del: "-2" },
    ],
  },
  {
    time: "12:24",
    agentId: "gemini",
    state: "done",
    title: "Dashboard UI polish",
    repo: "pulsemetry-web",
    path: "src/routes/overview",
    dur: "38m",
    tokens: "21k",
    cost: "$0.36",
    range: "12:24 – 13:02 (38분)",
    summary: "Overview 화면의 카드 간격과 타이포 스케일을 디자인 토큰에 맞춰 정리했습니다.",
    kpi: ["38m", "21k", "24", "0", "$0.36", "58k", "9k", "$0.72"],
    stages: [
      { name: "Exploring", dur: "8m", weight: 8 },
      { name: "Implementing", dur: "24m", weight: 24 },
      { name: "Verifying", dur: "6m", weight: 6 },
    ],
    active: 2,
    events: [
      { time: "12:24", text: "세션 시작", dot: "var(--color-text-muted)" },
      { time: "12:29", text: "MetricCard.svelte 읽기", dot: "var(--color-text-muted)" },
      { time: "12:38", text: "MetricCard.svelte 수정", dot: "var(--color-info)" },
      { time: "12:51", text: "tokens.css 수정", dot: "var(--color-info)" },
      { time: "13:02", text: "세션 종료", dot: "var(--color-text-muted)" },
    ],
    files: [
      { dir: "src/lib/components/", name: "MetricCard.svelte", add: "+41", del: "-33" },
      { dir: "src/routes/overview/", name: "+page.svelte", add: "+28", del: "-19" },
      { dir: "src/lib/styles/", name: "tokens.css", add: "+12", del: "-4" },
    ],
  },
  {
    time: "11:20",
    agentId: "claude",
    state: "done",
    title: "Wails IPC bridge",
    repo: "telemetryctl",
    path: "cmd/pulsemetry-gui/bridge",
    dur: "51m",
    tokens: "44k",
    cost: "$1.18",
    range: "11:20 – 12:11 (51분)",
    summary:
      "Go 백엔드와 프론트엔드 사이의 IPC 브리지를 구현하고, 이벤트 스트리밍 채널을 연결했습니다.",
    kpi: ["51m", "44k", "31", "4", "$1.18", "112k", "18k", "$1.94"],
    stages: [
      { name: "Exploring", dur: "9m", weight: 9 },
      { name: "Implementing", dur: "26m", weight: 26 },
      { name: "Debugging", dur: "11m", weight: 11 },
      { name: "Verifying", dur: "5m", weight: 5 },
    ],
    active: 3,
    events: [
      { time: "11:20", text: "세션 시작", dot: "var(--color-text-muted)" },
      { time: "11:26", text: "app.go 읽기", dot: "var(--color-text-muted)" },
      { time: "11:34", text: "bridge.go 생성", dot: "var(--color-info)" },
      { time: "11:52", text: "빌드 실패 (타입 불일치)", dot: "var(--color-danger)" },
      { time: "12:03", text: "bridge.go 수정", dot: "var(--color-info)" },
      { time: "12:11", text: "빌드 성공", dot: "var(--color-success)" },
    ],
    files: [
      { dir: "cmd/pulsemetry-gui/bridge/", name: "bridge.go", add: "+184", del: "-12" },
      { dir: "cmd/pulsemetry-gui/", name: "app.go", add: "+46", del: "-8" },
      { dir: "internal/events/", name: "stream.go", add: "+31", del: "-5" },
    ],
  },
  {
    time: "10:02",
    agentId: "codex",
    state: "done",
    title: "Refactor token middleware",
    repo: "telemetryctl",
    path: "internal/auth",
    dur: "37m",
    tokens: "26k",
    cost: "$0.61",
    range: "10:02 – 10:39 (37분)",
    summary:
      "토큰 검증 미들웨어를 인터페이스 기반으로 분리해 테스트 가능하도록 리팩터링했습니다.",
    kpi: ["37m", "26k", "19", "2", "$0.61", "66k", "11k", "$0.88"],
    stages: [
      { name: "Exploring", dur: "5m", weight: 5 },
      { name: "Implementing", dur: "21m", weight: 21 },
      { name: "Debugging", dur: "6m", weight: 6 },
      { name: "Verifying", dur: "5m", weight: 5 },
    ],
    active: 3,
    events: [
      { time: "10:02", text: "세션 시작", dot: "var(--color-text-muted)" },
      { time: "10:08", text: "middleware.go 읽기", dot: "var(--color-text-muted)" },
      { time: "10:17", text: "verifier.go 생성", dot: "var(--color-info)" },
      { time: "10:31", text: "테스트 실행 (전체 통과)", dot: "var(--color-success)" },
      { time: "10:39", text: "세션 종료", dot: "var(--color-text-muted)" },
    ],
    files: [
      { dir: "internal/auth/", name: "middleware.go", add: "+52", del: "-88" },
      { dir: "internal/auth/", name: "verifier.go", add: "+74", del: "0" },
      { dir: "internal/auth/", name: "verifier_test.go", add: "+63", del: "0" },
    ],
  },
  {
    time: "09:18",
    agentId: "cursor",
    state: "stopped",
    title: "Fix linter issues",
    repo: "pulsemetry-web",
    path: "src/lib",
    dur: "16m",
    tokens: "8k",
    cost: "$0.14",
    range: "09:18 – 09:34 (16분, 중단)",
    summary: "ESLint 경고를 정리하던 중 사용자가 세션을 중단했습니다.",
    kpi: ["16m", "8k", "7", "0", "$0.14", "18k", "3k", "$0.21"],
    stages: [
      { name: "Exploring", dur: "4m", weight: 4 },
      { name: "Implementing", dur: "12m", weight: 12 },
    ],
    active: 1,
    events: [
      { time: "09:18", text: "세션 시작", dot: "var(--color-text-muted)" },
      { time: "09:22", text: "eslint 실행 (24 경고)", dot: "var(--color-text-muted)" },
      { time: "09:29", text: "utils.ts 수정", dot: "var(--color-info)" },
      { time: "09:34", text: "사용자가 중단", dot: "var(--color-inactive)" },
    ],
    files: [
      { dir: "src/lib/", name: "utils.ts", add: "+9", del: "-14" },
      { dir: "src/lib/", name: "format.ts", add: "+4", del: "-7" },
    ],
  },
  {
    time: "08:41",
    agentId: "gemini",
    state: "done",
    title: "Env configuration",
    repo: "telemetryctl",
    path: "deploy/local",
    dur: "19m",
    tokens: "9k",
    cost: "$0.16",
    range: "08:41 – 09:00 (19분)",
    summary: "로컬 개발 환경의 환경변수 템플릿과 docker-compose 설정을 정리했습니다.",
    kpi: ["19m", "9k", "9", "0", "$0.16", "22k", "4k", "$0.27"],
    stages: [
      { name: "Exploring", dur: "6m", weight: 6 },
      { name: "Implementing", dur: "10m", weight: 10 },
      { name: "Verifying", dur: "3m", weight: 3 },
    ],
    active: 2,
    events: [
      { time: "08:41", text: "세션 시작", dot: "var(--color-text-muted)" },
      { time: "08:46", text: ".env.example 읽기", dot: "var(--color-text-muted)" },
      { time: "08:52", text: "docker-compose.yml 수정", dot: "var(--color-info)" },
      { time: "09:00", text: "세션 종료", dot: "var(--color-text-muted)" },
    ],
    files: [
      { dir: "deploy/local/", name: "docker-compose.yml", add: "+18", del: "-6" },
      { dir: "deploy/local/", name: ".env.example", add: "+11", del: "-2" },
    ],
  },
];

// ml: 세션 행 표시 데이터 계산
export function rowDisplay(e: ActivitySession, selected: boolean) {
  const r = AGENT_STYLE[e.agentId];
  const n = STATE_STYLE[e.state];
  const running = e.state === "running";
  const stage = running ? e.stages[e.active] : undefined;
  return {
    dot: running ? "var(--color-accent)" : "var(--color-border-strong)",
    dotAnim: running ? "livePulse 2s ease-out infinite" : "none",
    rail: running ? "inset 3px 0 0 var(--color-accent)" : "none",
    bg: selected
      ? running
        ? "var(--color-accent-soft)"
        : "var(--color-inactive-soft)"
      : running
        ? "var(--color-sand-soft)"
        : "transparent",
    time: e.time,
    agentTile: { bg: r.bg, fg: r.fg, glyph: r.glyph, size: r.fontSm, weight: r.weight },
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
    badge: { label: n.label, bg: n.bg, fg: n.fg, dot: running ? "6px" : "0px" },
  };
}

// bl: 세션 상세(드로어) 표시 데이터 계산
export function detailDisplay(index: number) {
  const t = SESSIONS[index];
  const r = AGENT_STYLE[t.agentId];
  const n = STATE_STYLE[t.state];
  return {
    title: t.title,
    repo: t.repo,
    path: t.path,
    agentName: AGENT_LABELS[t.agentId],
    agentTile: { bg: r.bg, fg: r.fg, glyph: r.glyph, size: r.fontMd, weight: r.weight },
    badge: { label: n.label, bg: n.bg, fg: n.fg },
    range: t.range,
    summary: t.summary,
    kpi: t.kpi,
    stages: t.stages.map((stage, o) => {
      const activeStage = t.state === "running" && o === t.active;
      const s = STAGE_STYLE[stage.name];
      return {
        name: stage.name || "•••",
        dur: stage.dur,
        weight: stage.weight,
        color: activeStage ? "var(--color-accent)" : (s?.bar ?? "var(--color-border)"),
        labelColor: activeStage
          ? "var(--color-accent)"
          : (s?.label ?? "var(--color-text-muted)"),
        weightFont: activeStage ? 600 : 400,
      };
    }),
    events: t.events,
    files: t.files,
    position: `${index + 1} / ${SESSIONS.length}`,
  };
}
