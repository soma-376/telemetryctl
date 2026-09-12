import {
  createMutation,
  createQuery,
  useQueryClient,
} from "@tanstack/svelte-query";
import { Dashboard, type TrayQuery, type TraySnapshot } from "$lib/bindings";
import { TRAY_STALE_MS } from "./client";

// 트레이 스냅샷의 조회·갱신을 한곳에 모은다 (ADR 0015).
//
// lib/bindings.ts 는 바인딩의 경로와 이름만 알고, 여기가 "언제 부르고 결과를 어떻게 들고
// 있는가" 를 안다. 화면은 둘 다 몰라도 된다.

// 캐시 키. 다른 화면이 이 캐시를 무효화하게 되면 그때 export 한다.
const TRAY_KEY = ["tray"] as const;

// trayQuery 는 데몬이 들고 있는 상태를 읽는다. 벤더를 두드리지 않는다.
//
// visible 이 false 면 폴링을 멈춘다. 트레이 창은 닫혀도 파괴되지 않고 숨겨질 뿐이라,
// 이것을 끄지 않으면 보이지도 않는 창이 1분마다 데몬을 깨운다.
export function trayQuery(q: TrayQuery, visible: () => boolean) {
  return createQuery(() => ({
    queryKey: TRAY_KEY,
    queryFn: () => Dashboard.Tray(q),
    refetchInterval: visible() ? TRAY_STALE_MS : (false as const),
  }));
}

// 버튼만 벤더 갱신을 요청한다. 실패하면 마지막 정상 스냅샷은 유지한다.
export function trayRefreshMutation(q: TrayQuery) {
  const client = useQueryClient();
  return createMutation(() => ({
    mutationFn: () => Dashboard.RefreshTray(q),
    async onSuccess(snapshot: TraySnapshot) {
      // 먼저 시작한 조회가 늦게 끝나 최신 갱신 결과를 덮지 않게 한다.
      await client.cancelQueries({ queryKey: TRAY_KEY });
      client.setQueryData(TRAY_KEY, snapshot);
    },
  }));
}
