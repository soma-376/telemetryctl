import AgentBadge from "$lib/components/ui/AgentBadge";
import Dot from "$lib/components/ui/Dot";
import Mascot from "$lib/components/ui/Mascot";
import Pill from "$lib/components/ui/Pill";
import { AGENT_STYLE } from "$lib/domain/agent";
import CheckIcon from "$lib/icons/CheckIcon";
import ChevronDownIcon from "$lib/icons/ChevronDownIcon";
import RefreshIcon from "$lib/icons/RefreshIcon";
import SlidersIcon from "$lib/icons/SlidersIcon";
import XIcon from "$lib/icons/XIcon";
import { getAppInfo, type AppInfo } from "$lib/ipc/app";
import { useUpdatesQuery } from "$lib/query/updates";
import { cssStyle, useWindowEvent } from "$lib/react-utils";
import { Fragment, useEffect, useState } from "react";
import DaemonUpdates from "./DaemonUpdates";
import {
  COLLECTION,
  CONNECTIONS,
  CONN_STATUS,
  HEALTH,
  POLICY,
  PREFS,
  PREF_DEFAULTS,
  TRANSPORT,
} from "./mock";

export default function SettingsModal({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const updates = useUpdatesQuery(open);
  const [toggles, setToggles] = useState(PREF_DEFAULTS);
  const [appInfo, setAppInfo] = useState<AppInfo>({
    name: "Pulsemetry",
    version: "",
  });

  useEffect(() => {
    let active = true;

    void getAppInfo().then((i) => {
      if (active) setAppInfo(i);
    });

    return () => {
      active = false;
    };
  }, []);

  const line = (i: number, n: number) =>
    i === n - 1 ? "transparent" : "#f5f1ea";

  useWindowEvent("keydown", (e) => {
    if (open && e.key === "Escape") {
      e.preventDefault();
      onClose();
    }
  });

  return (
    <>
      {open ? (
        <>
          <div className="fixed inset-0 flex items-center justify-center z-[60]">
            <button
              type="button"
              aria-label="닫기"
              onClick={onClose}
              className="absolute inset-0 cursor-default border-none [background:rgba(27,26,24,0.22)] animate-[fadeIn_160ms_ease-out]"
            />
            <div className="bg-surface border-border relative flex flex-col border w-[560px] max-w-[calc(100vw_-_64px)] max-h-[calc(100vh_-_96px)] rounded-[16px] [box-shadow:0_18px_48px_rgba(27,26,24,0.16)] animate-[popIn_200ms_cubic-bezier(0.32,0.72,0,1)]">
              <div className="border-track flex flex-none items-center border-b gap-[12px] p-[18px_22px_14px]">
                <span className="bg-accent-soft text-accent flex flex-none items-center justify-center w-[30px] h-[30px] rounded-[9px]">
                  <SlidersIcon
                    size={16}
                    strokeWidth={1.8}
                    knobFill="var(--color-accent-soft)"
                  />
                </span>
                <span className="text-text font-bold text-[16px] tracking-[-0.01em] flex-1">
                  설정
                </span>
                <button
                  type="button"
                  aria-label="닫기"
                  onClick={onClose}
                  className="border-border text-text-secondary hover:border-border-strong flex flex-none cursor-pointer items-center justify-center border bg-transparent transition-colors duration-[120ms] ease-in-out w-[28px] h-[28px] rounded-[9px]"
                >
                  <XIcon size={13} />
                </button>
              </div>
              <div className="overflow-y-auto flex-1 p-[16px_22px_20px]">
                <div className="bg-surface-hover [border:1px_solid_#efe9e1] rounded-[12px] p-[14px_16px] mb-[20px]">
                  <div className="flex items-center gap-[12px] mb-[14px]">
                    <Mascot pose="found" height={44} />
                    <div className="min-w-0 flex-1">
                      <div className="text-text font-bold whitespace-nowrap text-[13.5px] mb-[3px]">
                        모든 연결이 정상이에요
                      </div>
                      <div className="text-text-muted whitespace-nowrap text-[11.5px]">
                        마지막 확인 2분 전
                      </div>
                    </div>
                    <button
                      type="button"
                      className="bg-surface border-border text-text hover:border-border-strong flex flex-none cursor-pointer items-center border whitespace-nowrap transition-colors duration-[120ms] ease-in-out gap-[7px] rounded-[9px] p-[8px_12px] text-[12px]"
                    >
                      <RefreshIcon size={13} className="text-text-secondary" />{" "}
                      상태 새로고침
                    </button>
                  </div>
                  <div className="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-[0_20px] [border-top:1px_solid_#efe9e1] pt-[4px]">
                    {HEALTH.map((h) => (
                      <Fragment key={h.name}>
                        <span className="grid items-center grid-cols-[7px_minmax(0,1fr)_auto] gap-[9px] p-[6px_0]">
                          <Dot color="var(--color-success)" />
                          <span className="text-text truncate font-semibold text-[12px]">
                            {h.name}
                          </span>
                          <span className="text-text-muted whitespace-nowrap text-[11.5px]">
                            {h.state}
                          </span>
                        </span>
                      </Fragment>
                    ))}
                  </div>
                </div>
                <div className="text-text-muted font-semibold text-[11.5px] tracking-[0.02em] mb-[2px]">
                  설정
                </div>
                {PREFS.map((p, i) => (
                  <Fragment key={p.key}>
                    <div
                      style={cssStyle(
                        "border-bottom:1px solid " +
                          String(line(i, PREFS.length)),
                      )}
                      className="grid items-center grid-cols-[32px_minmax(0,1fr)_auto] gap-[12px] p-[13px_0]"
                    >
                      <span className="text-accent flex items-center justify-center w-[32px] h-[32px] rounded-[10px] [background:#f4f0e9] text-[14px]">
                        {p.icon}
                      </span>
                      <span className="min-w-0">
                        <span className="text-text block truncate font-semibold text-[13.5px] mb-[3px]">
                          {p.name}
                        </span>
                        <span className="text-text-muted block truncate text-[11.5px]">
                          {p.desc}
                        </span>
                        {p.dbPath ? (
                          <>
                            <span className="flex items-center gap-[8px] mt-[6px] min-w-0">
                              <span className="truncate [font-family:var(--font-mono)] text-[10.5px] text-[#a8a29a] min-w-0 [direction:rtl] [text-align:left]">
                                <bdi>{p.dbPath}</bdi>
                              </span>
                              <span className="flex-none text-[10.5px] text-[#a8a29a]">
                                · {p.dbSize}
                              </span>
                            </span>
                          </>
                        ) : null}
                      </span>
                      {p.kind === "toggle" ? (
                        <>
                          <button
                            type="button"
                            role="switch"
                            aria-checked={toggles[p.key]}
                            aria-label={p.name}
                            onClick={() =>
                              setToggles({
                                ...toggles,
                                [p.key]: !toggles[p.key],
                              })
                            }
                            style={cssStyle(
                              "background:" +
                                String(
                                  toggles[p.key]
                                    ? "var(--color-accent)"
                                    : "#ddd6cc",
                                ),
                            )}
                            className="flex flex-none cursor-pointer items-center border-none w-[40px] h-[23px] rounded-[999px] p-[3px] [transition:background_260ms_cubic-bezier(0.32,0.72,0,1)]"
                          >
                            <span
                              style={cssStyle(
                                "transform:translateX(" +
                                  String(toggles[p.key] ? "17px" : "0px") +
                                  ")",
                              )}
                              className="block w-[17px] h-[17px] rounded-[50%] [background:#fff] [box-shadow:0_1px_2px_rgba(27,26,24,0.2)] [transition:transform_260ms_cubic-bezier(0.32,0.72,0,1)]"
                            />
                          </button>
                        </>
                      ) : (
                        <>
                          <button
                            type="button"
                            className="border-border text-text hover:border-border-strong flex flex-none cursor-pointer items-center border bg-transparent whitespace-nowrap transition-colors duration-[120ms] ease-in-out gap-[9px] rounded-[9px] p-[7px_11px] text-[12.5px]"
                          >
                            {p.value}
                            <ChevronDownIcon
                              size={12}
                              strokeWidth={2.4}
                              className="text-text-muted"
                            />
                          </button>
                        </>
                      )}
                    </div>
                  </Fragment>
                ))}
                <DaemonUpdates
                  snapshot={updates.data}
                  unavailable={updates.isError}
                />
                <div className="text-text-muted font-semibold text-[11.5px] tracking-[0.02em] m-[22px_0_2px]">
                  연결 상태
                </div>
                {CONNECTIONS.map((c, i) => (
                  <Fragment key={c.id}>
                    {(() => {
                      const st = CONN_STATUS[c.state];

                      return (
                        <>
                          <div
                            style={cssStyle(
                              "border-bottom:1px solid " +
                                String(line(i, CONNECTIONS.length)),
                            )}
                            className="grid items-center grid-cols-[32px_minmax(0,1fr)_auto] gap-[12px] p-[12px_0]"
                          >
                            <AgentBadge agent={c.id} size={32} />
                            <span className="min-w-0">
                              <span
                                style={cssStyle(
                                  "color:" +
                                    String(
                                      c.state === "off"
                                        ? "var(--color-text-secondary)"
                                        : "var(--color-text)",
                                    ),
                                )}
                                className="block truncate font-semibold text-[13.5px] mb-[3px]"
                              >
                                {AGENT_STYLE[c.id].name}
                              </span>
                              <span className="text-text-muted block truncate text-[11.5px]">
                                {c.seen}
                              </span>
                            </span>
                            <span className="flex flex-none items-center gap-[9px]">
                              {st.action ? (
                                <>
                                  <button
                                    type="button"
                                    className="text-accent hover:text-accent-hover cursor-pointer border-none bg-transparent font-semibold whitespace-nowrap text-[12px]"
                                  >
                                    연결
                                  </button>
                                </>
                              ) : null}
                              <Pill
                                label={st.label}
                                fg={st.fg}
                                bg={st.bg}
                                fontSize={11.5}
                                padding="5px 10px"
                              />
                            </span>
                          </div>
                        </>
                      );
                    })()}
                  </Fragment>
                ))}
                <div className="text-text-muted font-semibold text-[11.5px] tracking-[0.02em] m-[22px_0_8px]">
                  보안 및 데이터 수집
                </div>
                <div className="flex items-start gap-[10px] [background:#fdf6ec] [border:1px_solid_#efdfc4] rounded-[11px] p-[11px_14px] mb-[14px]">
                  <svg
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="#8b6b36"
                    strokeWidth="1.9"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    className="flex-none w-[15px] h-[15px] mt-[2px]"
                  >
                    <path d="M12 4.5 21 19.5H3z" />
                    <path d="M12 10v4" />
                    <circle cx="12" cy="16.8" r="0.9" fill="#8b6b36" />
                  </svg>
                  <div className="text-[12px] leading-[1.6] text-[#5d5852]">
                    아래 항목은{" "}
                    <strong className="text-text font-semibold">
                      조직의 중앙 서버로 전송
                    </strong>
                    됩니다. 조직 정책으로 관리되며 이 기기에서 변경할 수 없어요.
                  </div>
                </div>
                {COLLECTION.map((c, i) => (
                  <Fragment key={c.key}>
                    <div
                      style={cssStyle(
                        "border-bottom:1px solid " +
                          String(line(i, COLLECTION.length)),
                      )}
                      className="grid items-center grid-cols-[26px_minmax(0,1fr)_auto] gap-[11px] p-[9px_0]"
                    >
                      <span
                        style={cssStyle(
                          "background:" +
                            String(
                              c.sent ? "#f4f0e9" : "var(--color-inactive-soft)",
                            ) +
                            ";color:" +
                            String(c.sent ? "var(--color-accent)" : "#a8a29a"),
                        )}
                        className="flex items-center justify-center w-[26px] h-[26px] rounded-[8px] text-[12px]"
                      >
                        {c.icon}
                      </span>
                      <span className="min-w-0">
                        <span className="text-text block truncate font-semibold text-[12.5px] mb-[3px]">
                          {c.label}
                        </span>
                        <span className="block truncate [font-family:var(--font-mono)] text-[10.5px] text-[#a8a29a]">
                          {c.key}
                        </span>
                      </span>
                      <span
                        style={cssStyle(
                          "color:" +
                            String(
                              c.sent
                                ? "var(--color-accent)"
                                : "var(--color-inactive)",
                            ) +
                            ";background:" +
                            String(
                              c.sent
                                ? "var(--color-accent-soft)"
                                : "var(--color-inactive-soft)",
                            ),
                        )}
                        className="inline-flex items-center font-semibold whitespace-nowrap justify-self-end gap-[6px] text-[11px] rounded-[7px] p-[5px_9px]"
                      >
                        {c.sent ? (
                          <>
                            <CheckIcon
                              size={10}
                              strokeWidth={2.8}
                              className="flex-none"
                            />
                          </>
                        ) : (
                          <>
                            <XIcon
                              size={10}
                              strokeWidth={2.8}
                              className="flex-none"
                            />
                          </>
                        )}
                        {c.sent ? "전송" : "제외"}
                      </span>
                    </div>
                  </Fragment>
                ))}
                <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-[10px] mt-[14px]">
                  <div className="bg-surface-hover [border:1px_solid_#efe9e1] rounded-[11px] p-[12px_14px]">
                    <div className="text-text-muted text-[11px] mb-[7px]">
                      전송 대상
                    </div>
                    <div className="text-text [font-family:var(--font-mono)] text-[11.5px] mb-[9px] [overflow-wrap:anywhere]">
                      {TRANSPORT.target}
                    </div>
                    <div className="text-text-secondary flex items-center gap-[7px] text-[11.5px]">
                      <Dot size={6} color="var(--color-success)" />
                      {TRANSPORT.status}
                    </div>
                  </div>
                  <div className="bg-surface-hover [border:1px_solid_#efe9e1] rounded-[11px] p-[12px_14px]">
                    <div className="text-text-muted text-[11px] mb-[7px]">
                      정책 출처
                    </div>
                    <div className="text-text truncate font-semibold text-[12.5px] mb-[6px]">
                      {POLICY.org}
                    </div>
                    <div className="text-text-muted whitespace-pre-line text-[11.5px] leading-[1.5]">
                      {POLICY.detail}
                    </div>
                  </div>
                </div>
                <div className="text-text-muted text-[11.5px] leading-[1.6] mt-[14px]">
                  수집 항목 변경은 조직 관리자에게 문의하세요.
                </div>
              </div>
              <div className="border-track bg-surface-hover flex flex-none items-center border-t gap-[12px] p-[12px_22px] rounded-[0_0_16px_16px]">
                <span className="text-text-muted truncate text-[12px] min-w-0">
                  {appInfo.name}
                  {appInfo.version}
                </span>
              </div>
            </div>
          </div>
        </>
      ) : null}
    </>
  );
}
