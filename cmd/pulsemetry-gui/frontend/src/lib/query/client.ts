import { QueryClient } from "@tanstack/svelte-query";

// 앱당 하나인 조회 캐시. 캐시·신선도·폴링·로딩/오류 상태는 전부 여기 위에서 돈다 (ADR 0015).
//
// 기본값 셋이 이 앱의 결정을 담고 있다. 화면마다 다시 쓰지 않도록 여기 모아 둔다.

// TRAY_STALE_MS 는 화면이 데몬을 다시 읽는 주기다. **벤더 호출 주기가 아니다** — 벤더를
// 언제 두드릴지는 데몬의 등급별 쿨다운이 정한다 (ADR 0014). 여기는 로컬 왕복이라 싸다.
export const TRAY_STALE_MS = 60_000;

export function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: TRAY_STALE_MS,

        // 조회 실패는 재시도가 아니라 "마지막 정상값을 계속 그린다" 로 다룬다. 데몬이 꺼져
        // 있는 것이 흔한 정상 상태라, 재시도는 그 상태를 오류 폭주로 바꿀 뿐이다.
        retry: false,

        // document.visibilityState 는 네이티브 창의 Hide/Show 와 같은 계약이 아니다.
        // 트레이 퀵뷰는 닫혀도 파괴되지 않고 숨겨질 뿐이라, 폴링 수명은 Wails 창 이벤트로
        // 제어한다 (PROJ-117 에서 확인). 이것을 켜면 그 제어가 두 곳으로 갈라진다.
        refetchOnWindowFocus: false,

        // 실패해도 마지막 정상 데이터를 화면에 남긴다. 낡은 숫자를 현재로 읽지 않도록
        // "언제 기준인가" 는 헤더가 limits_observed_at 으로 말한다.
        placeholderData: <T>(previous: T) => previous,
      },
      mutations: {
        retry: false,
      },
    },
  });
}
