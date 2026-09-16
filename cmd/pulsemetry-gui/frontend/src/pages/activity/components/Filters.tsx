import ChevronDownIcon from "$lib/icons/ChevronDownIcon";
import { Fragment } from "react";

export default function Filters() {
  const filters = ["모든 프로젝트", "All agents", "모든 상태"];

  return (
    <>
      <div className="flex items-center gap-[10px]">
        {filters.map((label) => (
          <Fragment key={label}>
            <button
              type="button"
              className="bg-surface border-border text-text hover:border-border-strong flex flex-none cursor-pointer items-center whitespace-nowrap border gap-[9px] rounded-[10px] p-[10px_14px] text-[13px]"
            >
              {` ${label} `}
              <ChevronDownIcon
                strokeWidth={2}
                className="text-text-muted ml-[10px]"
              />
            </button>
          </Fragment>
        ))}
        <div className="min-w-[12px] flex-1" />
        <div className="bg-surface border-border flex flex-none items-center border gap-[9px] rounded-[10px] p-[10px_14px] w-[260px]">
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="var(--color-text-muted)"
            strokeWidth="1.8"
            strokeLinecap="round"
            className="flex-none w-[16px] h-[16px]"
          >
            <circle cx="11" cy="11" r="6.5" />
            <path d="m16 16 4 4" />
          </svg>
          <span className="text-text-muted overflow-hidden text-ellipsis whitespace-nowrap text-[13px]">
            작업, 경로, 파일 검색
          </span>
        </div>
      </div>
    </>
  );
}
