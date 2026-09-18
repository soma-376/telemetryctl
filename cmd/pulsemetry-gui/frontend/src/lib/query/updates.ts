import { useQuery } from "@tanstack/react-query";
import { Dashboard } from "$lib/bindings";

// 설정을 열면 데몬의 마지막 확인 결과를 다시 읽는다. 외부 서버 확인은 데몬의 책임이다.
export function useUpdatesQuery(open: boolean) {
  return useQuery({
    queryKey: ["updates"],
    queryFn: () => Dashboard.Updates(),
    enabled: open,
    staleTime: 0,
    refetchInterval: open ? 60_000 : false,
  });
}
