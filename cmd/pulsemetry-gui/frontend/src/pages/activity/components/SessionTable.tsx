import EmptyState from "$lib/components/ui/EmptyState";
import ChevronDownIcon from "$lib/icons/ChevronDownIcon";
import { Fragment } from "react";
import { dayHeadings } from "../model";
import type { ActivitySession } from "../types";
import SessionRow from "./SessionRow";

export default function SessionTable({
  sessions,
  selectedIndex = null,
  onOpen,
  hasMore = false,
  loadingMore = false,
  onLoadMore,
}: {
  sessions: ActivitySession[];
  hasMore?: boolean;
  loadingMore?: boolean;
  onLoadMore?: () => void;
  selectedIndex?: number | null;
  onOpen?: (index: number) => void;
}) {
  const indexed = sessions.map((session, index) => ({ session, index }));
  const running = indexed.filter((row) => row.session.state === "running");
  const completed = indexed.filter((row) => row.session.state !== "running");
  const headings = dayHeadings(completed.map((row) => row.session));

  return (
    <>
      <div className="bg-surface border-border overflow-hidden border rounded-[14px]">
        <div className="border-border text-text-muted grid items-center border-b text-center font-semibold grid-cols-[14px_58px_minmax(0,1.35fr)_minmax(0,1fr)_62px_62px_62px_76px] gap-[12px] p-[11px_18px] [background:#faf7f2] text-[11.5px] tracking-[0.02em]">
          <span />
          <span>시작</span>
          <span>작업</span>
          <span>경로</span>
          <span>소요</span>
          <span>토큰</span>
          <span>비용</span>
          <span>상태</span>
        </div>
        {sessions.length === 0 ? (
          <>
            <EmptyState
              title="세션이 없어요"
              description="선택한 기간에 실행된 세션이 없습니다.&#xA;기간을 넓히거나 필터를 지워보세요."
            />
          </>
        ) : (
          <>
            {running.map((row, index) => (
              <Fragment key={row.session.id}>
                <SessionRow
                  session={row.session}
                  selected={selectedIndex === row.index}
                  onOpen={() => onOpen?.(row.index)}
                />
              </Fragment>
            ))}
            {completed.map((row, i) => (
              <Fragment key={row.session.id}>
                {headings[i] ? (
                  <>
                    <div className="border-border text-text-secondary border-b font-semibold p-[8px_18px_7px] [background:#fbfaf7] text-[11.5px] tabular-nums">
                      {headings[i]}
                    </div>
                  </>
                ) : null}
                <SessionRow
                  session={row.session}
                  selected={selectedIndex === row.index}
                  onOpen={() => onOpen?.(row.index)}
                />
              </Fragment>
            ))}
            {hasMore ? (
              <>
                <button
                  disabled={loadingMore}
                  onClick={() => onLoadMore?.()}
                  type="button"
                  className="text-text-secondary hover:text-text flex w-full cursor-pointer items-center justify-center border-none bg-transparent gap-[7px] p-[14px] text-[12.5px]"
                >
                  {loadingMore ? "불러오는 중…" : "더 불러오기"}
                  <ChevronDownIcon strokeWidth={2} />
                </button>
              </>
            ) : null}
          </>
        )}
      </div>
    </>
  );
}
