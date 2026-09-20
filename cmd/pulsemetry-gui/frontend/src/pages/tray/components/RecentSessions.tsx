import EmptyState from "$lib/components/ui/EmptyState";
import { Fragment } from "react";
import type { TraySession } from "../types";
import RecentSessionRow from "./RecentSessionRow";

export default function RecentSessions({
  sessions,
}: {
  sessions: TraySession[];
}) {
  return (
    <>
      <section
        aria-labelledby="recent-sessions-title"
        className="p-[5px_14px_8px]"
      >
        <h2
          id="recent-sessions-title"
          className="text-text-muted font-semibold text-[11px] tracking-[0.02em] mb-[7px]"
        >
          최근 세션
        </h2>
        {sessions.map((session) => (
          <Fragment key={session.id}>
            <RecentSessionRow session={session} />
          </Fragment>
        ))}
        {sessions.length === 0 ? (
          <>
            <EmptyState
              size="sm"
              pose="collecting-alt"
              title="아직 세션이 없습니다"
              description="AI 도구로 작업을 시작하면 여기에 나타납니다."
            />
          </>
        ) : null}
      </section>
    </>
  );
}
