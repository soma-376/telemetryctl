import type { ActivityDetail, SessionRow } from "$lib/bindings";
import type { ActivitySession, TurnKind } from "./types";
import { formatDuration } from "$lib/utils/format";

const number = (n: number) => n.toLocaleString();
const clock = (s: number | null) =>
  s
    ? new Date(s * 1000).toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
        hourCycle: "h23",
      })
    : "-";
const pad2 = (n: number) => String(n).padStart(2, "0");
// 로컬 자정으로 끊는다. toISOString 은 UTC 라 저녁 세션이 다음 날로 밀린다.
const dayKey = (s: number | null) =>
  s
    ? `${new Date(s * 1000).getFullYear()}-${pad2(new Date(s * 1000).getMonth() + 1)}-${pad2(new Date(s * 1000).getDate())}`
    : "";
const duration = (s: number) => formatDuration(s / 60);
const moneyFormat = new Intl.NumberFormat("en-US", {
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
  useGrouping: false,
});
const usd = (n: number) => `${moneyFormat.format(n)}$`;
const kind = (value: string): TurnKind =>
  (
    ({
      exploration: "explore",
      implementation: "implement",
      debugging: "debug",
      verification: "verify",
    }) as Record<string, TurnKind>
  )[value] ?? "unknown";
const tokens = (n: number) => {
  if (n < 1_000) return number(n);
  const millions = n >= 999_950;
  return `${Number((n / (millions ? 1_000_000 : 1_000)).toFixed(1))}${millions ? "M" : "k"}`;
};

export function sessionRow(row: SessionRow): ActivitySession {
  const tokenText = tokens(row.input_tokens + row.output_tokens);
  const cost = row.reported_cost_calls === 0 ? "-" : usd(row.cost_usd);
  return {
    id: String(row.id),
    time: clock(row.started_at),
    day: dayKey(row.started_at),
    agentId:
      row.vendor === "claude_code"
        ? "claude"
        : row.vendor === "codex"
          ? "codex"
          : "other",
    state: row.status === "running" ? "running" : "done",
    title: row.title || "제목 없는 세션",
    repo: row.project_name || "프로젝트 미확인",
    path: row.workspace_path,
    dur: row.started_at ? duration(row.duration_ms / 1000) : "-",
    tokens: tokenText,
    cost,
    range: `${clock(row.started_at)} ~ ${row.ended_at === null ? "진행 중" : clock(row.ended_at)}`,
    kpi: [],
    stages: [],
    active: 0,
    turns: [],
    files: [],
  };
}

export function sessionDetail(data: ActivityDetail): ActivitySession {
  const row = data.detail.session;
  const result = sessionRow(row);
  const m = data.metrics;
  const total = m.totals;
  const cost =
    total.cost.reported_calls + total.cost.estimated_calls === 0
      ? "계산 불가"
      : usd(total.cost.total.usd);
  const tokenText = tokens(
    total.tokens.input_tokens + total.tokens.output_tokens,
  );
  const classes = new Map(
    (data.classification.turns ?? []).map((t) => [t.turn_id, t]),
  );
  result.workType = kind(data.classification.work_type);
  result.cost = cost;
  result.kpi = [
    result.dur,
    tokenText,
    number(total.tool_calls),
    "미수집",
    cost,
    tokens(total.tokens.cache_read_tokens),
    tokens(total.tokens.cache_write_tokens),
    total.cache_savings.available_calls
      ? usd(total.cache_savings.total.usd)
      : "계산 불가",
  ];
  result.turns = (m.turns ?? [])
    .filter((t) => !t.virtual)
    .map((t) => ({
      id: t.turn_id,
      time: clock(t.started_at),
      kind: kind(classes.get(t.turn_id)?.work_type ?? "unknown"),
      mins: t.duration_seconds === null ? null : t.duration_seconds / 60,
      prompt: t.prompt_text,
      promptTruncated: t.prompt_truncated,
      actions: t.tool_calls,
      filesChanged: t.files_changed,
      tokens: tokens(t.tokens.input_tokens + t.tokens.output_tokens),
      retries: null,
      calls: (data.detail.tools ?? [])
        .filter((c) => c.turn_id === t.turn_id)
        .map((c) => ({
          id: c.id,
          time: clock(c.ts),
          tool: c.tool_name,
          arg: c.target || "-",
          dur:
            c.duration_ms === null
              ? "-"
              : c.duration_ms < 60_000
                ? `${(c.duration_ms / 1000).toFixed(1)}s`
                : duration(c.duration_ms / 1000),
          ok: c.success,
        })),
    }));
  result.files = (data.detail.files ?? []).map((f) => {
    const path = f.file_path.replaceAll("\\", "/");
    return {
      dir: path.slice(0, path.lastIndexOf("/") + 1),
      name: f.file_name,
      add: f.lines_added === null ? "-" : `+${f.lines_added}`,
      del: f.lines_removed === null ? "-" : `−${f.lines_removed}`,
    };
  });
  result.notice = [
    m.turns_truncated ? "턴 목록은 일부만 표시됩니다." : "",
    data.detail.tools_truncated ? "도구 호출은 일부만 표시됩니다." : "",
    (m.turns ?? []).some(
      (t) => t.virtual && (t.tool_calls > 0 || t.llm_calls > 0),
    )
      ? "턴에 연결되지 않은 호출도 상단 합계에 포함됩니다."
      : "",
  ]
    .filter(Boolean)
    .join(" ");
  return result;
}
