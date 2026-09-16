import AgentBadge from "$lib/components/ui/AgentBadge";
import ProgressBar from "$lib/components/ui/ProgressBar";
import { AGENT_STYLE } from "$lib/domain/agent";
import { cssStyle } from "$lib/react-utils";
import { Fragment } from "react";
import type { VendorRow } from "../types";

export default function VendorTable({ vendors }: { vendors: VendorRow[] }) {
  return (
    <>
      <div className="bg-surface border-border border rounded-[14px] p-[6px_22px_10px] mb-[12px]">
        {vendors.map((v, i) => (
          <Fragment key={v.id}>
            {(() => {
              const style = AGENT_STYLE[v.id];

              return (
                <>
                  <div
                    style={cssStyle(
                      "border-bottom:1px solid " +
                        String(
                          i === vendors.length - 1 ? "transparent" : "#f5f1ea",
                        ),
                    )}
                    className="grid items-center grid-cols-[30px_128px_74px_62px_minmax(0,1fr)_168px] gap-[16px] p-[12px_0]"
                  >
                    <AgentBadge agent={v.id} size={30} />
                    <span className="min-w-0">
                      <span className="text-text block truncate font-semibold text-[13.5px] mb-[3px]">
                        {style.name}
                      </span>
                      <span className="text-text-muted block truncate text-[11px]">
                        {v.plan}
                      </span>
                    </span>
                    <span className="text-right font-bold whitespace-nowrap text-[15px] tabular-nums">
                      {v.spend}
                    </span>
                    <span className="text-text-secondary text-right whitespace-nowrap text-[12.5px] tabular-nums">
                      {v.tokens}
                    </span>
                    <span className="flex items-center min-w-0 gap-[10px]">
                      <ProgressBar
                        pct={v.share}
                        color={style.fg}
                        height={6}
                        className="flex-1 min-w-0"
                      />
                      <span className="text-text-secondary flex-none font-semibold whitespace-nowrap text-[11.5px] tabular-nums">
                        {v.share}
                      </span>
                    </span>
                    <span className="text-text-muted truncate text-[11.5px]">
                      {v.topModel}
                    </span>
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
