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

// traySyncMutation 은 창이 열렸을 때 부른다. 자동 등급이라 데몬 쿨다운 안이면 벤더를
// 두드리지 않고 저장된 최신값이 그대로 온다 (ADR 0014).
export function traySyncMutation(q: TrayQuery) {
  return trayCommand(() => Dashboard.SyncTray(q));
}

// trayRefreshMutation 은 새로고침 버튼이다. 수동 등급으로 명령한다.
export function trayRefreshMutation(q: TrayQuery) {
  return trayCommand(() => Dashboard.RefreshTray(q));
}

// trayCommand 는 두 갱신의 공통 몸통이다. 둘은 데몬에 어느 등급으로 명령하는지만 다르고,
// 받은 스냅샷을 조회 캐시로 밀어 넣는 것은 같다 — 그래야 갱신 결과가 곧 화면이 된다.
//
// 실패해도 캐시를 건드리지 않는다. 데몬이 꺼졌다고 과거 화면까지 지울 이유가 없다.
function trayCommand(run: () => Promise<TraySnapshot>) {
  const client = useQueryClient();
  return createMutation(() => ({
    mutationFn: run,
    onSuccess(snapshot: TraySnapshot) {
      client.setQueryData(TRAY_KEY, snapshot);
    },
  }));
}
