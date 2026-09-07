import { AGENT_NAMES } from "$lib/domain/agent";
import type { AgentId } from "$lib/domain/agent.types";
import { formatDuration } from "$lib/utils/format";
import {
  LimitState,
  type LimitWindow,
  type RecentSession,
  type TrayMonitoring,
  type TraySnapshot,
  type VendorLimit,
} from "$lib/bindings";
import type { TrayLimitWindow, TraySession, TrayVendor } from "./types";

// 경계 번역 — TraySnapshot(백엔드 모양)을 트레이 화면 타입으로 옮긴다.
//
// model.ts 와 나눠 둔 이유: 여기는 바깥 모양을 안쪽 모양으로 바꾸며 대표 창을 표시하고,
// model.ts 는 표시된 대표 창의 색 같은 화면 계산을 맡는다.

const AGENT_IDS = new Set<string>([
  "claude",
  "codex",
  "gemini",
  "cursor",
  "other",
]);

// toAgentId 는 벤더 표기를 화면의 AgentId 로 옮긴다. 한도 조회와 로컬 파이프라인이 같은
// 표기를 쓰기로 되어 있어(vendorlimit.Vendor 주석) 두 출처를 이 함수 하나가 받는다.
// 모르는 벤더는 버리지 않고 "other" 로 남긴다 — 조용히 사라지면 합계가 맞지 않는다.
function toAgentId(vendor: string): AgentId {
  if (vendor === "claude_code") return "claude";
  return AGENT_IDS.has(vendor) ? (vendor as AgentId) : "other";
}

const PERIOD_LABEL: Record<string, string> = {
  five_hour: "5시간 한도",
  weekly: "주간 한도",
  monthly: "월간 한도",
};

// windowLabels 는 창 이름을 사람이 읽는 말로 바꾼다.
//
// 같은 종류의 창이 둘 이상일 때(Claude 의 seven_day 와 seven_day_opus) 둘 다 "주간 한도" 가
// 되면 어느 쪽이 막혔는지 알 수 없다. 그때만 벤더가 붙인 원래 이름을 덧붙인다.
function windowLabels(windows: LimitWindow[]): string[] {
  const total = new Map<string, number>();
  for (const w of windows) total.set(w.period, (total.get(w.period) ?? 0) + 1);

  const seen = new Map<string, number>();
  return windows.map((w) => {
    const base = PERIOD_LABEL[w.period] ?? w.label;
    if ((total.get(w.period) ?? 0) < 2) return base;
    const n = (seen.get(w.period) ?? 0) + 1;
    seen.set(w.period, n);
    return n === 1 ? base : `${base} · ${w.label}`;
  });
}

// remainPct 는 사용률을 **남은 비율**로 뒤집는다. 화면의 막대와 숫자는 남은 쪽을 말한다.
// 한도를 넘겨 쓰면 used_ratio 가 1.0 을 넘을 수 있어 0 에서 자른다.
function remainPct(usedRatio: number): number {
  return Math.max(0, Math.min(100, Math.round((1 - usedRatio) * 100)));
}

const pad2 = (n: number) => (n < 10 ? `0${n}` : `${n}`);

// startOfDay 는 현지 자정이다. 초기화 시각이 "오늘/내일" 중 어디인지 세는 기준이 된다.
function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

// resetText 는 초기화 시각을 짧은 말로 만든다.
//
// 벤더에 따라 절대 시각만 주거나(Claude) 남은 시간만 준다(Codex). 둘 다 없으면 빈 문자열이다 —
// resets_in_seconds 의 0 을 "0초 뒤" 로 읽으면 방금 초기화된 것처럼 보인다.
function resetText(w: LimitWindow, now: Date): string {
  if (w.resets_at) {
    const at = new Date(w.resets_at);
    if (!Number.isNaN(at.getTime())) {
      const days = Math.round((startOfDay(at) - startOfDay(now)) / 86_400_000);
      const hm = `${pad2(at.getHours())}:${pad2(at.getMinutes())}`;
      if (days === 0) return `오늘 ${hm}`;
      if (days === 1) return `내일 ${hm}`;
      return `${at.getMonth() + 1}월 ${at.getDate()}일`;
    }
  }
  if (w.resets_in_seconds > 0) {
    return `${formatDuration(Math.round(w.resets_in_seconds / 60))} 뒤`;
  }
  return "";
}

