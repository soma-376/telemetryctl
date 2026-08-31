import { Dashboard } from "../../../bindings/github.com/your-org/pulsemetry/cmd/pulsemetry-gui";
import type { RecentSession } from "../../../bindings/github.com/your-org/pulsemetry/internal/dashboard";
// 트레이 타입은 Go 쪽 tray 패키지에 있어 이름에 접두사가 없다. 화면이 쓰는 이름은
// 여기서 붙인다 — Tray 를 뗀 Snapshot·Query 는 다른 화면의 것과 구분되지 않는다.
import { State as TrayState } from "../../../bindings/github.com/your-org/pulsemetry/internal/dashboard/tray";
import type {
  Monitoring as TrayMonitoring,
  Query as TrayQuery,
  Snapshot as TraySnapshot,
  TightestLimit,
} from "../../../bindings/github.com/your-org/pulsemetry/internal/dashboard/tray";
import { State as LimitState } from "../../../bindings/github.com/your-org/pulsemetry/internal/vendorlimit";
import type {
  Result as VendorLimit,
  Window as LimitWindow,
} from "../../../bindings/github.com/your-org/pulsemetry/internal/vendorlimit";

// 로컬 조회 바인딩의 얇은 껍데기 — app.ts(창 제어)의 형제다.
//
// 생성된 바인딩의 긴 경로를 아는 파일은 여기 하나다. Go 패키지가 옮겨가도 고칠 곳이
// 한 군데로 끝난다. **모양은 바꾸지 않는다** — 백엔드 타입을 그대로 통과시키고,
// 화면 모양으로의 번역은 각 페이지의 adapter 가 한다.

export { TrayState, LimitState };
export type {
  LimitWindow,
  RecentSession,
  TightestLimit,
  TrayMonitoring,
  TrayQuery,
  TraySnapshot,
  VendorLimit,
};

// IpcResult 는 실패를 예외가 아니라 값으로 돌려준다. 화면은 로딩·성공·실패를 나란히
// 놓고 그려야 하는데, try/catch 로 흩어지면 그 셋이 한곳에 모이지 않는다.
export type IpcResult<T> =
  | { ok: true; data: T }
  | { ok: false; message: string };

// localTimeZone 은 "오늘" 의 경계를 정한다. 빈 문자열을 넘기면 백엔드가 UTC 로 읽어
// 자정 근처에서 남의 날짜를 그린다.
export function localTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

async function guard<T>(run: () => Promise<T>): Promise<IpcResult<T>> {
  try {
    return { ok: true, data: await run() };
  } catch (e) {
    return { ok: false, message: e instanceof Error ? e.message : String(e) };
  }
}

// fetchTray 는 갱신 주기 안이면 백엔드가 들고 있는 캐시를 그대로 받는다.
export function fetchTray(q: TrayQuery): Promise<IpcResult<TraySnapshot>> {
  return guard(() => Dashboard.Tray(q));
}

// refreshTray 는 주기를 무시하고 다시 만든다. 사용자가 새로고침을 눌렀는데 캐시가
// 돌아오면 버튼이 고장 난 것처럼 보인다.
export function refreshTray(q: TrayQuery): Promise<IpcResult<TraySnapshot>> {
  return guard(() => Dashboard.RefreshTray(q));
}
