import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import ts from "typescript";

// 어댑터는 타입만 import하는 순수 함수다. Wails 런타임 없이 표시 계약을 검증한다.
const source = readFileSync(new URL("../src/pages/activity/adapter.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText;
const { sessionRow, sessionDetail } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString("base64")}`);

const row = {
  id: 42, vendor: "codex", status: "running", title: "", project_name: "demo", workspace_path: "/demo",
  started_at: 1789139543, ended_at: null, duration_ms: 2000,
  input_tokens: 0, output_tokens: 0, api_requests: 1, usage_calls: 0, reported_cost_calls: 0, cost_usd: 0,
};

test("미수집·관측된 0·부분 토큰을 구분하고 제목을 폴백한다", () => {
  assert.equal(sessionRow(row).tokens, "미수집");
  assert.equal(sessionRow(row).cost, "—");
  assert.equal(sessionRow(row).title, "제목 없는 세션");
  assert.equal(sessionRow({ ...row, usage_calls: 1 }).tokens, "0");
  assert.equal(sessionRow({ ...row, usage_calls: 1, api_requests: 2, input_tokens: 100 }).tokens, "100 (일부)");
});

test("도구를 턴 ID로 연결하고 모르는 상태와 비용을 꾸미지 않는다", () => {
  const totals = { llm_calls: 1, tool_calls: 1, usage_calls: 0,
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
