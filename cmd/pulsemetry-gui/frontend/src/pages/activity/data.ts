import type { AgentId } from "../../lib/types";
import { AGENT_NAMES } from "../../lib/agents";
import { formatDuration } from "../../lib/format";

// 세션 상태(진행 중/종료)와 스테이지(탐색/구현/디버깅/검증) 정의.
// 종료는 성공/실패 판단 없이 "끝났다"는 사실만 담는다 — 성공으로 읽히는 초록을 쓰지 않고,
// 초록은 작업 유형(검증) 배지가 쓰므로 상태 배지와 색이 겹치지 않게 한다.
export type SessionState = "running" | "done";
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

// 드로어 턴 분류. 스테이지와 별개로 턴 단위 라벨링에 쓴다.
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

// 파일 변경 목록은 이 개수까지만 접어서 보여준다.
export const FILE_CAP = 4;

// 에이전트 id → 표시 이름 (AGENT_NAMES 재사용)
export const AGENT_LABELS = AGENT_NAMES;

// oo: 세션 상태 스타일 맵 — 진행 중은 모래빛+dot, 종료는 중립 회색+dot 없음
export const STATE_STYLE: Record<SessionState, StateStyle> = {
  running: { label: "진행 중", bg: "var(--color-sand-soft)", fg: "#8b6b36" },
  done: { label: "종료", bg: "var(--color-inactive-soft)", fg: "var(--color-text-secondary)" },
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

export interface TurnStyle {
  name: string;
  bar: string;
  fg: string;
  bg: string;
  border: string;
}

// 턴 라벨 스타일 맵 (Activity v2 드로어 전용 팔레트 — 디버깅 계열은 리터럴)
export const TURN_STYLE: Record<TurnKind, TurnStyle> = {
  explore: { name: "탐색", bar: "var(--color-inactive)", fg: "#5e5a54", bg: "#f1efeb", border: "#ddd8d0" },
  implement: { name: "구현", bar: "var(--color-sand)", fg: "var(--color-accent)", bg: "var(--color-sand-soft)", border: "#e6d5b8" },
  debug: { name: "디버깅", bar: "#e08a3c", fg: "#9a5a14", bg: "#fbeee0", border: "#f0d2ae" },
  verify: { name: "검증", bar: "var(--color-success)", fg: "#2f7e55", bg: "var(--color-success-soft)", border: "#c9e7d6" },
};

const TURN_KINDS = Object.keys(TURN_STYLE) as TurnKind[];

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
    kpi: ["42m", "31k", "18", "3", "$0.82", "84k", "12k", "$1.24"],
    stages: [
      { name: "Exploring", dur: "6m", weight: 6 },
      { name: "Implementing", dur: "19m", weight: 19 },
      { name: "Debugging", dur: "14m", weight: 14 },
      { name: "Verifying", dur: "3m", weight: 3 },
      { name: "", dur: "", weight: 2 },
    ],
    active: 2,
    turns: [
      {
        time: "09:41",
        kind: "explore",
        mins: 7,
        prompt: "OTLP 프록시에서 Bearer 토큰을 검증하는 부분이 어디야? 관련 파일 구조부터 알려줘.",
        actions: 8,
        filesChanged: 0,
        tokens: "2.1k",
        retries: 0,
        calls: [
          { time: "09:41", tool: "Glob", arg: "internal/auth/**/*.go", dur: "0.3s", ok: true },
          { time: "09:42", tool: "Grep", arg: '"Bearer"', dur: "0.2s", ok: true },
          { time: "09:43", tool: "Read", arg: "internal/auth/middleware.go", dur: "0.4s", ok: true },
        ],
      },
      {
        time: "09:48",
        kind: "explore",
        mins: 8,
        prompt: "middleware.go 전체를 보여주고, 토큰이 만료됐을 때 어떤 경로를 타는지 설명해줘.",
        actions: 5,
        filesChanged: 0,
        tokens: "3.4k",
        retries: 0,
        calls: [
          { time: "09:48", tool: "Read", arg: "internal/auth/middleware.go", dur: "0.3s", ok: true },
          { time: "09:50", tool: "Read", arg: "internal/auth/token.go", dur: "0.3s", ok: true },
        ],
      },
      {
        time: "09:56",
        kind: "implement",
        mins: 16,
        prompt:
          "토큰이 만료되면 401을 반환하도록 고쳐줘.\n에러 응답 본문에 만료 시각(exp)도 포함해줘. 기존 에러 포맷은 유지하고.",
        actions: 14,
        filesChanged: 2,
        tokens: "5.2k",
        retries: 0,
        calls: [
          { time: "09:58", tool: "Edit", arg: "internal/auth/middleware.go", dur: "1.2s", ok: true },
          { time: "10:04", tool: "Edit", arg: "internal/auth/token.go", dur: "0.9s", ok: true },
          { time: "10:09", tool: "Bash", arg: "go build ./...", dur: "6.1s", ok: true },
        ],
      },
      {
        time: "10:12",
        kind: "verify",
        mins: 7,
        prompt: "테스트 돌려봐.",
        actions: 6,
        filesChanged: 0,
        tokens: "1.8k",
        retries: 0,
        calls: [
          { time: "10:12", tool: "Bash", arg: "go test ./internal/auth/...", dur: "14.2s", ok: false },
          { time: "10:16", tool: "Read", arg: "internal/auth/middleware_test.go", dur: "0.3s", ok: true },
        ],
      },
      {
        time: "10:19",
        kind: "debug",
        mins: 15,
        prompt: "테스트 2개가 실패하는데, expired-token 케이스에서 401이 아니라 500이 뜬대. 원인 찾아줘.",
        actions: 19,
        filesChanged: 2,
        tokens: "6.1k",
        retries: 1,
        calls: [
          { time: "10:21", tool: "Grep", arg: '"StatusInternalServerError"', dur: "0.2s", ok: true },
          { time: "10:24", tool: "Edit", arg: "internal/auth/middleware.go", dur: "1.1s", ok: true },
          { time: "10:29", tool: "Bash", arg: "go test ./internal/auth/...", dur: "13.8s", ok: false },
        ],
      },
      {
        time: "10:34",
        kind: "debug",
        mins: 10,
        prompt:
          "여전히 같은 에러야.\n미들웨어보다 에러 핸들러가 먼저 잡는 것 같은데? 스택 순서를 확인해줘.",
        actions: 21,
        filesChanged: 3,
        tokens: "7.3k",
        retries: 2,
        calls: [
          { time: "10:34", tool: "Read", arg: "internal/server/error_handler.go", dur: "0.4s", ok: true },
          { time: "10:37", tool: "Edit", arg: "internal/server/error_handler.go", dur: "1.1s", ok: true },
          { time: "10:38", tool: "Bash", arg: "go test ./internal/auth/...", dur: "12.4s", ok: false },
          { time: "10:41", tool: "Bash", arg: "go test -run Expired -v", dur: "8.9s", ok: false },
        ],
      },
      {
        time: "10:44",
        kind: "debug",
        mins: 7,
        prompt: "핸들러 등록 순서를 로그로 찍어서 보여줘.",
        actions: 11,
        filesChanged: 1,
        tokens: "3.2k",
        retries: 0,
        calls: [
          { time: "10:44", tool: "Edit", arg: "internal/server/router.go", dur: "0.8s", ok: true },
          { time: "10:46", tool: "Bash", arg: "go run ./cmd/pulsemetry -v", dur: "9.4s", ok: true },
        ],
      },
      {
        time: "10:51",
        kind: "implement",
        mins: 13,
        prompt:
          "그럼 error_handler.go에서 AuthError를 먼저 분기하도록 바꿔줘. 다른 에러 타입 처리는 건드리지 마.",
        actions: 16,
        filesChanged: 2,
        tokens: "4.4k",
        retries: 0,
        calls: [
          { time: "10:51", tool: "Edit", arg: "internal/server/error_handler.go", dur: "1.3s", ok: true },
          { time: "11:01", tool: "Bash", arg: "go build ./...", dur: "5.8s", ok: true },
        ],
      },
      {
        time: "11:04",
        kind: "verify",
        mins: 5,
        prompt: "다시 테스트.",
        actions: 7,
        filesChanged: 0,
        tokens: "1.9k",
        retries: 0,
        calls: [
          { time: "11:04", tool: "Bash", arg: "go test ./internal/auth/...", dur: "13.1s", ok: true },
        ],
      },
      {
        time: "11:09",
        kind: "verify",
        mins: 5,
        prompt: "커버리지도 확인해줘.",
        actions: 5,
        filesChanged: 0,
        tokens: "1.4k",
        retries: 0,
        calls: [
          { time: "11:09", tool: "Bash", arg: "go test -cover ./internal/...", dur: "16.7s", ok: true },
        ],
      },
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
    kpi: ["1h 09m", "38k", "22", "1", "$0.71", "96k", "14k", "$1.52"],
    stages: [
      { name: "Exploring", dur: "9m", weight: 9 },
      { name: "Implementing", dur: "60m", weight: 60 },
      { name: "", dur: "", weight: 8 },
    ],
    active: 1,
    turns: [
      {
        time: "14:05",
        kind: "explore",
        mins: 11,
        prompt: "OTLP 수신 파이프라인에서 배치 처리하는 부분 찾아줘.",
        actions: 9,
        filesChanged: 0,
        tokens: "3.1k",
        retries: 0,
        calls: [
          { time: "14:06", tool: "Glob", arg: "internal/ingest/**", dur: "0.3s", ok: true },
          { time: "14:08", tool: "Read", arg: "internal/ingest/batcher.go", dur: "0.4s", ok: true },
        ],
      },
      {
        time: "14:24",
        kind: "implement",
        mins: 33,
        prompt: "배치 크기를 설정으로 빼고, 큐가 가득 차면 백프레셔를 걸도록 구현해줘.",
        actions: 26,
        filesChanged: 2,
        tokens: "18.4k",
        retries: 1,
        calls: [
          { time: "14:26", tool: "Edit", arg: "internal/ingest/batcher.go", dur: "1.4s", ok: true },
          { time: "14:47", tool: "Write", arg: "internal/ingest/backpressure.go", dur: "1.9s", ok: true },
          { time: "15:02", tool: "Bash", arg: "go test -bench Ingest", dur: "22.1s", ok: true },
        ],
      },
      {
        time: "15:04",
        kind: "implement",
        mins: 25,
        prompt: "설정 기본값을 config.go에도 반영해줘.",
        actions: 12,
        filesChanged: 1,
        tokens: "6.2k",
        retries: 0,
        calls: [
          { time: "15:06", tool: "Edit", arg: "internal/ingest/config.go", dur: "0.7s", ok: true },
          { time: "15:14", tool: "Bash", arg: "go build ./...", dur: "5.9s", ok: true },
        ],
      },
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
    kpi: ["23m", "18k", "11", "1", "$0.41", "42k", "6k", "$0.58"],
    stages: [
      { name: "Exploring", dur: "3m", weight: 3 },
      { name: "Implementing", dur: "12m", weight: 12 },
      { name: "Debugging", dur: "4m", weight: 4 },
      { name: "Verifying", dur: "4m", weight: 4 },
    ],
    active: 3,
    turns: [
      {
        time: "13:08",
        kind: "explore",
        mins: 4,
        prompt: "Collector 엔드투엔드 테스트가 지금 어디까지 커버돼?",
        actions: 5,
        filesChanged: 0,
        tokens: "2.2k",
        retries: 0,
        calls: [
          { time: "13:09", tool: "Read", arg: "internal/collector/collector_test.go", dur: "0.3s", ok: true },
        ],
      },
      {
        time: "13:19",
        kind: "implement",
        mins: 15,
        prompt: "OTLP 수신부터 저장까지 통합 테스트를 추가해줘. 실패 케이스도 포함해서.",
        actions: 14,
        filesChanged: 1,
        tokens: "9.8k",
        retries: 0,
        calls: [
          { time: "13:20", tool: "Edit", arg: "internal/collector/collector_test.go", dur: "1.6s", ok: true },
          { time: "13:31", tool: "Bash", arg: "go test ./internal/collector/...", dur: "18.3s", ok: true },
        ],
      },
      {
        time: "13:27",
        kind: "verify",
        mins: 4,
        prompt: "CI에도 연결해줘.",
        actions: 6,
        filesChanged: 1,
        tokens: "6.0k",
        retries: 0,
        calls: [
          { time: "13:28", tool: "Edit", arg: ".github/workflows/ci.yml", dur: "0.5s", ok: true },
        ],
      },
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
    kpi: ["38m", "21k", "24", "0", "$0.36", "58k", "9k", "$0.72"],
    stages: [
      { name: "Exploring", dur: "8m", weight: 8 },
      { name: "Implementing", dur: "24m", weight: 24 },
      { name: "Verifying", dur: "6m", weight: 6 },
    ],
    active: 2,
    turns: [
      {
        time: "12:24",
        kind: "explore",
        mins: 8,
        prompt: "Overview 화면의 카드 간격이 제각각인데, 어디서 정해지는지 찾아줘.",
        actions: 10,
        filesChanged: 0,
        tokens: "4.1k",
        retries: 0,
        calls: [
          { time: "12:26", tool: "Grep", arg: '"MetricCard"', dur: "0.2s", ok: true },
          { time: "12:29", tool: "Read", arg: "src/lib/components/MetricCard.svelte", dur: "0.3s", ok: true },
        ],
      },
      {
        time: "12:38",
        kind: "implement",
        mins: 24,
        prompt: "디자인 토큰에 맞춰 간격과 타이포 스케일을 정리해줘. 색은 건드리지 말고.",
        actions: 22,
        filesChanged: 3,
        tokens: "13.2k",
        retries: 0,
        calls: [
          { time: "12:40", tool: "Edit", arg: "src/lib/components/MetricCard.svelte", dur: "1.2s", ok: true },
          { time: "12:51", tool: "Edit", arg: "src/lib/styles/tokens.css", dur: "0.6s", ok: true },
        ],
      },
      {
        time: "12:56",
        kind: "verify",
        mins: 6,
        prompt: "빌드하고 스냅샷 테스트 돌려줘.",
        actions: 8,
        filesChanged: 0,
        tokens: "3.7k",
        retries: 0,
        calls: [
          { time: "12:57", tool: "Bash", arg: "npm run build", dur: "24.6s", ok: true },
          { time: "13:01", tool: "Bash", arg: "npm test -- --u", dur: "19.2s", ok: true },
        ],
      },
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
    kpi: ["51m", "44k", "31", "4", "$1.18", "112k", "18k", "$1.94"],
    stages: [
      { name: "Exploring", dur: "9m", weight: 9 },
      { name: "Implementing", dur: "26m", weight: 26 },
      { name: "Debugging", dur: "11m", weight: 11 },
      { name: "Verifying", dur: "5m", weight: 5 },
    ],
    active: 3,
    turns: [
      {
        time: "11:20",
        kind: "explore",
        mins: 9,
        prompt: "Go 백엔드랑 Svelte 프론트 사이 IPC가 지금 어떻게 연결돼 있어?",
        actions: 11,
        filesChanged: 0,
        tokens: "5.2k",
        retries: 0,
        calls: [
          { time: "11:22", tool: "Read", arg: "cmd/pulsemetry-gui/app.go", dur: "0.4s", ok: true },
        ],
      },
      {
        time: "11:41",
        kind: "implement",
        mins: 28,
        prompt: "Wails 바인딩을 bridge.go로 분리하고, 프론트용 타입을 자동 생성하도록 해줘.",
        actions: 24,
        filesChanged: 3,
        tokens: "21.4k",
        retries: 0,
        calls: [
          { time: "11:43", tool: "Write", arg: "cmd/pulsemetry-gui/bridge/bridge.go", dur: "2.1s", ok: true },
          { time: "11:56", tool: "Edit", arg: "cmd/pulsemetry-gui/app.go", dur: "0.9s", ok: true },
        ],
      },
      {
        time: "11:58",
        kind: "debug",
        mins: 9,
        prompt: "타입 생성이 실패하는데, ipc.ts가 안 만들어져.",
        actions: 18,
        filesChanged: 1,
        tokens: "9.9k",
        retries: 2,
        calls: [
          { time: "11:59", tool: "Bash", arg: "wails generate module", dur: "11.4s", ok: false },
          { time: "12:04", tool: "Edit", arg: "frontend/src/lib/ipc.ts", dur: "1.1s", ok: true },
          { time: "12:07", tool: "Bash", arg: "wails generate module", dur: "10.8s", ok: false },
        ],
      },
      {
        time: "12:09",
        kind: "verify",
        mins: 5,
        prompt: "빌드만 확인해줘.",
        actions: 7,
        filesChanged: 0,
        tokens: "7.5k",
        retries: 0,
        calls: [
          { time: "12:10", tool: "Bash", arg: "wails build", dur: "38.2s", ok: true },
        ],
      },
    ],
    files: [
      { dir: "cmd/pulsemetry-gui/bridge/", name: "bridge.go", add: "+204", del: "0" },
      { dir: "cmd/pulsemetry-gui/", name: "app.go", add: "+37", del: "-11" },
      { dir: "frontend/src/lib/", name: "ipc.ts", add: "+62", del: "-8" },
      { dir: "cmd/pulsemetry-gui/bridge/", name: "types.go", add: "+88", del: "0" },
      { dir: "frontend/src/lib/", name: "client.ts", add: "+31", del: "-14" },
      { dir: "cmd/pulsemetry-gui/", name: "main.go", add: "+12", del: "-3" },
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
    kpi: ["37m", "26k", "19", "2", "$0.61", "66k", "11k", "$0.88"],
    stages: [
      { name: "Exploring", dur: "5m", weight: 5 },
      { name: "Implementing", dur: "21m", weight: 21 },
      { name: "Debugging", dur: "6m", weight: 6 },
      { name: "Verifying", dur: "5m", weight: 5 },
    ],
    active: 3,
    turns: [
      {
        time: "10:02",
        kind: "explore",
        mins: 6,
        prompt: "토큰 검증 미들웨어가 여러 군데 중복된 것 같은데 정리 가능한지 봐줘.",
        actions: 7,
        filesChanged: 0,
        tokens: "3.3k",
        retries: 0,
        calls: [
          { time: "10:03", tool: "Grep", arg: '"validateToken"', dur: "0.2s", ok: true },
        ],
      },
      {
        time: "10:17",
        kind: "implement",
        mins: 24,
        prompt: "중복 로직을 validator.go 하나로 합쳐줘. 동작은 그대로.",
        actions: 20,
        filesChanged: 2,
        tokens: "15.1k",
        retries: 1,
        calls: [
          { time: "10:18", tool: "Write", arg: "internal/auth/validator.go", dur: "1.7s", ok: true },
          { time: "10:29", tool: "Edit", arg: "internal/auth/middleware.go", dur: "1.0s", ok: true },
        ],
      },
      {
        time: "10:31",
        kind: "verify",
        mins: 7,
        prompt: "테스트 통과하는지 확인.",
        actions: 8,
        filesChanged: 0,
        tokens: "7.6k",
        retries: 0,
        calls: [
          { time: "10:32", tool: "Bash", arg: "go test ./internal/auth/...", dur: "14.9s", ok: true },
        ],
      },
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
    state: "done",
    title: "Fix linter issues",
    repo: "pulsemetry-web",
    path: "src/lib",
    dur: "16m",
    tokens: "8k",
    cost: "$0.14",
    range: "09:18 – 09:34 (16분, 중단)",
    kpi: ["16m", "8k", "7", "0", "$0.14", "18k", "3k", "$0.21"],
    stages: [
      { name: "Exploring", dur: "4m", weight: 4 },
      { name: "Implementing", dur: "12m", weight: 12 },
    ],
    active: 1,
    turns: [
      {
        time: "09:18",
        kind: "explore",
        mins: 5,
        prompt: "ESLint 에러 목록 보여줘.",
        actions: 4,
        filesChanged: 0,
        tokens: "1.6k",
        retries: 0,
        calls: [
          { time: "09:19", tool: "Bash", arg: "npx eslint src --format json", dur: "8.2s", ok: true },
        ],
      },
      {
        time: "09:29",
        kind: "implement",
        mins: 11,
        prompt: "utils.ts랑 format.ts의 규칙 위반만 고쳐줘.",
        actions: 9,
        filesChanged: 2,
        tokens: "6.4k",
        retries: 0,
        calls: [
          { time: "09:30", tool: "Edit", arg: "src/lib/utils.ts", dur: "0.8s", ok: true },
          { time: "09:33", tool: "Edit", arg: "src/lib/format.ts", dur: "0.5s", ok: true },
        ],
      },
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
    kpi: ["19m", "9k", "9", "0", "$0.16", "22k", "4k", "$0.27"],
    stages: [
      { name: "Exploring", dur: "6m", weight: 6 },
      { name: "Implementing", dur: "10m", weight: 10 },
      { name: "Verifying", dur: "3m", weight: 3 },
    ],
    active: 2,
    turns: [
      {
        time: "08:41",
        kind: "explore",
        mins: 5,
        prompt: "로컬 개발 환경 설정 파일들 어디 있어?",
        actions: 5,
        filesChanged: 0,
        tokens: "2.0k",
        retries: 0,
        calls: [
          { time: "08:42", tool: "Glob", arg: "deploy/**", dur: "0.3s", ok: true },
        ],
      },
      {
        time: "08:46",
        kind: "implement",
        mins: 14,
        prompt: "docker-compose에 collector 서비스를 추가하고 .env.example도 맞춰줘.",
        actions: 10,
        filesChanged: 2,
        tokens: "7.0k",
        retries: 0,
        calls: [
          { time: "08:47", tool: "Edit", arg: "deploy/local/docker-compose.yml", dur: "0.9s", ok: true },
          { time: "08:54", tool: "Edit", arg: "deploy/local/.env.example", dur: "0.4s", ok: true },
        ],
      },
    ],
    files: [
      { dir: "deploy/local/", name: "docker-compose.yml", add: "+18", del: "-6" },
      { dir: "deploy/local/", name: ".env.example", add: "+11", del: "-2" },
    ],
  },
];

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

// bl: 세션 상세(드로어) 표시 데이터 계산
export function detailDisplay(index: number) {
  const t = SESSIONS[index];
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
        preview: u.prompt.split("\n")[0],
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
    position: `${index + 1} / ${SESSIONS.length}`,
  };
}
