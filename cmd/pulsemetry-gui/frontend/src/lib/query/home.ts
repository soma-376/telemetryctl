import { useQuery } from "@tanstack/react-query";
import { Dashboard, type HomeQuery } from "$lib/bindings";
import { Events } from "@wailsio/runtime";
import { useEffect, useState } from "react";

export function useHomeQuery(query: HomeQuery) {
  const [visible, setVisible] = useState(true);
  const result = useQuery({
    queryKey: ["home", query],
    queryFn: () => Dashboard.Home(query),
    placeholderData: undefined,
    enabled: visible,
    refetchInterval: visible ? 30_000 : false,
    staleTime: 10_000,
  });
  const { refetch } = result;

  useEffect(() => {
    const shown = Events.On("main:shown", () => {
      setVisible(true);
      // 헤더와 홈이 같은 키를 구독하므로 먼저 시작한 재조회를 함께 기다린다.
      void refetch({ cancelRefetch: false });
    });
    const hidden = Events.On("main:hidden", () => setVisible(false));

    return () => {
      shown();
      hidden();
    };
  }, [refetch]);

  return result;
}