// toWindows 는 한 벤더의 창들을 화면 줄로 옮기고 주간 한도를 대표로 세운다.
// 주간 창이 없으면 5시간, 그것도 없으면 첫 창을 쓴다 — 대표 카드는 벤더 사이에서 같은
// 기간을 안정적으로 보여주는 것이 목적이고, "가장 빠듯한 하나" 를 고르는 것이 아니다.
function toWindows(windows: LimitWindow[], now: Date): TrayLimitWindow[] {
  const labels = windowLabels(windows);
  const weeklyAt = windows.findIndex((window) => window.period === "weekly");
  const fiveHourAt = windows.findIndex(
    (window) => window.period === "five_hour",
  );
  const headAt = weeklyAt >= 0 ? weeklyAt : fiveHourAt >= 0 ? fiveHourAt : 0;
  return windows.map((w, i) => {
    const pct = remainPct(w.used_ratio);
    return {
      label: labels[i],
      pct,
      remain: `${pct}%`,
      reset: resetText(w, now),
      head: i === headAt,
    };
  });
}

const capitalize = (s: string) =>
  s ? s.charAt(0).toUpperCase() + s.slice(1) : "";

// UnavailableVendor 는 한도를 읽지 못한 벤더다.
//
// 목록에서 빼지 않고 따로 들고 나오는 이유는 백엔드가 실패한 벤더도 자리를 지켜서 주기
// 때문이다(vendorlimit.Snapshot 주석) — 조용히 사라지면 화면이 "아직 로딩 중" 과
// "로그인하지 않음" 을 구분하지 못한다.
export interface UnavailableVendor {
  id: AgentId;
  reason: string;
  text: string;
}

// REASON_TEXT 는 실패 원인을 사용자가 할 수 있는 일로 바꾼다.
//
// Result.Detail 을 쓰지 않는 이유는 백엔드가 명시한 대로다 — Detail 은 사람이 읽는 보조
// 설명이고 언제든 바뀐다. 화면 분기는 기계 판독 값인 Reason 으로 한다.
const REASON_TEXT: Record<string, string> = {
  credential_missing: "로그인하지 않았습니다",
  credential_unreadable: "자격증명을 읽을 권한이 없습니다",
  credential_malformed: "자격증명 형식을 알 수 없습니다",
  token_expired: "토큰이 만료됐습니다 — 해당 도구에서 다시 로그인하세요",
  network_error: "연결하지 못했습니다",
  upstream_status: "공급자가 응답을 거부했습니다",
  response_unrecognized: "응답 형식이 바뀐 것 같습니다",
  internal_error: "조회 중 오류가 났습니다",
};

function reasonText(reason: string): string {
  return REASON_TEXT[reason] ?? "한도를 알 수 없습니다";
}

interface TrayView {
  vendors: TrayVendor[];
  unavailable: UnavailableVendor[];
  sessions: TraySession[];
  monitoring: TrayMonitoring;
}

