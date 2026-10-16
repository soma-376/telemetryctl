import AgentBadge from "$lib/components/ui/AgentBadge";
import EmptyState from "$lib/components/ui/EmptyState";
import Pill from "$lib/components/ui/Pill";
import { cssStyle } from "$lib/react-utils";
import { Fragment } from "react";
import type { ActivityData } from "../types";

export default function ActivityList({
  activity,
  onViewAll,
}: {
  activity: ActivityData;
  onViewAll?: () => void;
}) {
  const STATUS = {
    running: { label: "진행 중", fg: "#8b6b36", bg: "var(--color-sand-soft)" },
    done: {
      label: "종료",
      fg: "var(--color-text-secondary)",
      bg: "var(--color-inactive-soft)",
    },
  } as const;
  const note = `${activity.total} 세션${activity.running ? ` · 진행 중 ${activity.running}` : ""}`;

  return (
    <>
      <div className="bg-surface border-border border rounded-[14px] p-[14px_22px_8px]">
        <div className="flex items-baseline gap-[10px] mb-[2px]">
          <div className="text-text font-bold text-[16px] tracking-[-0.01em]">
            활동
          </div>
          <div className="text-text-muted text-[12.5px]">{note}</div>
          <div className="flex-1" />
          <button
            type="button"
            onClick={onViewAll}
            className="text-text-secondary hover:text-text cursor-pointer border-none bg-transparent font-medium whitespace-nowrap text-[13px]"
          >
            전체 보기 →
          </button>
        </div>
        {activity.rows.length === 0 ? (
          <>
            <EmptyState
              title="표시할 세션이 없어요"
              description="선택한 기간에 실행된 세션이 없습니다."
            />
          </>
        ) : null}
        {activity.rows.map((t, i) => (
          <Fragment key={t.id}>
            {(() => {
              const st = STATUS[t.state];

              return (
                <>
                  <div
                    style={cssStyle(
                      "border-bottom:1px solid " +
                        String(
                          i === activity.rows.length - 1
                            ? "transparent"
                            : "#f5f1ea",
                        ),
                    )}
                    className="grid items-center grid-cols-[74px_28px_minmax(0,1fr)_46px_60px] gap-[12px] p-[9px_0]"
                  >
                    <span className="whitespace-nowrap tabular-nums">
                      <span className="text-text-muted block text-[11px] mb-[2px]">
                        {t.date}
                      </span>
                      <span className="text-text block font-semibold text-[12.5px]">
                        {t.time}
                      </span>
                    </span>
                    <AgentBadge agent={t.agent} size={28} />
                    <span className="min-w-0">
                      <span className="text-text block truncate font-semibold text-[13.5px] mb-[3px]">
                        {t.title}
                      </span>
                      <span className="text-text-muted block truncate text-[11px]">
                        {t.sub}
                      </span>
                    </span>
                    <span className="text-text-secondary text-right whitespace-nowrap text-[12.5px] tabular-nums">
                      {t.tokens}
                    </span>
                    <Pill
                      label={st.label}
                      fg={st.fg}
                      bg={st.bg}
                      className="justify-self-end"
                    />
                  </div>
                </>
              );
            })()}
          </Fragment>
        ))}
      </div>
    </>
  );
}
