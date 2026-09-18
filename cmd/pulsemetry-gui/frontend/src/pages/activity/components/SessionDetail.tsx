import AgentBadge from "$lib/components/ui/AgentBadge";
import Pill from "$lib/components/ui/Pill";
import ChevronLeftIcon from "$lib/icons/ChevronLeftIcon";
import ChevronRightIcon from "$lib/icons/ChevronRightIcon";
import XIcon from "$lib/icons/XIcon";
import { cssStyle, Portal, usePresence } from "$lib/react-utils";
import { Fragment, useEffect, useState } from "react";
import { detailDisplay } from "../model";
import type { ActivitySession } from "../types";
import FileChanges from "./FileChanges";
import KpiGrid from "./KpiGrid";
import TurnCard from "./TurnCard";
import TurnFlow from "./TurnFlow";

export default function SessionDetail({
  open = false,
  session,
  position,
  onClose,
  onPrev,
  onNext,
  loading = false,
  error = "",
  onRetry,
}: {
  loading?: boolean;
  error?: string;
  onRetry?: () => void;
  open?: boolean;
  session: ActivitySession | null;
  position: string;
  onClose?: () => void;
  onPrev?: () => void;
  onNext?: () => void;
}) {
  const present = usePresence(open, 280);
  const [turnOpen, setTurnOpen] = useState<Record<number, boolean>>({});
  const [turnSel, setTurnSel] = useState<number | null>(null);
  const [filesOpen, setFilesOpen] = useState(false);
  const [last, setLast] = useState<ActivitySession | null>(null);
  const [lastPosition, setLastPosition] = useState("");

  useEffect(() => {
    if (!session) return;
    const changed = last?.id !== session.id;

    setLast(session);
    setLastPosition(position);
    // 세션이 바뀌면 턴 펼침·선택·파일 목록 상태를 초기화한다.
    if (changed) {
      setTurnOpen({});
      setTurnSel(null);
      setFilesOpen(false);
    }
  }, [session, position, last?.id]);

  const shown = session ?? last;
  const d = shown ? detailDisplay(shown, position || lastPosition) : null;
  const openCount = Object.values(turnOpen).filter(Boolean).length;
  const collapseLabel = openCount ? "모두 접기" : "모두 펼치기";

  function toggleTurn(k: number) {
    const next = !turnOpen[k];

    setTurnOpen({ ...turnOpen, [k]: next });
    if (next) setTurnSel(k);
  }

  function pickTurn(k: number) {
    setTurnSel(k);
    setTurnOpen({ ...turnOpen, [k]: true });
  }

  function collapseAll() {
    if (openCount) {
      setTurnOpen({});

      return;
    }
    const all: Record<number, boolean> = {};

    d?.turns.forEach((t) => (all[t.n] = true));
    setTurnOpen(all);
  }

  return (
    <>
      {present && d ? (
        <>
          <Portal>
            <div
              data-open={open}
              className="session-overlay fixed inset-0 z-[60]"
            >
              <button
                type="button"
                aria-label="닫기"
                onClick={() => onClose?.()}
                className="absolute inset-0 cursor-default border-none [background:rgba(27,26,24,0.28)]"
              />
              <div className="session-sheet bg-surface absolute top-0 right-0 bottom-0 flex flex-col w-[min(720px,70vw)] [border-left:1px_solid_var(--color-border)] [box-shadow:-18px_0_44px_rgba(27,26,24,0.13)]">
                <div className="flex-none p-[20px_24px_16px] [border-bottom:1px_solid_#f1ece4]">
                  <div className="flex items-start gap-[14px] mb-[14px]">
                    <AgentBadge agent={d.agentId} size={40} />
                    <div className="min-w-0 flex-1">
                      <div className="text-text overflow-hidden font-bold text-ellipsis whitespace-nowrap text-[21px] tracking-[-0.02em] mb-[7px]">
                        {d.title}
                      </div>
                      <div className="flex items-baseline [font-family:var(--font-mono)] text-[12.5px] mb-[8px]">
                        <span className="text-text flex-none">{d.repo}</span>
                        <span className="text-text-muted flex-none p-[0_5px]">
                          /
                        </span>
                        <span className="text-text-muted min-w-0 overflow-hidden text-ellipsis whitespace-nowrap [flex:0_1_auto] [direction:rtl] [text-align:left]">
                          <bdi>{d.path}</bdi>
                        </span>
                      </div>
                      <div className="text-text-muted flex items-center whitespace-nowrap gap-[9px] text-[12.5px]">
                        <span className="text-text-secondary flex-none font-medium">
                          {d.agentName}
                        </span>
                        <Pill
                          label={d.badge.label}
                          fg={d.badge.fg}
                          bg={d.badge.bg}
                          fontSize={11.5}
                          padding="4px 8px"
                          className="flex-none"
                        />
                        <span
                          style={cssStyle(
                            "color:" +
                              String(d.character.fg) +
                              ";background:" +
                              String(d.character.bg) +
                              ";border-color:" +
                              String(d.character.border),
                          )}
                          className="inline-flex flex-none items-center border font-semibold gap-[6px] text-[11.5px] rounded-[7px] p-[4px_9px]"
                        >
                          <span
                            style={cssStyle(
                              "background:" + String(d.character.dot),
                            )}
                            className="flex-none w-[7px] h-[7px] rounded-[2px]"
                          />
                          {d.character.label}
                        </span>
                        <span className="overflow-hidden text-ellipsis">
                          {d.range}
                        </span>
                      </div>
                    </div>
                    <button
                      type="button"
                      aria-label="닫기"
                      onClick={() => onClose?.()}
                      className="border-border text-text-secondary hover:border-border-strong flex flex-none cursor-pointer items-center justify-center border bg-transparent w-[30px] h-[30px] rounded-[9px]"
                    >
                      <XIcon />
                    </button>
                  </div>
                </div>
                <div className="flex min-h-0 flex-1 flex-col overflow-y-auto p-[18px_24px_22px] gap-[14px]">
                  {loading ? (
                    <>
                      <p role="status">세션 상세를 불러오는 중입니다.</p>
                    </>
                  ) : (
                    <>
                      {error && d.kpi.length === 0 ? (
                        <>
                          <div role="alert">
                            {error}
                            <button onClick={() => onRetry?.()}>
                              다시 시도
                            </button>
                          </div>
                        </>
                      ) : (
                        <>
                          {error ? (
                            <>
                              <p role="alert">
                                {error} 마지막 조회 결과를 표시합니다.{" "}
                                <button onClick={() => onRetry?.()}>
                                  다시 시도
                                </button>
                              </p>
                            </>
                          ) : null}
                          {shown?.notice ? (
                            <>
                              <p role="status">{shown.notice}</p>
                            </>
                          ) : null}
                          <KpiGrid kpi={d.kpi} />
                          <TurnFlow
                            turnCount={d.turnCount}
                            segments={d.segments}
                            legend={d.legend}
                            selected={turnSel}
                            onPick={pickTurn}
                          />
                          <div>
                            <div className="flex items-baseline gap-[9px] mb-[9px]">
                              <span className="text-text-secondary font-semibold text-[12.5px]">
                                턴별 프롬프트
                              </span>
                              <span className="flex-1" />
                              <button
                                type="button"
                                onClick={collapseAll}
                                className="text-text-secondary hover:text-text cursor-pointer border-none bg-transparent font-semibold whitespace-nowrap text-[11.5px]"
                              >
                                {collapseLabel}
                              </button>
                            </div>
                            {d.turns.map((turn) => (
                              <Fragment key={turn.n}>
                                <TurnCard
                                  turn={turn}
                                  open={!!turnOpen[turn.n]}
                                  sel={turnSel === turn.n}
                                  onToggle={() => toggleTurn(turn.n)}
                                />
                              </Fragment>
                            ))}
                          </div>
                          <FileChanges
                            files={d.files}
                            open={filesOpen}
                            onToggle={() => setFilesOpen(!filesOpen)}
                          />
                        </>
                      )}
                    </>
                  )}
                </div>
                <div className="bg-surface flex flex-none items-center gap-[10px] p-[14px_24px] [border-top:1px_solid_#f1ece4]">
                  <button
                    type="button"
                    onClick={() => onPrev?.()}
                    className="bg-surface border-border text-text hover:border-border-strong flex cursor-pointer items-center border font-semibold whitespace-nowrap gap-[8px] text-[13px] p-[10px_15px] rounded-[9px]"
                  >
                    <ChevronLeftIcon
                      size={13}
                      strokeWidth={2.2}
                      className="text-text-secondary"
                    />{" "}
                    이전
                  </button>
                  <button
                    type="button"
                    onClick={() => onNext?.()}
                    className="bg-surface border-border text-text hover:border-border-strong flex cursor-pointer items-center border font-semibold whitespace-nowrap gap-[8px] text-[13px] p-[10px_15px] rounded-[9px]"
                  >
                    다음{" "}
                    <ChevronRightIcon
                      size={13}
                      strokeWidth={2.2}
                      className="text-text-secondary"
                    />
                  </button>
                  <span className="text-text-muted text-[12.5px] tabular-nums">
                    {d.position}
                  </span>
                  <span className="flex-1" />
                  <button
                    type="button"
                    onClick={collapseAll}
                    className="text-text-secondary hover:text-text cursor-pointer border-none bg-transparent font-semibold whitespace-nowrap text-[12px] mr-[4px]"
                  >
                    {collapseLabel}
                  </button>
                  <span className="text-text-muted flex items-center whitespace-nowrap gap-[7px] text-[11.5px]">
                    <span className="border-border border [font-family:var(--font-mono)] rounded-[5px] p-[3px_6px]">
                      J
                    </span>
                    <span className="border-border border [font-family:var(--font-mono)] rounded-[5px] p-[3px_6px]">
                      K
                    </span>{" "}
                    이동{" "}
                    <span className="border-border border [font-family:var(--font-mono)] rounded-[5px] p-[3px_6px] ml-[6px]">
                      Esc
                    </span>{" "}
                    닫기
                  </span>
                </div>
              </div>
            </div>
          </Portal>
        </>
      ) : null}
    </>
  );
}
