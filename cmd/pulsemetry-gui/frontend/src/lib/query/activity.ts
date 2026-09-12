import { createInfiniteQuery, createQuery } from "@tanstack/svelte-query";
import { Dashboard, type ActivityQuery } from "$lib/bindings";

export function activityQuery(query: () => ActivityQuery, visible: () => boolean) {
  return createInfiniteQuery(() => ({
    queryKey: ["activity", query()],
    initialPageParam: { running: false, sort_at: 0, id: 0 },
    queryFn: ({ pageParam }) => Dashboard.Activity({ ...query(), cursor: pageParam }),
    getNextPageParam: (page) => page.has_more ? page.next_cursor : undefined,
    placeholderData: undefined,
    enabled: visible(),
    refetchInterval: visible() ? 30_000 : false,
    staleTime: 10_000,
  }));
}

export function activityDetailQuery(id: () => number | null, visible: () => boolean) {
  return createQuery(() => ({
    queryKey: ["activity-session", id()],
    queryFn: () => Dashboard.ActivitySession(id()!),
    // 다른 세션의 상세가 잠깐 표시되지 않도록 이전 키의 데이터를 쓰지 않는다.
    placeholderData: undefined,
    enabled: id() !== null && visible(),
    refetchInterval: visible() && id() !== null ? 30_000 : false,
    staleTime: 10_000,
  }));
}