// toVendor 는 창이 하나라도 있는 벤더를 카드로 만든다.
//
// **state 를 보지 않는다.** 백엔드는 조회에 실패해도 직전 성공값을 지우지 않으므로
// (store 의 upsert 가 available 일 때만 windows 를 덮어쓴다) 그 숫자를 계속 보여주는 편이
// 낫다. 429 한 번에 "공급자가 응답을 거부했습니다" 로 바뀌면, 손에 든 값을 두고 아무것도
// 안 보여주는 셈이 된다. 값이 언제 기준인지는 헤더의 경과 시간이 말한다.
//
// 창이 비면 보여줄 숫자가 없다 — 그때만 null 이고, 안내 문구는 unavailable 쪽이 맡는다.
function toVendor(r: VendorLimit, now: Date): TrayVendor | null {
  const windows = r.windows ?? [];
  if (windows.length === 0) return null;
  return {
    id: toAgentId(r.vendor),
    plan: capitalize(r.plan),
    // 이 셋은 TraySnapshot 에 출처가 없다. 벤더별 지출·토큰은 Breakdown 이 따로 쥐고 있고,
    // 자격증명 종류는 vendorlimit 이 일부러 담지 않는다(토큰 유출 방지). 빈 문자열을 주면
    // 카드가 그 줄을 통째로 접는다.
    spend: "",
    tokens: "",
    credential: "",
    windows: toWindows(windows, now),
  };
}

const STATUS_TEXT: Record<string, string> = {
  running: "진행 중",
  completed: "완료",
};

function toSession(s: RecentSession): TraySession {
  const agentId = toAgentId(s.vendor);
  const parts = [AGENT_NAMES[agentId]];
  if (s.duration_ms > 0)
    parts.push(formatDuration(Math.round(s.duration_ms / 60_000)));
  const status = STATUS_TEXT[s.status];
  if (status) parts.push(status);
  return {
    id: String(s.id),
    agentId,
    // 제목이 아직 붙지 않은 세션이 있다. 폴더 이름이라도 보여 주는 편이 빈 줄보다 낫다.
    title: s.title || s.project_name || "제목 없음",
    sub: parts.join(" · "),
    live: s.status === "running",
  };
}

// fetchedAtText 는 데몬에서 스냅샷을 마지막으로 받은 시각이다 ("21:05 조회" 꼴).
//
// 경과 시간("3분 전")이 아니라 절대 시각인 이유는 둘이다. 하나, 경과를 세려면 매초 다시
// 그려야 하는데 트레이 창은 닫아도 숨겨질 뿐이라(Application.Hide) 웹뷰가 그 타이머를
// 늦추면 숫자가 굳는다. 둘, 폴링이 60초라 경과는 늘 1분 안쪽이고 그건 왼쪽의
// "● 모니터링 중" 과 같은 말이다.
//
// 한도 자체가 얼마나 낡았는지(limits_observed_at)는 여기서 말하지 않는다. 벤더 조회가
// 밀리는 것은 데몬이 쿨다운·backoff 로 다룰 문제다.
export function fetchedAtText(fetchedAtMs: number): string {
  // 숫자가 아닌 값이 새어 들어오면 Date 가 Invalid 가 되어 화면에 그대로 나간다.
  if (!Number.isFinite(fetchedAtMs) || fetchedAtMs <= 0) return "";
  const at = new Date(fetchedAtMs);
  return `${pad2(at.getHours())}:${pad2(at.getMinutes())}:${pad2(at.getSeconds())} 조회`;
}

export function toTrayView(
  snap: TraySnapshot,
  now: Date = new Date(),
): TrayView {
  const limits = snap.limits ?? [];
  return {
    vendors: limits
      .map((r) => toVendor(r, now))
      .filter((v): v is TrayVendor => v !== null),
    // 숫자를 하나도 못 들고 있는 벤더만 안내 문구로 간다. 직전 성공값이 있으면
    // 카드로 그린다 (toVendor).
    unavailable: limits
      .filter(
        (r) =>
          r.state !== LimitState.StateAvailable &&
          (r.windows ?? []).length === 0,
      )
      .map((r) => ({
        id: toAgentId(r.vendor),
        reason: r.reason,
        text: reasonText(r.reason),
      })),
    sessions: (snap.recent_sessions ?? []).map(toSession),
    monitoring: snap.monitoring,
  };
}
