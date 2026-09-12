import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import ts from "typescript";

// 순수 표시 함수를 Wails 런타임 없이 검증한다.
const formatSource = readFileSync(new URL("../src/lib/utils/format.ts", import.meta.url), "utf8");
const formatJS = ts.transpileModule(formatSource, { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText;
const formatURL = `data:text/javascript;base64,${Buffer.from(formatJS).toString("base64")}`;
const source = readFileSync(new URL("../src/pages/activity/adapter.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText;
const { sessionRow, sessionDetail } = await import(`data:text/javascript;base64,${Buffer.from(compiled.replace("$lib/utils/format", formatURL)).toString("base64")}`);

const row = {
  id: 42, vendor: "codex", status: "running", title: "", project_name: "demo", workspace_path: "/demo",
  started_at: 1789139543, ended_at: null, duration_ms: 2000,
  input_tokens: 0, output_tokens: 0, api_requests: 1, reported_cost_calls: 0, cost_usd: 0,
};

test("토큰 합계를 축약하고 제목을 폴백한다", () => {
  assert.equal(sessionRow(row).tokens, "0");
  assert.equal(sessionRow(row).cost, "—");
  assert.equal(sessionRow(row).title, "제목 없는 세션");
  assert.equal(sessionRow({ ...row, input_tokens: 76_809 }).tokens, "76.8k");
  assert.equal(sessionRow({ ...row, input_tokens: 1_200_000 }).tokens, "1.2M");
  assert.equal(sessionRow({ ...row, cost_usd: 0.135, reported_cost_calls: 1 }).cost, "0.14$");
  assert.equal(sessionRow({ ...row, duration_ms: 69 * 60_000 }).dur, "1h 09m");
  for (const hour of [0, 9, 21]) {
    const started_at = new Date(2026, 8, 12, hour, 0).getTime() / 1000;
    assert.equal(sessionRow({ ...row, started_at }).time, `${String(hour).padStart(2, "0")}:00`);
  }
});

test("도구를 턴 ID로 연결하고 모르는 상태와 비용을 꾸미지 않는다", () => {
  const totals = { llm_calls: 1, tool_calls: 1,
    tokens: { input_tokens: 0, output_tokens: 0 },
    cost: { reported_calls: 0, estimated_calls: 0, complete: false, total: { usd: 0 } },
    cache_savings: { available_calls: 0 },
  };
  const data = {
    detail: { session: row, files: [], tools: [
      { id: 1, turn_id: 11, ts: row.started_at, tool_name: "Read", target: "/demo/a.go", success: null, duration_ms: null },
      { id: 2, turn_id: 12, ts: row.started_at, tool_name: "Edit", target: "/demo/b.go", success: true, duration_ms: 100 },
    ] },
    metrics: { totals, turns: [{ ...totals, turn_id: 11, virtual: false, started_at: row.started_at, duration_seconds: null, prompt_text: "", files_changed: 0 }] },
    classification: { work_type: "unknown", turns: [] },
  };
  const view = sessionDetail(data);
  assert.equal(view.cost, "계산 불가");
  assert.equal(view.turns[0].kind, "unknown");
  assert.equal(view.turns[0].mins, null);
  assert.equal(view.turns[0].retries, null);
  assert.deepEqual(view.turns[0].calls.map(c => c.id), [1]);
  assert.equal(view.turns[0].calls[0].ok, null);
});
