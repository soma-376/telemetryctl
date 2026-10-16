import { period, periodRangeText, usePeriod } from "$lib/domain/period";
import type { AppSection } from "$lib/navigation";
import ActivityList from "./components/ActivityList";
import UsageHero from "./components/UsageHero";
import VendorTable from "./components/VendorTable";
import { useHomeQuery } from "$lib/query/home";
import { localTimeZone } from "$lib/utils/timezone";
import { homeView } from "./adapter";

export default function Home({
  onNavigate,
}: {
  onNavigate?: (tab: AppSection) => void;
}) {
  usePeriod();
  const p = period.value;
  const query = useHomeQuery({ ...p, tz: localTimeZone() });
  const view = query.data ? homeView(query.data) : null;
  const rangeText = periodRangeText(p);

  return (
    <>
      <main className="mx-auto w-full flex-1 max-w-[var(--page-max-width)] p-[14px_32px_6px]">
        <div className="flex items-baseline gap-[12px] mb-[14px]">
          <h1 className="text-text m-0 font-bold text-[38px] tracking-[-0.035em] leading-none">
            Home
          </h1>
          <div className="text-text-muted text-[14px]">({rangeText})</div>
          <button
            type="button"
            onClick={() => void query.refetch()}
            disabled={query.isFetching}
            className="ml-auto cursor-pointer text-text-secondary disabled:opacity-50 text-[13px]"
          >
            새로고침
          </button>
        </div>
        {query.isPending && (
          <p role="status" className="text-text-muted text-[13px]">
            홈 데이터를 불러오는 중입니다.
          </p>
        )}
        {query.isError && (
          <p role="alert" className="text-danger-strong text-[13px] mb-[12px]">
            홈 데이터를 조회하지 못했습니다. 데몬 실행 상태를 확인해주세요.
            {view ? " 마지막으로 받은 데이터를 표시합니다." : ""}
          </p>
        )}
        {view && (
          <>
            <UsageHero hero={view.hero} />
            {view.vendors.length > 0 && <VendorTable vendors={view.vendors} />}
            <ActivityList
              activity={view.activity}
              onViewAll={() => onNavigate?.("activity")}
            />
            <p className="text-text-muted text-[11px] mt-[10px]">
              평균은 활동이 있는 구간을 기준으로 계산합니다. 활동 시간은 세션
              시작일에, 목록의 토큰은 세션 전체에 해당합니다.
            </p>
          </>
        )}
      </main>
    </>
  );
}
