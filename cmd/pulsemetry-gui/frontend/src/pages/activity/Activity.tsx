import type { ActivityQuery } from "$lib/bindings";
import Input from "$lib/components/ui/Input";
import Select from "$lib/components/ui/Select";
import {
  addDays,
  periodRangeText,
  toDate,
  usePeriod,
} from "$lib/domain/period";
import RefreshIcon from "$lib/icons/RefreshIcon";
import { useActivityDetailQuery, useActivityQuery } from "$lib/query/activity";
import { cssStyle, useWindowEvent } from "$lib/react-utils";
import { Events } from "@wailsio/runtime";
import { useEffect, useMemo, useState } from "react";
import { sessionDetail, sessionRow } from "./adapter";
import SessionDetail from "./components/SessionDetail";
import SessionTable from "./components/SessionTable";

export default function Activity() {
  const { value: selectedPeriod } = usePeriod();
  const [visible, setVisible] = useState(true);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [vendor, setVendor] = useState("");
  const [status, setStatus] = useState("");
  const [project, setProject] = useState("");
  const [search, setSearch] = useState("");
  const [text, setText] = useState("");
  const [projectPath, setProjectPath] = useState("");

  useEffect(() => {
    const value = search.trim();
    const path = project.trim();
    const timer = setTimeout(() => {
      setText(value);
      setProjectPath(path);
    }, 300);

    return () => clearTimeout(timer);
  }, [search, project]);

  const request: ActivityQuery = useMemo(
    () => ({
      since: Math.floor(toDate(selectedPeriod.start).getTime() / 1000),
      until: Math.floor(
        addDays(toDate(selectedPeriod.end), 1).getTime() / 1000,
      ),
      vendors: vendor ? [vendor] : [],
      projects: projectPath ? [projectPath] : [],
      status: status ? [status] : [],
      text,
      limit: 50,
      cursor: { id: 0, running: false, sort_at: 0 },
    }),
    [
      selectedPeriod.start,
      selectedPeriod.end,
      vendor,
      projectPath,
      status,
      text,
    ],
  );
  const list = useActivityQuery(request, visible);
  const detail = useActivityDetailQuery(selectedId, visible);
  const { refetch: refetchList } = list;
  const { refetch: refetchDetail } = detail;
  const sessions = (() => {
    // 페이지 사이에 세션이 마감돼 다시 나타나도 같은 행을 두 번 렌더링하지 않는다.
    const seen = new Set<number>();

    return (list.data?.pages ?? [])
      .flatMap((page) => page.rows ?? [])
      .filter((row) => {
        if (seen.has(row.id)) return false;
        seen.add(row.id);

        return true;
      })
      .map(sessionRow);
  })();
  const selectedIndex = sessions.findIndex((s) => Number(s.id) === selectedId);
  const selected =
    selectedIndex >= 0 ? (sessions[selectedIndex] ?? null) : null;
  const shown =
    detail.data?.detail.found && detail.data.detail.session.id === selectedId
      ? sessionDetail(detail.data)
      : selected;
  const detailError = detail.isError
    ? "상세를 갱신하지 못했습니다."
    : detail.data && !detail.data.detail.found
      ? "세션이 삭제되었거나 더 이상 존재하지 않습니다."
      : "";

  useEffect(() => {
    setSelectedId(null);
  }, [request]);

  function step(dir: number) {
    if (!sessions.length) return;
    const next =
      sessions[(selectedIndex + dir + sessions.length) % sessions.length];

    if (next) setSelectedId(Number(next.id));
  }

  function onKeydown(e: KeyboardEvent) {
    if (
      selectedId === null ||
      (e.target instanceof HTMLElement &&
        (e.target.matches("input,textarea,select") ||
          e.target.isContentEditable))
    )
      return;
    if (e.key === "Escape") {
      e.preventDefault();
      setSelectedId(null);
    } else if (["j", "J", "ArrowDown"].includes(e.key)) {
      e.preventDefault();
      step(1);
    } else if (["k", "K", "ArrowUp"].includes(e.key)) {
      e.preventDefault();
      step(-1);
    }
  }

  const MIN_SPIN_MS = 450;
  const [spinning, setSpinning] = useState(false);

  async function refresh() {
    if (spinning) return;
    setSpinning(true);
    const startedAt = Date.now();

    try {
      const pending: Promise<unknown>[] = [refetchList()];

      if (selectedId !== null) pending.push(refetchDetail());
      await Promise.all(pending);
    } finally {
      const wait = MIN_SPIN_MS - (Date.now() - startedAt);

      if (wait > 0) await new Promise((resolve) => setTimeout(resolve, wait));
      setSpinning(false);
    }
  }

  useEffect(() => {
    const shown = Events.On("main:shown", () => {
      setVisible(true);
      void refetchList();
      if (selectedId !== null) void refetchDetail();
    });
    const hidden = Events.On("main:hidden", () => {
      setVisible(false);
    });

    return () => {
      shown();
      hidden();
    };
  }, [selectedId, refetchList, refetchDetail]);

  useWindowEvent("keydown", onKeydown);

  return (
    <>
      <main className="mx-auto w-full flex-1 max-w-[var(--page-max-width)] p-[14px_32px_10px]">
        <div className="flex items-baseline gap-3 mb-4">
          <h1 className="text-text m-0 font-bold text-[38px] tracking-[-0.035em]">
            Activity
          </h1>
          <span className="text-text-muted text-sm">
            ({periodRangeText(selectedPeriod)})
          </span>

          <button
            type="button"
            disabled={spinning}
            title={spinning ? "조회 중" : "새로고침"}
            aria-label="새로고침"
            onClick={() => {
              void refresh();
            }}
            style={cssStyle("opacity:" + String(spinning ? "0.6" : "1"))}
            className={[
              "ml-auto flex flex-none items-center justify-center border bg-transparent transition-[opacity,border-color] duration-[180ms] ease-in-out " +
                String(
                  spinning
                    ? "text-text-muted cursor-default"
                    : "text-accent hover:border-border-strong hover:bg-surface-hover cursor-pointer",
                ),
              "w-[30px] h-[30px] rounded-[9px] [border-color:var(--color-border)]",
            ]
              .filter(Boolean)
              .join(" ")}
          >
            <RefreshIcon
              size={15}
              strokeWidth={2.2}
              style={
                "animation:" +
                String(spinning ? "spin 900ms linear infinite" : "none")
              }
              className="[transform-origin:50%_50%]"
            />
          </button>
        </div>
        <div className="flex flex-wrap gap-2 mb-4">
          <Select
            aria-label="에이전트 필터"
            value={vendor}
            onValueChange={setVendor}
          >
            <option value="">모든 에이전트</option>
            <option value="claude_code">Claude Code</option>
            <option value="codex">Codex</option>
          </Select>
          <Select
            aria-label="상태 필터"
            value={status}
            onValueChange={setStatus}
          >
            <option value="">모든 상태</option>
            <option value="running">진행 중</option>
            <option value="completed">종료</option>
          </Select>
          <Input
            aria-label="프로젝트 전체 경로"
            placeholder="프로젝트 전체 경로"
            value={project}
            onValueChange={setProject}
          />
          <Input
            type="search"
            icon="search"
            aria-label="활동 검색"
            placeholder="제목·경로·파일·프롬프트 검색"
            value={search}
            onValueChange={setSearch}
            className="min-w-60 flex-1"
          />
        </div>
        {list.isError ? (
          <>
            <p role="alert">
              데몬에서 활동을 불러오지 못했습니다.{" "}
              {list.data
                ? "마지막 조회 결과를 표시합니다."
                : "데몬 실행 상태를 확인해주세요."}
            </p>
          </>
        ) : null}
        {list.dataUpdatedAt ? (
          <>
            <p className="text-text-muted text-xs mb-2">
              {new Date(list.dataUpdatedAt).toLocaleTimeString([], {
                hour: "2-digit",
                minute: "2-digit",
                hourCycle: "h23",
              })}{" "}
              조회 · 목록 비용은 벤더 보고값입니다.
            </p>
          </>
        ) : null}
        {list.isPending ? (
          <>
            <p role="status">활동을 불러오는 중입니다.</p>
          </>
        ) : (
          <>
            {list.data ? (
              <>
                <SessionTable
                  sessions={sessions}
                  selectedIndex={selectedIndex}
                  onOpen={(i) => {
                    const row = sessions[i];

                    if (row) setSelectedId(Number(row.id));
                  }}
                  hasMore={list.hasNextPage}
                  loadingMore={list.isFetchingNextPage}
                  onLoadMore={() => {
                    void list.fetchNextPage();
                  }}
                />
              </>
            ) : null}
          </>
        )}
      </main>
      <SessionDetail
        open={selectedId !== null}
        session={shown}
        position={
          selectedIndex >= 0 ? `${selectedIndex + 1} / ${sessions.length}` : ""
        }
        loading={detail.isPending}
        error={detailError}
        onRetry={() => {
          void detail.refetch();
        }}
        onClose={() => setSelectedId(null)}
        onPrev={() => step(-1)}
        onNext={() => step(1)}
      />
    </>
  );
}
