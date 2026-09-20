import {
  reconnect,
  retryNote,
  retryNow,
  useReconnect,
} from "$lib/domain/reconnect";
import RefreshIcon from "$lib/icons/RefreshIcon";
import { cssStyle } from "$lib/react-utils";
import EmptyState from "./ui/EmptyState";

export default function DaemonDown() {
  useReconnect();

  return (
    <>
      <EmptyState
        pose="confused"
        title="데몬에 연결할 수 없어요"
        description="Pulsemetry 데몬이 응답하지 않아 사용량을 확인할 수 없어요. 수집도 멈춘 상태예요."
        action={
          <>
            <div className="flex flex-col items-center gap-[11px]">
              <button
                type="button"
                disabled={reconnect.retrying}
                onClick={() => retryNow()}
                style={cssStyle(
                  "background:" +
                    String(
                      reconnect.retrying ? "#f7e4c6" : "var(--color-surface)",
                    ) +
                    ";border-color:" +
                    String(reconnect.retrying ? "#f0d2ae" : "#e0b071") +
                    ";color:" +
                    String(reconnect.retrying ? "#8b6b36" : "#9a6a14"),
                )}
                className={[
                  "inline-flex items-center border font-semibold transition-[background,border-color] duration-[180ms] ease-in-out " +
                    String(
                      reconnect.retrying
                        ? "cursor-default"
                        : "cursor-pointer hover:bg-[#fbf1e4]",
                    ),
                  "gap-[8px] rounded-[10px] p-[10px_20px] text-[13px] [white-space:nowrap]",
                ]
                  .filter(Boolean)
                  .join(" ")}
              >
                <RefreshIcon
                  size={14}
                  strokeWidth={2.2}
                  style={
                    "animation:" +
                    String(
                      reconnect.retrying
                        ? "spin 900ms linear infinite"
                        : "none",
                    )
                  }
                  className="[transform-origin:50%_50%]"
                />
                {reconnect.retrying ? "연결 중" : "지금 재연결"}
              </button>

              <div className="text-text-muted text-[11px] [white-space:nowrap]">
                {retryNote()}
              </div>
            </div>
          </>
        }
      />
    </>
  );
}
