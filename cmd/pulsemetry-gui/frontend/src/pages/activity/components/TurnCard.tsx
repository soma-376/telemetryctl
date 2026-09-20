import ChevronDownIcon from "$lib/icons/ChevronDownIcon";
import { cssStyle } from "$lib/react-utils";
import { Fragment } from "react";
import type { TurnDisplay } from "../types";

export default function TurnCard({
  turn,
  open,
  sel,
  onToggle,
}: {
  turn: TurnDisplay;
  open: boolean;
  sel: boolean;
  onToggle?: () => void;
}) {
  function copyPrompt() {
    navigator.clipboard?.writeText(turn.prompt);
  }

  return (
    <>
      <div
        style={cssStyle(
          "border-color:" +
            String(sel ? "var(--color-border-strong)" : "#efe9e1") +
            ";background:" +
            String(sel ? "var(--color-surface-hover)" : "var(--color-surface)"),
        )}
        className="border rounded-[12px] p-[12px_14px] mb-[8px]"
      >
        <button
          type="button"
          aria-expanded={open}
          onClick={() => onToggle?.()}
          className="grid w-full cursor-pointer items-center text-left grid-cols-[26px_44px_60px_minmax(0,1fr)_auto_14px] gap-[11px]"
        >
          <span
            style={cssStyle(
              "background:" +
                String(sel ? turn.labelDot : "#f4f0e9") +
                ";color:" +
                String(
                  sel ? "var(--color-surface)" : "var(--color-accent-hover)",
                ),
            )}
            className="flex items-center justify-center font-bold w-[26px] h-[26px] rounded-[8px] text-[11.5px] tabular-nums"
          >
            {turn.n}
          </span>
          <span className="text-text-secondary whitespace-nowrap text-[12px] tabular-nums">
            {turn.time}
          </span>
          <span
            style={cssStyle(
              "color:" +
                String(turn.labelFg) +
                ";background:" +
                String(turn.labelBg) +
                ";border-color:" +
                String(turn.labelBorder),
            )}
            className="inline-flex items-center justify-center border font-semibold whitespace-nowrap gap-[5px] text-[11px] rounded-[6px] p-[4px_0]"
          >
            <span
              style={cssStyle("background:" + String(turn.labelDot))}
              className="flex-none w-[6px] h-[6px] rounded-[2px]"
            />
            {turn.label}
          </span>
          <span className="text-text min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-[13px]">
            {turn.preview}
          </span>
          <span className="text-text-muted flex-none whitespace-nowrap text-[11px] tabular-nums">
            {turn.meta}
          </span>
          <ChevronDownIcon
            size={12}
            strokeWidth={2.4}
            rotated={open}
            className="text-text-muted flex-none"
          />
        </button>
        {open ? (
          <>
            <div className="animate-[rowIn_180ms_ease-out]">
              <div
                style={cssStyle(
                  "border-left:3px solid " + String(turn.labelFg),
                )}
                className="bg-surface mt-[12px] p-[11px_13px] [border:1px_solid_#efe9e1] rounded-[10px]"
              >
                <div className="flex items-baseline gap-[8px] mb-[7px]">
                  <span className="text-text-muted font-semibold text-[10.5px] tracking-[0.02em]">
                    보낸 프롬프트 원문
                  </span>
                  <span className="flex-1" />
                  <span className="text-text-muted whitespace-nowrap text-[10.5px]">
                    {turn.chars}
                  </span>
                  <button
                    type="button"
                    onClick={copyPrompt}
                    className="cursor-pointer border-none bg-transparent font-semibold whitespace-nowrap text-[10.5px] text-[var(--color-accent)]"
                  >
                    복사
                  </button>
                </div>
                <div className="text-text text-[13px] leading-[1.75] [white-space:pre-wrap] [word-break:break-word]">
                  {turn.prompt}
                </div>
              </div>
              <div className="grid grid-cols-[repeat(4,minmax(0,1fr))] gap-[8px] mt-[10px]">
                {turn.stats.map((stat) => (
                  <Fragment key={stat.name}>
                    <div className="bg-surface-hover [border:1px_solid_#efe9e1] rounded-[10px] p-[9px_11px]">
                      <div
                        style={cssStyle("color:" + String(stat.fg))}
                        className="text-[14px] font-bold tabular-nums mb-[3px]"
                      >
                        {stat.value}
                      </div>
                      <div className="text-text-muted whitespace-nowrap text-[10.5px]">
                        {stat.name}
                      </div>
                    </div>
                  </Fragment>
                ))}
              </div>
              <div className="bg-surface-hover mt-[10px] [border:1px_solid_#efe9e1] rounded-[10px] p-[11px_13px]">
                <div className="flex items-baseline gap-[8px] mb-[9px]">
                  <span className="text-text-muted font-semibold text-[10.5px] tracking-[0.02em]">
                    도구 호출
                  </span>
                  <span className="flex-1" />
                  <span className="text-text-muted whitespace-nowrap text-[10.5px]">
                    {turn.callNote}
                  </span>
                </div>
                {turn.calls.map((call, i) => (
                  <Fragment key={call.id ?? i}>
                    <div className="grid items-center grid-cols-[42px_62px_minmax(0,1fr)_46px_14px] gap-[10px] p-[5px_0]">
                      <span className="text-text-muted whitespace-nowrap text-[11px] tabular-nums">
                        {call.time}
                      </span>
                      <span className="text-text-secondary overflow-hidden font-semibold text-ellipsis whitespace-nowrap text-[11px]">
                        {call.tool}
                      </span>
                      <span className="text-text min-w-0 overflow-hidden text-ellipsis whitespace-nowrap [font-family:var(--font-mono)] text-[11px]">
                        {call.arg}
                      </span>
                      <span className="text-text-muted whitespace-nowrap text-[11px] tabular-nums [text-align:right]">
                        {call.dur}
                      </span>
                      <span
                        style={cssStyle(
                          "background:" +
                            String(
                              call.ok === null
                                ? "var(--color-surface-hover)"
                                : call.ok
                                  ? "var(--color-success-soft)"
                                  : "var(--color-danger-soft)",
                            ) +
                            ";color:" +
                            String(
                              call.ok === null
                                ? "var(--color-text-muted)"
                                : call.ok
                                  ? "#2f7e55"
                                  : "var(--color-danger-strong)",
                            ),
                        )}
                        className="flex items-center justify-center font-bold w-[14px] h-[14px] rounded-[4px] text-[9px]"
                      >
                        {call.ok === null ? "?" : call.ok ? "✓" : "✕"}
                      </span>
                    </div>
                  </Fragment>
                ))}
                <div className="text-text-muted text-[10.5px] mt-[8px] pt-[8px] [border-top:1px_solid_#f1ece4] leading-[1.6]">
                  수집된 도구 이름·대상·소요 시간·성공 여부를 표시합니다.
                </div>
              </div>
            </div>
          </>
        ) : null}
      </div>
    </>
  );
}
