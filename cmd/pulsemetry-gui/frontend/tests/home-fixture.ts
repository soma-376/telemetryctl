import type { HomeSnapshot } from "../src/lib/bindings";

// 화면이 소비하는 수치만 둔다. 캐시 토큰과 생애 전체 세션 토큰을 일부러 크게 만든다.
export function homeFixture(): HomeSnapshot {
  const cost = {
    calls: 2,
    reported: 1,
    estimated: 0,
    unavailable: 1,
    total: { usd: 1.25, nano_usd: 1_250_000_000 },
  };
  const vendor = {
    vendor: "codex",
    tokens: 1200,
    token_share_permille: 1000,
    cost,
    models: [{ model: "gpt-test", tokens: 1200, cost }],
  };

  return {
    usage: {
      tz: "Asia/Seoul",
      date: "2026-09-20",
      start_at: 1789830000,
      end_at: 1789916400,
      totals: {
        input_tokens: 1000,
        output_tokens: 200,
        cache_read_tokens: 9000,
        reasoning_tokens: 50,
        active_seconds: 120,
        sessions_started: 9,
      },
      cost,
      vendors: [vendor],
      windows: [
        {
          start_at: 1789830000,
          end_at: 1789837200,
          local_hour: 0,
          tokens: 1200,
          active: true,
          vendors: [vendor],
        },
        {
          start_at: 1789837200,
          end_at: 1789844400,
          local_hour: 2,
          tokens: 0,
          active: false,
          vendors: [{ ...vendor, tokens: 0 }],
        },
      ],
      peak: { found: true, index: 0 },
    },
    end_date: "2026-09-20",
    unit: "hour",
    bucket_size: 2,
    recent: [
      {
        id: 42,
        vendor: "codex",
        title: "실제 세션",
        project_name: "demo",
        started_at: 1789830000,
        ended_at: null,
        duration_ms: 120_000,
        tokens: 2400,
      },
    ],
    recent_truncated: true,
    running_sessions: 1,
    active_agents: ["codex"],
    database_available: true,
  } as HomeSnapshot;
}
