import type { CostSummary, HomeSnapshot } from "$lib/bindings";
import { AGENT_STYLE } from "$lib/domain/agent";
import type { AgentId } from "$lib/domain/agent.types";
import { formatDuration, formatTokens } from "$lib/utils/format";
import type { ActivityData, BucketUnit, HeroData, VendorRow } from "./types";

const agentId = (vendor: string): AgentId =>
  vendor === "claude_code" ? "claude" : vendor === "codex" ? "codex" : "other";
const vendorName = (vendor: string) =>
  agentId(vendor) === "other" ? vendor : AGENT_STYLE[agentId(vendor)].name;

function costText(cost: CostSummary): string {
  if (cost.calls > 0 && cost.unavailable === cost.calls) return "산정 불가";

  return `$${cost.total.usd.toFixed(2)}${cost.unavailable > 0 ? " 이상" : ""}`;
}

export function homeView(snapshot: HomeSnapshot): {
  hero: HeroData;
  vendors: VendorRow[];
  activity: ActivityData;
} {
  const usage = snapshot.usage;
  const windows = usage.windows ?? [];
  const vendors = usage.vendors ?? [];
  const unit: BucketUnit =
    snapshot.unit === "hour" ||
    snapshot.unit === "week" ||
    snapshot.unit === "month"
      ? snapshot.unit
      : "day";
  const unitLabel =
    unit === "hour"
      ? `${snapshot.bucket_size}시간`
      : unit === "day"
        ? "일"
        : unit === "week"
          ? "주"
          : "월";
  const dateLabel = new Intl.DateTimeFormat("ko-KR", {
    timeZone: usage.tz,
    month: "numeric",
    day: "numeric",
  });
  const clockLabel = new Intl.DateTimeFormat("ko-KR", {
    timeZone: usage.tz,
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  });
  const monthLabel = new Intl.DateTimeFormat("ko-KR", {
    timeZone: usage.tz,
    year: "2-digit",
    month: "short",
  });
  const total = usage.totals.input_tokens + usage.totals.output_tokens;
  const activeWindows = windows.filter((w) => w.active).length;
  const bars = windows.map((w) => {
    const date = new Date(w.start_at * 1000);

    return {
      label:
        unit === "month"
          ? monthLabel.format(date)
          : unit === "hour"
            ? w.local_hour === 0
              ? dateLabel.format(date)
              : clockLabel.format(date)
            : dateLabel.format(date),
      tooltipLabel:
        unit === "hour"
          ? `${dateLabel.format(date)} ${clockLabel.format(date)}`
          : undefined,
      values: vendors.map(
        (v) =>
          (w.vendors ?? []).find((row) => row.vendor === v.vendor)?.tokens ?? 0,
      ),
      totalValue: w.tokens,
    };
  });
  const peak = usage.peak.found ? bars[usage.peak.index] : undefined;

  return {
    hero: {
      unit,
      bucketSize: snapshot.bucket_size,
      caption: `${unitLabel} 단위 · 벤더 구성`,
      avgLabel: `${unitLabel} 평균`,
      avgValue: formatTokens(activeWindows ? total / activeWindows : 0),
      totalTokens: formatTokens(total),
      totalCost: costText(usage.cost),
      totalTime: formatDuration(usage.totals.active_seconds / 60),
      peakNote: peak
        ? `최다 ${peak.label} · ${formatTokens(peak.totalValue)}`
        : "",
      legend: vendors.map((v) => ({
        name: vendorName(v.vendor),
        color: AGENT_STYLE[agentId(v.vendor)].fg,
      })),
      bars,
      grandTotal: total,
      costNote: usage.cost.unavailable
        ? `비용을 산정하지 못한 호출 ${usage.cost.unavailable.toLocaleString()}건이 합계에서 제외되어 있습니다.`
        : "",
    },
    vendors: vendors.map((v) => {
      const model = v.models?.[0];

      return {
        id: v.vendor,
        agent: agentId(v.vendor),
        name: vendorName(v.vendor),
        plan: "구독 정보 미확인",
        spend: costText(v.cost),
        tokens: formatTokens(v.tokens),
        share: `${v.token_share_permille / 10}%`,
        topModel: model
          ? `${model.model || "모델 미확인"} · ${formatTokens(model.tokens)}`
          : "모델 사용 기록 없음",
      };
    }),
    activity: {
      total: usage.totals.sessions_started,
      running: snapshot.running_sessions,
      rows: (snapshot.recent ?? []).map((s) => ({
        id: String(s.id),
        date: s.started_at
          ? dateLabel.format(new Date(s.started_at * 1000))
          : "-",
        time: s.started_at
          ? clockLabel.format(new Date(s.started_at * 1000))
          : "-",
        agent: agentId(s.vendor),
        title: s.title || "제목 없는 세션",
        sub: `${s.project_name || "프로젝트 미확인"} · ${formatDuration(s.duration_ms / 60_000)}`,
        tokens: formatTokens(s.tokens),
        state: s.ended_at === null ? "running" : "done",
      })),
    },
  };
}
