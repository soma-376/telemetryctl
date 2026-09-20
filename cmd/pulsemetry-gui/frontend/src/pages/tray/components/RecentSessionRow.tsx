import AgentBadge from "$lib/components/ui/AgentBadge";
import Dot from "$lib/components/ui/Dot";
import { openMainWindow } from "$lib/ipc/app";
import { cssStyle } from "$lib/react-utils";
import { useState } from "react";
import type { TraySession } from "../types";
import "./RecentSessionRow.css";

export default function RecentSessionRow({
  session,
}: {
  session: TraySession;
}) {
  const [titleViewport, setTitleViewport] = useState<HTMLSpanElement | null>(
    null,
  );
  const [titleText, setTitleText] = useState<HTMLSpanElement | null>(null);
  const [overflow, setOverflow] = useState(0);

  function measureTitle() {
    if (!titleText || !titleViewport) return;
    setOverflow(Math.max(0, titleText.scrollWidth - titleViewport.clientWidth));
  }

  return (
    <>
      <button
        type="button"
        onClick={openMainWindow}
        onMouseEnter={measureTitle}
        onFocus={measureTitle}
        style={cssStyle(
          "border-left:3px solid " +
            String(session.live ? "var(--color-sand)" : "var(--color-border)"),
        )}
        className="session-row bg-surface hover:bg-surface-hover grid w-full cursor-pointer items-center text-left scope-x6o5n4 grid-cols-[8px_24px_minmax(0,1fr)] gap-[9px] [border:1px_solid_var(--color-border)] rounded-[11px] p-[7px_11px] mb-[7px]"
      >
        <Dot
          color={
            session.live ? "var(--color-sand)" : "var(--color-border-strong)"
          }
          pulse={session.live}
        />
        <AgentBadge agent={session.agentId} size={24} />
        <span className="scope-x6o5n4 min-w-0">
          <span
            ref={setTitleViewport}
            style={cssStyle(
              "--title-offset:-" +
                String(overflow) +
                "px;--title-duration:" +
                String(overflow / 35) +
                "s",
            )}
            className={[
              [
                "session-title text-text block overflow-hidden whitespace-nowrap font-semibold",
                overflow > 0 ? "overflowing" : "",
                "scope-x6o5n4",
              ]
                .filter(Boolean)
                .join(" "),
              "text-[12.5px] mb-[2px]",
            ]
              .filter(Boolean)
              .join(" ")}
          >
            <span
              ref={setTitleText}
              className="title-text block truncate scope-x6o5n4"
            >
              {session.title}
            </span>
          </span>
          <span className="text-text-muted block truncate scope-x6o5n4 text-[10.5px]">
            {session.sub}
          </span>
        </span>
      </button>
    </>
  );
}
