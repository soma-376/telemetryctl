import { TrayState, type TrayQuery } from "$lib/bindings";
import DaemonDown from "$lib/components/DaemonDown";
import QuitDialog from "$lib/components/dialog/QuitDialog";
import EmptyState from "$lib/components/ui/EmptyState";
import {
  bindRetry,
  noteFailure,
  noteSuccess,
  reconnect,
  useReconnect,
} from "$lib/domain/reconnect";
import { isTrayVisible } from "$lib/ipc/app";
import { useTrayQuery, useTrayRefreshMutation } from "$lib/query/tray";
import { localTimeZone } from "$lib/utils/timezone";
import { Events } from "@wailsio/runtime";
import { useEffect, useRef, useState } from "react";
import { toTrayView } from "./adapter";
import RecentSessions from "./components/RecentSessions";
import TrayFooter from "./components/TrayFooter";
import TrayHeader from "./components/TrayHeader";
import TraySettingsMenu from "./components/TraySettingsMenu";
import VendorLimits from "./components/VendorLimits";

export default function TrayQuickView() {
  useReconnect();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [quitOpen, setQuitOpen] = useState(false);

  function requestQuit() {
    setSettingsOpen(false);
    setQuitOpen(true);
  }

  const [query] = useState<TrayQuery>(() => ({
    tz: localTimeZone(),
    recent_limit: 5,
  }));
  const [visible, setVisible] = useState(false);
  const tray = useTrayQuery(query, visible);
  const refresh = useTrayRefreshMutation(query);
  const visibilitySyncRef = useRef(0);

  async function syncVisibility() {
    const currentSync = ++visibilitySyncRef.current;
    const current = await isTrayVisible();

    // 마운트 조회와 show/hide 조회가 겹쳐도 늦게 끝난 과거 응답이 최신 상태를 덮지 않는다.
    if (currentSync === visibilitySyncRef.current) setVisible(current);
  }

  useEffect(() => {
    void syncVisibility();
    const offShow = Events.On("tray:shown", () => {
      void syncVisibility();
      // 창 열기는 저장된 값을 즉시 읽기만 한다. 진행 중인 조회가 있으면 공유한다.
      void tray.refetch({ cancelRefetch: false });
    });
    const offHide = Events.On("tray:hidden", () => {
      void syncVisibility();
    });

    return () => {
      visibilitySyncRef.current++;
      offShow();
      offHide();
    };
  }, [tray.refetch]);

  const seenErrorAtRef = useRef(0);
  const seenDataAtRef = useRef(0);

  useEffect(() => bindRetry(() => tray.refetch()), [tray.refetch]);

  useEffect(() => {
    if (tray.errorUpdatedAt > seenErrorAtRef.current) {
      seenErrorAtRef.current = tray.errorUpdatedAt;
      noteFailure();
    }
    if (tray.dataUpdatedAt > seenDataAtRef.current) {
      seenDataAtRef.current = tray.dataUpdatedAt;
      noteSuccess();
    }
  }, [tray.refetch, tray.errorUpdatedAt, tray.dataUpdatedAt]);

  const fetching = tray.isFetching;
  const view = tray.data ? toTrayView(tray.data) : null;
  const failure =
    tray.error || refresh.error ? String(tray.error ?? refresh.error) : "";
  const shown = view;
  const notInstalled = shown?.monitoring.state === TrayState.StateNotInstalled;

  return (
    <>
      <div className="bg-bg relative flex h-screen flex-col overflow-hidden animate-[trayIn_180ms_cubic-bezier(0.32,0.72,0,1)]">
        <TrayHeader
          observedAt={tray.data?.limits_observed_at ?? ""}
          trayState={shown?.monitoring.state}
          fetching={fetching}
          disconnected={reconnect.down}
          onRefresh={async () => {
            // 헤더가 이 약속을 기다려 스피너 최소 표시 시간을 맞춘다. 스냅샷은 캐시로 들어가므로
            // (query/tray.ts) 여기서 받을 것이 없다.
            try {
              await refresh.mutateAsync();
            } catch {
              // 오류는 mutation 상태로 표시하고 마지막 정상 데이터를 유지한다.
            }
          }}
        />
        {failure && shown ? (
          <>
            <p role="status" className="text-text-secondary px-4 py-2 text-xs">
              최신 상태를 확인하지 못했습니다. 마지막 조회 결과를 표시합니다.
            </p>
          </>
        ) : null}
        <main className="tray-scroll min-h-0 flex-1 overflow-y-auto">
          {reconnect.down ? (
            <>
              <DaemonDown />
            </>
          ) : (
            <>
              {tray.isPending ? (
                <></>
              ) : (
                <>
                  {!shown ? (
                    <>
                      <EmptyState
                        pose="warning"
                        title="사용량을 불러오지 못했습니다"
                        description={failure}
                      />
                    </>
                  ) : (
                    <>
                      {notInstalled ? (
                        <>
                          <EmptyState
                            pose="no-data"
                            title="아직 수집한 데이터가 없습니다"
                            description="Pulsemetry 를 설치하고 AI 도구를 한 번 쓰면 여기에 사용량이 쌓입니다."
                          />
                        </>
                      ) : (
                        <>
                          <VendorLimits
                            vendors={shown.vendors}
                            unavailable={shown.unavailable}
                          />
                          <RecentSessions sessions={shown.sessions} />
                        </>
                      )}
                    </>
                  )}
                </>
              )}
            </>
          )}
        </main>
        <TrayFooter
          settingsOpen={settingsOpen}
          onSettings={() => setSettingsOpen(!settingsOpen)}
          onRequestQuit={requestQuit}
        />
        {settingsOpen ? (
          <>
            <TraySettingsMenu
              onClose={() => setSettingsOpen(false)}
              onRequestQuit={requestQuit}
            />
          </>
        ) : null}
        <QuitDialog open={quitOpen} onClose={() => setQuitOpen(false)} />
      </div>
    </>
  );
}
