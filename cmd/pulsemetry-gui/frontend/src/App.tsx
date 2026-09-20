import QuitDialog from "$lib/components/dialog/QuitDialog";
import Header from "$lib/components/shell/Header";
import Nav from "$lib/components/shell/Nav";
import { usePeriod } from "$lib/domain/period";
import type { AppSection } from "$lib/navigation";
import { makeQueryClient } from "$lib/query/client";
import { cssStyle, useWindowEvent } from "$lib/react-utils";
import { QueryClientProvider } from "@tanstack/react-query";
import { Events } from "@wailsio/runtime";
import { useCallback, useEffect, useState } from "react";
import Activity from "./pages/activity/Activity";
import Home from "./pages/home/Home";
import SettingsModal from "./pages/settings/SettingsModal";
import TrayQuickView from "./pages/tray/TrayQuickView";

export default function App() {
  const { value: selectedPeriod } = usePeriod();
  const isTray =
    new URLSearchParams(window.location.search).get("view") === "tray";
  const [queryClient] = useState(makeQueryClient);
  const [activeTab, setActiveTab] = useState<AppSection>("overview");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [quitOpen, setQuitOpen] = useState(false);
  const go = (tab: AppSection) => setActiveTab(tab);
  const FADE = 18;
  const [scroller, setScroller] = useState<HTMLDivElement | null>(null);
  const [fadeTop, setFadeTop] = useState(false);
  const [fadeBottom, setFadeBottom] = useState(false);
  const syncFade = useCallback(() => {
    const el = scroller;

    if (!el) return;
    setFadeTop(el.scrollTop > 0);
    // 화면 배율에 따라 바닥에서 1px 미만 오차가 남으므로 여유를 둔다.
    setFadeBottom(el.scrollTop + el.clientHeight < el.scrollHeight - 1);
  }, [scroller]);

  useEffect(() => {
    syncFade();
    if (!scroller) return;
    const observer = new ResizeObserver(syncFade);

    observer.observe(scroller);
    if (scroller.firstElementChild)
      observer.observe(scroller.firstElementChild);

    return () => observer.disconnect();
  }, [activeTab, selectedPeriod, scroller, syncFade]);

  const maskStyle = (() => {
    if (!fadeTop && !fadeBottom) return "";
    const head = fadeTop ? `transparent 0, #000 ${FADE}px` : "#000 0";
    const tail = fadeBottom
      ? `#000 calc(100% - ${FADE}px), transparent 100%`
      : "#000 100%";
    const gradient = `linear-gradient(to bottom, ${head}, ${tail})`;

    return `-webkit-mask-image:${gradient};mask-image:${gradient}`;
  })();

  useEffect(() => {
    if (!isTray) {
      try {
        return Events.On("open-settings", () => setSettingsOpen(true));
      } catch {
        /* 브라우저 미리보기 */
      }
    }
  }, [isTray]);

  useWindowEvent("resize", syncFade);

  return (
    <>
      <QueryClientProvider client={queryClient}>
        {isTray ? (
          <>
            <TrayQuickView />
          </>
        ) : (
          <>
            <div className="flex h-screen min-w-(--page-min-width) flex-col overflow-hidden bg-bg">
              <Header
                onOpenSettings={() => setSettingsOpen(true)}
                onQuit={() => setQuitOpen(true)}
              />
              <div
                ref={setScroller}
                onScroll={syncFade}
                style={cssStyle(maskStyle)}
                className="flex flex-1 flex-col overflow-y-auto"
              >
                {activeTab === "overview" ? (
                  <>
                    <Home onNavigate={go} />
                  </>
                ) : (
                  <>
                    {activeTab === "activity" ? (
                      <>
                        <Activity />
                      </>
                    ) : (
                      <>
                        <main className="flex flex-1 flex-col items-center justify-center gap-2">
                          <div className="text-text font-bold text-[25px] tracking-[-0.02em]">
                            Insights
                          </div>
                          <div className="text-text-secondary text-[13.5px]">
                            아직 준비 중인 화면이야.
                          </div>
                        </main>
                      </>
                    )}
                  </>
                )}
              </div>
              <Nav active={activeTab} onSelect={go} />
            </div>
            <SettingsModal
              open={settingsOpen}
              onClose={() => setSettingsOpen(false)}
            />
            <QuitDialog open={quitOpen} onClose={() => setQuitOpen(false)} />
          </>
        )}
      </QueryClientProvider>
    </>
  );
}
