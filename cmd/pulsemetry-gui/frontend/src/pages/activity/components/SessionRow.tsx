import AgentBadge from "$lib/components/ui/AgentBadge";
import Dot from "$lib/components/ui/Dot";
import Pill from "$lib/components/ui/Pill";
import { cssStyle } from "$lib/react-utils";
import { rowDisplay } from "../model";
import type { ActivitySession } from "../types";
import "./SessionRow.css";

export default function SessionRow({
  session,
  selected = false,
  onOpen,
}: {
  session: ActivitySession;
  selected?: boolean;
  onOpen?: () => void;
}) {
  const d = rowDisplay(session, selected);

  return (
    <>
      <button
        type="button"
        onClick={() => onOpen?.()}
        style={cssStyle(
          "--row-bg:" + String(d.bg) + ";box-shadow:" + String(d.rail),
        )}
        className="row grid w-full cursor-pointer items-center border-b text-center scope-r6mhn2 grid-cols-[14px_58px_minmax(0,1.35fr)_minmax(0,1fr)_62px_62px_62px_76px] gap-[12px] p-[12px_18px] [border-color:#f1ece4]"
      >
        <span className="flex justify-center scope-r6mhn2">
          <Dot size={9} color={d.dot} pulse={d.running} />
        </span>
        <span className="text-text-secondary scope-r6mhn2 text-[13px] tabular-nums">
          {d.time}
        </span>
        <span className="flex min-w-0 items-center justify-center scope-r6mhn2 gap-[10px]">
          <AgentBadge agent={d.agentId} size={28} />
          <span className="scope-r6mhn2 min-w-0">
            <span className="text-text block overflow-hidden font-semibold text-ellipsis whitespace-nowrap scope-r6mhn2 text-[13.5px] mb-[3px]">
              {d.title}
            </span>
            <span className="block overflow-hidden text-ellipsis whitespace-nowrap scope-r6mhn2 text-[11.5px]">
              <span className="text-text-muted scope-r6mhn2">
                {d.agentName}
              </span>
              <span className="font-semibold scope-r6mhn2 text-[var(--color-accent-hover)]">
                {d.stageText}
              </span>
            </span>
          </span>
        </span>
        <span className="flex min-w-0 items-baseline justify-center scope-r6mhn2 [font-family:var(--font-mono)] text-[12px]">
          <span className="text-text flex-none scope-r6mhn2">{d.repo}</span>
          <span className="text-text-muted flex-none scope-r6mhn2">/</span>
          <span className="text-text-muted min-w-0 overflow-hidden text-ellipsis whitespace-nowrap scope-r6mhn2 [direction:rtl]">
            <bdi className="scope-r6mhn2">{d.path}</bdi>
          </span>
        </span>
        <span
          style={cssStyle(
            "color:" +
              String(d.durColor) +
              ";font-weight:" +
              String(d.durWeight),
          )}
          className="scope-r6mhn2 text-[12.5px] tabular-nums"
        >
          {d.dur}
        </span>
        <span className="text-text scope-r6mhn2 text-[12.5px] tabular-nums">
          {d.tokens}
        </span>
        <span className="text-text scope-r6mhn2 text-[12.5px] tabular-nums">
          {d.cost}
        </span>
        <Pill
          label={d.badge.label}
          fg={d.badge.fg}
          bg={d.badge.bg}
          radius={6}
          padding="4px 8px"
          gap={5}
          dot={d.badge.dot}
          pulse={d.running}
          className="justify-self-center"
        />
      </button>
    </>
  );
}
