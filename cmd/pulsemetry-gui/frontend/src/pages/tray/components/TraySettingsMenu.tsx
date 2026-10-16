import PowerIcon from "$lib/icons/PowerIcon";
import { openMainSettings } from "$lib/ipc/app";
import { cssStyle } from "$lib/react-utils";
import { Fragment, useState } from "react";
import type { TrayOption, TrayOptionKey } from "../types";

export default function TraySettingsMenu({
  onClose,
  onRequestQuit,
}: {
  onClose: () => void;
  onRequestQuit: () => void;
}) {
  const TRAY_OPTIONS: TrayOption[] = [
    { key: "notify", name: "한도 알림", desc: "20% 아래로 떨어지면 알림" },
    { key: "launch", name: "로그인 시 자동 실행", desc: "" },
  ];
  const [optionOn, setOptionOn] = useState<Record<TrayOptionKey, boolean>>({
    notify: true,
    launch: true,
  });

  function openSettings() {
    onClose();
    openMainSettings();
  }

  return (
    <>
      <button
        type="button"
        aria-label="메뉴 닫기"
        onClick={onClose}
        className="absolute cursor-default border-none [inset:0] [background:rgba(27,26,24,0.10)]"
      />
      <div
        role="menu"
        className="bg-surface absolute border [right:12px] [bottom:56px] w-[244px] [border-color:#dfd8ce] rounded-[12px] [box-shadow:0_10px_28px_rgba(27,26,24,0.16)] p-[6px] animate-[menuIn_160ms_cubic-bezier(0.32,0.72,0,1)]"
      >
        {TRAY_OPTIONS.map((option) => (
          <Fragment key={option.key}>
            <button
              type="button"
              onClick={() =>
                setOptionOn({
                  ...optionOn,
                  [option.key]: !optionOn[option.key],
                })
              }
              className="hover:bg-surface-hover grid w-full cursor-pointer items-center border-none bg-transparent text-left grid-cols-[minmax(0,1fr)_auto] gap-[10px] p-[8px_10px] rounded-[9px]"
            >
              <span className="min-w-0">
                <span className="text-text block truncate font-semibold text-[12.5px]">
                  {option.name}
                </span>
                {option.desc ? (
                  <>
                    <span className="text-text-muted block truncate text-[10.5px] mt-[2px]">
                      {option.desc}
                    </span>
                  </>
                ) : null}
              </span>
              <span
                style={cssStyle(
                  "background:" +
                    String(
                      optionOn[option.key] ? "var(--color-accent)" : "#ddd6cc",
                    ) +
                    ";justify-content:" +
                    String(optionOn[option.key] ? "flex-end" : "flex-start"),
                )}
                className="flex flex-none w-[34px] h-[20px] rounded-[999px] p-[3px] [transition:background_220ms_cubic-bezier(0.32,0.72,0,1)]"
              >
                <span className="block w-[14px] h-[14px] rounded-[50%] [background:#ffffff]" />
              </span>
            </button>
          </Fragment>
        ))}
        <div className="h-[1px] [background:#f1ece4] m-[5px_8px]" />
        <button
          type="button"
          onClick={openSettings}
          className="text-text hover:bg-surface-hover flex w-full cursor-pointer items-center border-none bg-transparent text-left font-semibold whitespace-nowrap gap-[9px] p-[9px_10px] rounded-[9px] text-[12.5px]"
        >
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="#96918a"
            strokeWidth="1.9"
            strokeLinecap="round"
            strokeLinejoin="round"
            className="flex-none w-[13px] h-[13px]"
          >
            <path d="M14 5h5v5" />
            <path d="M19 5 11 13" />
            <path d="M17.5 14v4.5a1.5 1.5 0 0 1-1.5 1.5H6a1.5 1.5 0 0 1-1.5-1.5V8A1.5 1.5 0 0 1 6 6.5h4.5" />
          </svg>{" "}
          전체 설정 열기…
        </button>
        <button
          type="button"
          onClick={onRequestQuit}
          onMouseEnter={(event) =>
            (event.currentTarget.style.background = "#fcf3f3")
          }
          onMouseLeave={(event) =>
            (event.currentTarget.style.background = "transparent")
          }
          className="flex w-full cursor-pointer items-center border-none bg-transparent text-left font-semibold whitespace-nowrap gap-[9px] p-[9px_10px] rounded-[9px] text-[12.5px] text-[var(--color-danger-strong)]"
        >
          <PowerIcon size={13} className="flex-none" /> Pulsemetry 종료
        </button>
      </div>
    </>
  );
}
