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
} from "$lib/ipc/dashboard";
import type { TrayLimitWindow, TraySession, TrayVendor } from "./types";

// 경계 번역 — TraySnapshot(백엔드 모양)을 트레이 화면 타입으로 옮긴다.
//
// model.ts 와 나눠 둔 이유: model.ts 는 화면 안의 계산(어느 창이 대표인가, 무슨 색인가)이고
// 여기는 바깥 모양을 안쪽 모양으로 바꾸는 일이다. 둘 다 순수 함수지만 바뀌는 이유가 다르다 —
// 여기는 백엔드 필드가 바뀌면, model.ts 는 디자인이 바뀌면 고친다.

const AGENT_IDS = new Set<string>(["claude", "codex", "gemini", "cursor", "other"]);

// toAgentId 는 벤더 표기를 화면의 AgentId 로 옮긴다. 한도 조회와 로컬 파이프라인이 같은
// 표기를 쓰기로 되어 있어(vendorlimit.Vendor 주석) 두 출처를 이 함수 하나가 받는다.
// 모르는 벤더는 버리지 않고 "other" 로 남긴다 — 조용히 사라지면 합계가 맞지 않는다.
export function toAgentId(vendor: string): AgentId {
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

// toWindows 는 한 벤더의 창들을 화면 줄로 옮기고, 그중 **가장 빠듯한 하나**를 대표로 세운다.
// 대표를 사용률 최대로 정하는 것은 접힌 카드가 "실제로 막히는 한도" 를 보여줘야 하기 때문이다.
function toWindows(windows: LimitWindow[], now: Date): TrayLimitWindow[] {
  const labels = windowLabels(windows);
  let headAt = 0;
  for (let i = 1; i < windows.length; i++) {
    if (windows[i].used_ratio > windows[headAt].used_ratio) headAt = i;
  }
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

const capitalize = (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : "");

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

export function reasonText(reason: string): string {
  return REASON_TEXT[reason] ?? "한도를 알 수 없습니다";
}

export interface TrayView {
  vendors: TrayVendor[];
  unavailable: UnavailableVendor[];
  sessions: TraySession[];
  monitoring: TrayMonitoring;
  synced: string;
}

// toVendor 는 available 이고 창이 하나라도 있는 벤더만 카드로 만든다.
// 창이 비면 화면의 headOf 가 undefined 를 집어 그 자리에서 터진다.
function toVendor(r: VendorLimit, now: Date): TrayVendor | null {
  const windows = r.windows ?? [];
  if (r.state !== LimitState.StateAvailable || windows.length === 0) return null;
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
  if (s.duration_ms > 0) parts.push(formatDuration(Math.round(s.duration_ms / 60_000)));
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

// syncedText 는 스냅샷을 만든 시각을 "40초 전" 꼴로 만든다. 데이터의 신선도(last_event_at)가
// 아니라 **조회 시각**이다 — 새로고침 버튼이 방금 한 일을 말한다.
export function syncedText(refreshedAt: number, now: Date): string {
  if (refreshedAt <= 0) return "";
  const sec = Math.max(0, Math.round(now.getTime() / 1000 - refreshedAt));
  if (sec < 60) return `${sec}초 전`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}분 전`;
  return `${Math.round(min / 60)}시간 전`;
}

export function toTrayView(snap: TraySnapshot, now: Date = new Date()): TrayView {
  const limits = snap.limits ?? [];
  return {
    vendors: limits
      .map((r) => toVendor(r, now))
      .filter((v): v is TrayVendor => v !== null),
    unavailable: limits
      .filter((r) => r.state !== LimitState.StateAvailable)
      .map((r) => ({
        id: toAgentId(r.vendor),
        reason: r.reason,
        text: reasonText(r.reason),
      })),
    sessions: (snap.recent_sessions ?? []).map(toSession),
    monitoring: snap.monitoring,
    synced: syncedText(snap.refreshed_at, now),
  };
}
