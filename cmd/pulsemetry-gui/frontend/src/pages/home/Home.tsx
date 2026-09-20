import { period, periodRangeText, usePeriod } from "$lib/domain/period";
import type { AppSection } from "$lib/navigation";
import ActivityList from "./components/ActivityList";
import UsageHero from "./components/UsageHero";
import VendorTable from "./components/VendorTable";
import { buildActivity, heroData, vendorRows } from "./mock";

export default function Home({
  onNavigate,
}: {
  onNavigate?: (tab: AppSection) => void;
}) {
  usePeriod();
  const p = period.value;
  const hero = heroData(p.start, p.end);
  const vendors = vendorRows(hero);
  const activity = buildActivity(p.start, p.end);
  const rangeText = periodRangeText(p);

  return (
    <>
      <main className="mx-auto w-full flex-1 max-w-[var(--page-max-width)] p-[14px_32px_6px]">
        <div className="flex items-baseline gap-[12px] mb-[14px]">
          <h1 className="text-text m-0 font-bold text-[38px] tracking-[-0.035em] leading-none">
            Home
          </h1>
          <div className="text-text-muted text-[14px]">({rangeText})</div>
        </div>
        <UsageHero hero={hero} />
        <VendorTable vendors={vendors} />
        <ActivityList
          activity={activity}
          onViewAll={() => onNavigate?.("activity")}
        />
      </main>
    </>
  );
}
