import type { UpdateSnapshot } from "$lib/bindings";

export default function DaemonUpdates({
  snapshot,
  unavailable,
}: {
  snapshot?: UpdateSnapshot;
  unavailable: boolean;
}) {
  const hasResult = Boolean(
    snapshot?.last_success_at &&
    snapshot.latest_version &&
    typeof snapshot.update_available === "boolean",
  );
  const observedAt = snapshot?.last_success_at
    ? new Date(snapshot.last_success_at)
    : null;
  const showObservedAt = observedAt && !Number.isNaN(observedAt.getTime());
  let status = "업데이트 확인 중";

  if (unavailable) {
    status = "데몬에 연결할 수 없음";
  } else if (snapshot) {
    switch (snapshot.status) {
      case "disabled":
        status = "등록 후 확인할 수 있어요";
        break;
      case "unsupported":
        status = "서버가 업데이트 확인을 지원하지 않아요";
        break;
      case "error":
        status = "업데이트 확인에 실패했어요";
        break;
      case "ready":
        status = hasResult
          ? snapshot.update_available
            ? "업데이트 가능"
            : "최신 버전이에요"
          : "아직 확인하지 않았어요";

        break;
    }
  }

  return (
    <section
      aria-label="데몬 업데이트"
      className="border-track grid items-start border-t grid-cols-[32px_minmax(0,1fr)] gap-[12px] p-[13px_0]"
    >
      <span className="text-accent flex items-center justify-center w-[32px] h-[32px] rounded-[10px] [background:#f4f0e9] text-[14px]">
        ⇩
      </span>
      <div className="min-w-0">
        <div className="text-text font-semibold text-[13.5px] mb-[3px]">
          데몬 업데이트
        </div>
        <div
          role="status"
          className="text-text-secondary text-[12px] leading-[1.6]"
        >
          {status}
        </div>
        {snapshot?.current_version ? (
          <div className="text-text-muted text-[11.5px] leading-[1.6] [overflow-wrap:anywhere]">
            데몬 버전 {snapshot.current_version}
          </div>
        ) : null}
        {hasResult ? (
          <div className="text-text-muted text-[11.5px] leading-[1.6] [overflow-wrap:anywhere]">
            마지막 확인한 최신 버전 {snapshot?.latest_version}
          </div>
        ) : null}
        {showObservedAt ? (
          <div className="text-text-muted text-[11.5px] leading-[1.6]">
            마지막 성공 확인{" "}
            <time dateTime={snapshot?.last_success_at}>
              {observedAt.toLocaleString("ko-KR")}
            </time>
          </div>
        ) : null}
      </div>
    </section>
  );
}
