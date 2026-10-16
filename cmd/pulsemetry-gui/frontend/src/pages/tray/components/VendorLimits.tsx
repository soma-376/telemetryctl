import AgentBadge from "$lib/components/ui/AgentBadge";
import EmptyState from "$lib/components/ui/EmptyState";
import { AGENT_STYLE } from "$lib/domain/agent";
import { Fragment } from "react";
import type { UnavailableVendor } from "../adapter";
import type { TrayVendor } from "../types";
import VendorLimitCard from "./VendorLimitCard";

export default function VendorLimits({
  vendors,
  unavailable = [],
}: {
  vendors: TrayVendor[];
  unavailable?: UnavailableVendor[];
}) {
  return (
    <>
      <section aria-label="벤더 한도" className="p-[10px_14px_3px]">
        {vendors.map((vendor) => (
          <Fragment key={vendor.id}>
            <VendorLimitCard vendor={vendor} />
            {vendor.warning ? (
              <p
                role="status"
                className="text-text-secondary px-3 pb-2 text-[10.5px] leading-relaxed"
              >
                {vendor.warning}
              </p>
            ) : null}
          </Fragment>
        ))}

        {unavailable.map((vendor) => (
          <Fragment key={vendor.id}>
            <div className="bg-surface border-border flex items-center border gap-[8px] rounded-[11px] p-[9px_12px] mb-[7px]">
              <AgentBadge
                agent={vendor.id}
                size={24}
                fontSize={Math.min(AGENT_STYLE[vendor.id].fontSm + 2, 15)}
              />
              <span className="text-text flex-none font-bold text-[12.5px]">
                {AGENT_STYLE[vendor.id].name}
              </span>
              <span className="text-text-muted text-[10.5px] min-w-0 flex-1 [text-align:right]">
                {vendor.text}
              </span>
            </div>
          </Fragment>
        ))}
        {vendors.length === 0 && unavailable.length === 0 ? (
          <>
            <EmptyState
              size="sm"
              pose="no-data"
              title="한도 정보가 없습니다"
              description="지원하는 도구에 로그인하면 남은 한도가 여기 표시됩니다."
            />
          </>
        ) : null}
      </section>
    </>
  );
}
