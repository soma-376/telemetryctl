import CheckIcon from "$lib/icons/CheckIcon";
import ClockIcon from "$lib/icons/ClockIcon";
import PulseIcon from "$lib/icons/PulseIcon";
import RefreshIcon from "$lib/icons/RefreshIcon";

export default function KpiGrid({ kpi }: { kpi: string[] }) {
  const tile = "bg-surface border-border border flex flex-col";

  return (
    <>
      <div className="grid grid-cols-[repeat(4,minmax(0,1fr))] gap-[10px]">
        <div
          className={[tile, "rounded-[11px] p-[12px_13px]"]
            .filter(Boolean)
            .join(" ")}
        >
          <ClockIcon size={16} className="text-text-secondary mb-[8px]" />
          <div className="text-text text-[19px] font-bold tracking-[-0.02em] mb-[4px]">
            {kpi[0]}
          </div>
          <div className="text-text-secondary whitespace-nowrap text-[11.5px]">
            소요 시간
          </div>
        </div>
        <div
          className={[tile, "rounded-[11px] p-[12px_13px]"]
            .filter(Boolean)
            .join(" ")}
        >
          <PulseIcon size={16} className="text-[var(--color-info)] mb-[8px]" />
          <div className="text-text text-[19px] font-bold tracking-[-0.02em] mb-[4px]">
            {kpi[1]}
          </div>
          <div className="text-text-secondary whitespace-nowrap text-[11.5px]">
            토큰 사용량
          </div>
        </div>
        <div
          className={[tile, "rounded-[11px] p-[12px_13px]"]
            .filter(Boolean)
            .join(" ")}
        >
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="var(--color-text-secondary)"
            strokeWidth="1.9"
            strokeLinecap="round"
            strokeLinejoin="round"
            className="w-[16px] h-[16px] mb-[8px]"
          >
            <path d="M14.5 4.5a3.5 3.5 0 0 1 5 5L9 20l-5 1 1-5z" />
          </svg>
          <div className="text-text text-[19px] font-bold tracking-[-0.02em] mb-[4px]">
            {kpi[2]}
          </div>
          <div className="text-text-secondary whitespace-nowrap text-[11.5px]">
            툴 호출 수
          </div>
        </div>
        <div
          className={[tile, "rounded-[11px] p-[12px_13px]"]
            .filter(Boolean)
            .join(" ")}
        >
          <RefreshIcon
            size={16}
            strokeWidth={1.9}
            className="text-[var(--color-danger)] mb-[8px]"
          />
          <div className="text-text text-[19px] font-bold tracking-[-0.02em] mb-[4px]">
            {kpi[3]}
          </div>
          <div className="text-text-secondary whitespace-nowrap text-[11.5px]">
            재시도
          </div>
        </div>
        <div
          className={[tile, "rounded-[11px] p-[12px_13px]"]
            .filter(Boolean)
            .join(" ")}
        >
          <div className="w-[16px] h-[16px] mb-[8px] text-[var(--color-success)] text-[15px] font-bold leading-none">
            $
          </div>
          <div className="text-text text-[19px] font-bold tracking-[-0.02em] mb-[4px]">
            {kpi[4]}
          </div>
          <div className="text-text-secondary whitespace-nowrap text-[11.5px]">
            예상 비용
          </div>
        </div>
        <div
          className={[tile, "rounded-[11px] p-[12px_13px]"]
            .filter(Boolean)
            .join(" ")}
        >
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="var(--color-session)"
            strokeWidth="1.9"
            strokeLinecap="round"
            strokeLinejoin="round"
            className="w-[16px] h-[16px] mb-[8px]"
          >
            <rect x="3" y="5" width="18" height="14" rx="3" />
            <path d="M7 12h5l1.5-2.5L15 14l1-2h1" />
          </svg>
          <div className="text-text text-[19px] font-bold tracking-[-0.02em] mb-[4px]">
            {kpi[5]}
          </div>
          <div className="text-text-secondary whitespace-nowrap text-[11.5px]">
            캐시 리드
          </div>
        </div>
        <div
          className={[tile, "rounded-[11px] p-[12px_13px]"]
            .filter(Boolean)
            .join(" ")}
        >
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="var(--color-session)"
            strokeWidth="1.9"
            strokeLinecap="round"
            strokeLinejoin="round"
            className="w-[16px] h-[16px] mb-[8px]"
          >
            <path d="M12 4v9" />
            <path d="m8.5 9.5 3.5 3.5 3.5-3.5" />
            <path d="M4 16v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" />
          </svg>
          <div className="text-text text-[19px] font-bold tracking-[-0.02em] mb-[4px]">
            {kpi[6]}
          </div>
          <div className="text-text-secondary whitespace-nowrap text-[11.5px]">
            캐시 라이트
          </div>
        </div>
        <div className="flex flex-col border [background:var(--color-success-soft)] [border-color:#c9e7d6] rounded-[11px] p-[12px_13px]">
          <CheckIcon className="text-[var(--color-success)] mb-[8px]" />
          <div className="text-[19px] font-bold tracking-[-0.02em] mb-[4px] text-[#1b7c46]">
            {kpi[7]}
          </div>
          <div className="whitespace-nowrap text-[11.5px] text-[#2f7e55]">
            캐시 절감
          </div>
        </div>
      </div>
    </>
  );
}
