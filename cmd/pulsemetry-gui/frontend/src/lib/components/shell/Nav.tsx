import Pill from "$lib/components/ui/Pill";
import { Fragment } from "react";
import BarsIcon from "../../icons/BarsIcon";
import HomeIcon from "../../icons/HomeIcon";
import ListIcon from "../../icons/ListIcon";
import type { AppSection } from "../../navigation";

export default function Nav({
  active = "overview",
  onSelect,
}: {
  active?: AppSection;
  onSelect?: (tab: AppSection) => void;
}) {
  const TABS = [
    { id: "overview", label: "Home", icon: HomeIcon },
    { id: "activity", label: "Activity", icon: ListIcon },
  ] as const;

  return (
    <>
      <nav className="mx-auto w-full max-w-[var(--page-max-width)] p-[12px_32px_18px]">
        <div className="bg-surface border-border flex border gap-[6px] rounded-[16px] p-[7px]">
          {TABS.map((tab) => (
            <Fragment key={tab.id}>
              {(() => {
                const isActive = active === tab.id;

                return (
                  <>
                    <button
                      type="button"
                      onClick={() => onSelect?.(tab.id)}
                      className={[
                        "flex flex-1 cursor-pointer items-center justify-center border-none font-semibold whitespace-nowrap transition-colors duration-[120ms] ease-in-out " +
                          String(
                            isActive
                              ? "bg-accent-soft text-accent"
                              : "text-text-secondary bg-transparent hover:bg-surface-hover",
                          ),
                        "gap-[9px] p-[12px_8px] rounded-[12px] text-[14px]",
                      ]
                        .filter(Boolean)
                        .join(" ")}
                    >
                      <tab.icon size={17} strokeWidth={1.8} />
                      {tab.label}
                    </button>
                  </>
                );
              })()}
            </Fragment>
          ))}
          <span
            title="준비 중"
            className="text-text-muted flex flex-1 items-center justify-center font-semibold whitespace-nowrap gap-[9px] p-[12px_8px] rounded-[12px] text-[14px] [cursor:default]"
          >
            <BarsIcon size={17} strokeWidth={1.8} /> Insights{" "}
            <Pill
              label="준비 중"
              fg="var(--color-text-muted)"
              bg="var(--color-track)"
              fontSize={10.5}
              radius={5}
              padding="3px 6px"
            />
          </span>
        </div>
      </nav>
    </>
  );
}
