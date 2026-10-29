import { useQuery } from "@tanstack/react-query";
import { Dashboard } from "$lib/bindings";

export function usePolicyQuery(open: boolean) {
  return useQuery({
    queryKey: ["policy"],
    queryFn: () => Dashboard.Policy(),
    enabled: open,
    staleTime: 0,
    refetchInterval: open ? 10_000 : false,
  });
}
