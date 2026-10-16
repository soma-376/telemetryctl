import AgentBadge from "$lib/components/ui/AgentBadge";
import ProgressBar from "$lib/components/ui/ProgressBar";
import { AGENT_STYLE } from "$lib/domain/agent";
import ChevronDownIcon from "$lib/icons/ChevronDownIcon";
import { cssStyle } from "$lib/react-utils";
import { Fragment, useState } from "react";
import { headOf, limitTone } from "../model";
import type { TrayVendor } from "../types";
import LimitWindowRow from "./LimitWindowRow";

export default function VendorLimitCard({ vendor }: { vendor: TrayVendor }) {
  const [open, setOpen] = useState(false);
  const style = AGENT_STYLE[vendor.id];
  const head = headOf(vendor.windows);
  const rest = vendor.windows.filter((window) => window !== head);
  const expandable = rest.length > 0;
  const expanded = expandable && open;
  const tone = limitTone(head.pct, style.fg);

  return (
    <>
      <button
        type="button"
        disabled={!expandable}
        onClick={() => setOpen(!open)}
        aria-expanded={expandable ? expanded : undefined}
        style={cssStyle(
          "border-color:" +
            String(
              expanded ? "var(--color-border-strong)" : "var(--color-border)",
            ),
        )}
        className={[
          "bg-surface block w-full border text-left " +
            String(
              expandable
                ? "cursor-pointer hover:border-border-strong"
                : "cursor-default",
            ),
          "rounded-[11px] p-[9px_12px] mb-[7px]",
        ]
          .filter(Boolean)
          .join(" ")}
      >
        <div
          style={cssStyle(
            "grid-template-columns:24px minmax(0,1fr) auto auto " +
              String(expandable ? "auto" : ""),
          )}
          className="grid items-center gap-[8px] mb-[6px]"
        >
          <AgentBadge
            agent={vendor.id}
            size={24}
            fontSize={Math.min(style.fontSm + 2, 15)}
          />
          <span className="flex items-baseline gap-[6px] min-w-0">
            <span className="text-text flex-none font-bold text-[12.5px]">
              {style.name}
            </span>
            <span className="flex-none text-[10.5px] text-[#c9c3ba]">·</span>
            <span className="text-text-secondary truncate text-[11px] min-w-0">
              {head.label}
            </span>
          </span>
          <span className="flex-none whitespace-nowrap text-[10.5px] text-[#b3aba0]">
            {head.reset}
          </span>
          <span
            style={cssStyle("color:" + String(tone.value))}
            className="flex-none text-right font-bold whitespace-nowrap text-[13px] tabular-nums min-w-[34px]"
          >
            {head.remain}
          </span>
          {expandable ? (
            <>
              <span className="flex items-center justify-end gap-[4px]">
                <span className="whitespace-nowrap text-[9.5px] text-[#b3aba0]">
                  {!expanded ? `+${rest.length}` : ""}
                </span>
                <ChevronDownIcon
                  size={12}
                  strokeWidth={2.4}

                  rotated={expanded}
                  className="flex-none text-[#b3aba0]"
                />
              </span>
            </>
          ) : null}
        </div>
        <ProgressBar pct={head.pct} color={tone.bar} animate={true} />
        {expanded ? (
          <>
            <div className="mt-[10px] pt-[9px] [border-top:1px_solid_#f1ece4]">
              {rest.map((window) => (
                <Fragment key={window.label}>
                  <LimitWindowRow window={window} accent={style.fg} />
                </Fragment>
              ))}

              {vendor.credential || vendor.spend || vendor.tokens ? (
                <>
                  <div className="flex items-baseline gap-[8px] pt-[2px]">
                    <span className="truncate text-[10.5px] text-[#b3aba0] min-w-0">
                      {vendor.credential}
                    </span>
                    <span className="flex-1" />
                    <span className="text-text-muted flex-none whitespace-nowrap text-[10.5px]">
                      {[vendor.spend, vendor.tokens]
                        .filter(Boolean)
                        .join(" · ")}
                    </span>
                  </div>
                </>
              ) : null}
            </div>
          </>
        ) : null}
      </button>
    </>
  );
}
