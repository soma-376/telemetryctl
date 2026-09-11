import type { ActivityDetail, SessionRow } from "$lib/bindings";
import type { ActivitySession, TurnKind } from "./types";

const number = (n: number) => n.toLocaleString();
const clock = (s: number | null) => s ? new Date(s * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "—";
const duration = (s: number) => s < 60 ? `${Math.max(0, Math.round(s))}초` : `${Math.floor(s / 60)}분`;
const usd = (n: number) => `$${n.toFixed(4)}`;
const kind = (value: string): TurnKind => ({ exploration: "explore", implementation: "implement", debugging: "debug", verification: "verify" } as Record<string, TurnKind>)[value] ?? "unknown";
const tokens = (n: number, known: number, calls: number) => calls > 0 && known === 0 ? "미수집" : number(n) + (known < calls ? " (일부)" : "");

export function sessionRow(row: SessionRow): ActivitySession {
  const tokenText = tokens(row.input_tokens + row.output_tokens, row.usage_calls, row.api_requests);
  const cost = row.reported_cost_calls === 0 ? "—" : usd(row.cost_usd) + (row.reported_cost_calls < row.api_requests ? " (일부)" : "");
  return {
    id: String(row.id), time: row.started_at ? new Date(row.started_at * 1000).toLocaleString([], { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }) : "—",
    agentId: row.vendor === "claude_code" ? "claude" : row.vendor === "codex" ? "codex" : "other",
    state: row.status === "running" ? "running" : "done",
    title: row.title || "제목 없는 세션", repo: row.project_name || "프로젝트 미확인", path: row.workspace_path,
    dur: row.started_at ? duration(row.duration_ms / 1000) : "—", tokens: tokenText, cost,
    range: `${clock(row.started_at)} ~ ${row.ended_at === null ? "진행 중" : clock(row.ended_at)}`,
    kpi: [], stages: [], active: 0, turns: [], files: [],
  };
}

export function sessionDetail(data: ActivityDetail): ActivitySession {
  const row = data.detail.session;
  const result = sessionRow(row);
  const m = data.metrics;
  const total = m.totals;
  const cost = total.cost.reported_calls + total.cost.estimated_calls === 0 ? "계산 불가" : usd(total.cost.total.usd) + (!total.cost.complete ? " (일부)" : "");
  const tokenText = tokens(total.tokens.input_tokens + total.tokens.output_tokens, total.usage_calls, total.llm_calls);
  const classes = new Map((data.classification.turns ?? []).map(t => [t.turn_id, t]));
  result.workType = kind(data.classification.work_type);
  result.cost = cost;
  result.kpi = [result.dur, tokenText, number(total.tool_calls), "미수집", cost,
    total.usage_calls ? number(total.tokens.cache_read_tokens) : "—",
    total.usage_calls ? number(total.tokens.cache_write_tokens) : "—",
    total.cache_savings.available_calls ? usd(total.cache_savings.total.usd) + (!total.cache_savings.complete ? " (일부)" : "") : "계산 불가"];
  result.turns = (m.turns ?? []).filter(t => !t.virtual).map(t => ({
    id: t.turn_id, time: clock(t.started_at), kind: kind(classes.get(t.turn_id)?.work_type ?? "unknown"),
    mins: t.duration_seconds === null ? null : t.duration_seconds / 60,
    prompt: t.prompt_text, promptTruncated: t.prompt_truncated,
    actions: t.tool_calls, filesChanged: t.files_changed,
    tokens: tokens(t.tokens.input_tokens + t.tokens.output_tokens, t.usage_calls, t.llm_calls), retries: null,
    calls: (data.detail.tools ?? []).filter(c => c.turn_id === t.turn_id).map(c => ({
      id: c.id, time: clock(c.ts), tool: c.tool_name, arg: c.target || "—",
      dur: c.duration_ms === null ? "—" : duration(c.duration_ms / 1000), ok: c.success,
    })),
  }));
  result.files = (data.detail.files ?? []).map(f => {
    const path = f.file_path.replaceAll("\\", "/");
    return { dir: path.slice(0, path.lastIndexOf("/") + 1), name: f.file_name, add: `+${f.lines_added}`, del: `−${f.lines_removed}` };
  });
  result.notice = [m.turns_truncated ? "턴 목록은 일부만 표시됩니다." : "", data.detail.tools_truncated ? "도구 호출은 일부만 표시됩니다." : "",
    (m.turns ?? []).some(t => t.virtual && (t.tool_calls > 0 || t.llm_calls > 0)) ? "턴에 연결되지 않은 호출도 상단 합계에 포함됩니다." : ""].filter(Boolean).join(" ");
  return result;
}
