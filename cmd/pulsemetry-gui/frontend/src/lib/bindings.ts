import { App, Dashboard } from "../../bindings/github.com/your-org/pulsemetry/cmd/pulsemetry-gui";
import type { AppInfo } from "../../bindings/github.com/your-org/pulsemetry/cmd/pulsemetry-gui";
import type { RecentSession } from "../../bindings/github.com/your-org/pulsemetry/internal/dashboard";
// 트레이 타입은 Go 쪽 tray 패키지에 있어 이름에 접두사가 없다. 화면이 쓰는 이름은
// 여기서 붙인다 — Tray 를 뗀 Snapshot·Query 는 다른 화면의 것과 구분되지 않는다.
import { State as TrayState } from "../../bindings/github.com/your-org/pulsemetry/internal/dashboard/tray";
import type {
  Monitoring as TrayMonitoring,
  Query as TrayQuery,
  Snapshot as TraySnapshot,
} from "../../bindings/github.com/your-org/pulsemetry/internal/dashboard/tray";
import { State as LimitState } from "../../bindings/github.com/your-org/pulsemetry/internal/vendorlimit";
import type {
  Result as VendorLimit,
  Window as LimitWindow,
} from "../../bindings/github.com/your-org/pulsemetry/internal/vendorlimit";

// 생성된 바인딩의 긴 경로와 이름을 아는 파일은 **여기 하나다.** Go 패키지가 옮겨가거나
// 타입 이름이 바뀌어도 고칠 곳이 한 군데로 끝난다.
//
// 모양은 바꾸지 않는다 — 백엔드 타입을 그대로 통과시키고, 화면 모양으로의 번역은 각
// 페이지의 adapter 가 한다. 호출도 감싸지 않는다: 한 줄짜리 통과 함수는 값을 더하지 않고,
// 언제 부를지와 실패를 어떻게 다룰지는 Query 계층이 정한다 (ADR 0015).
//
// 여기 없는 것을 내보내지 않는다. 쓰지 않는 타입까지 통과시키기 시작하면 이 파일이
// 바인딩 전체의 복사본이 되고, 그러면 무엇이 실제로 쓰이는지 알 수 없게 된다.

export { App, Dashboard, TrayState, LimitState };
export type {
  AppInfo,
  LimitWindow,
  RecentSession,
  TrayMonitoring,
  TrayQuery,
  TraySnapshot,
  VendorLimit,
};
