import { TrayState } from "$lib/bindings";
import Dot from "$lib/components/ui/Dot";
import Mascot from "$lib/components/ui/Mascot";
import RefreshIcon from "$lib/icons/RefreshIcon";
import XIcon from "$lib/icons/XIcon";
import { hideCurrentWindow } from "$lib/ipc/app";
import { cssStyle } from "$lib/react-utils";
import { useEffect, useRef, useState } from "react";
import { observedAtText } from "../adapter";

export default function TrayHeader({
  observedAt,
  trayState,
  fetching = false,
  disconnected = false,
  onRefresh,
}: {
  /** 벤더 한도 관측 시각(RFC3339). 로컬 재조회 시각과 구분한다. */
  observedAt: string;
  trayState?: TrayState;
  /** 폴링을 포함해 조회가 나가 있다. */
  fetching?: boolean;
  /** 데몬에 닿지 못하는 상태다. 본문은 이미 대체됐고 헤더도 그 사실을 말한다. */
  disconnected?: boolean;
  onRefresh?: () => Promise<void> | void;
}) {
  const [pulling, setPulling] = useState(false);
  const busyNow = pulling || fetching;
  const MIN_BUSY_MS = 450;
  const [busy, setBusy] = useState(false);
  const busyUntilRef = useRef(0);

  useEffect(() => {
    if (busyNow) {
      setBusy(true);
      busyUntilRef.current = Date.now() + MIN_BUSY_MS;

      return;
    }
    const wait = busyUntilRef.current - Date.now();

    if (wait <= 0) {
      setBusy(false);

      return;
    }
    const id = setTimeout(() => setBusy(false), wait);

    return () => clearTimeout(id);
  }, [busyNow]);

  const synced = observedAtText(observedAt);

  async function pull() {
    if (pulling) return;
    setPulling(true);
    try {
      await onRefresh?.();
    } finally {
      setPulling(false);
    }
  }

  const status = (() => {
    // 데몬에 못 닿으면 수집 상태를 말할 수 없다. 마지막으로 알던 값을 그대로 두면
    // "모니터링 중" 이라고 거짓말하게 된다.
    if (disconnected) {
      return { text: "연결 끊김", color: "var(--color-warning)" };
    }
    switch (trayState) {
      case TrayState.StateMonitoring:
        return { text: "모니터링 중", color: "var(--color-success)" };
      case TrayState.StatePaused:
        return { text: "수집 중지됨", color: "var(--color-inactive)" };
      case TrayState.StateNotInstalled:
        return { text: "설치되지 않음", color: "var(--color-inactive)" };
      default:
        return { text: "연결 중", color: "var(--color-inactive)" };
    }
  })();

  return (
    <>
      <header className="bg-surface flex flex-none items-center gap-[10px] p-[12px_14px] [border-bottom:1px_solid_#ede7de]">
        <Mascot pose="view-front" height={26} />
        <span className="text-text flex-none font-bold text-[13.5px] tracking-[-0.01em]">
          Pulsemetry
        </span>
        <span className="text-text-secondary flex items-center gap-[5px] text-[11px] flex-1 min-w-0">
          <Dot size={6} color={status.color} />
          <span className="truncate">{status.text}</span>
        </span>

        <span className="flex-none whitespace-nowrap text-[12px] text-[#b3aba0]">
          {disconnected ? "" : busy ? "조회 중" : `${synced} 조회`}
        </span>

        {!disconnected ? (
          <>
            <button
              type="button"
              disabled={busy}
              title={busy ? "조회 중" : "새로고침"}
              onClick={pull}
              style={cssStyle(
                "border-color:" +
                  String(busy ? "#efe9e1" : "var(--color-border)") +
                  ";opacity:" +
                  String(busy ? "0.6" : "1"),
              )}
              className={[
                "flex flex-none items-center justify-center border bg-transparent transition-[opacity,border-color] duration-[180ms] ease-in-out " +
                  String(
                    busy
                      ? "text-text-muted cursor-default"
                      : "text-accent hover:border-border-strong hover:bg-surface-hover cursor-pointer",
                  ),
                "w-[30px] h-[30px] rounded-[9px]",
              ]
                .filter(Boolean)
                .join(" ")}
            >
              <RefreshIcon
                size={15}
                strokeWidth={2.2}
                style={
                  "animation:" +
                  String(busy ? "spin 900ms linear infinite" : "none")
                }
                className="[transform-origin:50%_50%]"
              />
            </button>
          </>
        ) : null}
        <button
          type="button"
          title="퀵뷰 닫기"
          aria-label="퀵뷰 닫기"
          onClick={hideCurrentWindow}
          className="border-border text-text-muted hover:text-text hover:border-border-strong hover:bg-surface-hover flex flex-none cursor-pointer items-center justify-center border bg-transparent transition-colors duration-[120ms] ease-in-out w-[30px] h-[30px] rounded-[9px]"
        >
          <XIcon size={15} strokeWidth={1.9} />
        </button>
      </header>
    </>
  );
}
