// 데몬 연결이 끊겼을 때의 상태다. **앱당 하나**여야 한다.
//
// 화면마다 들고 있으면 트레이와 메인이 각자 카운트다운을 돌려 재시도가 두 번 나가고,
// 화면을 옮길 때마다 backoff 단계가 초기화된다. 데몬은 하나이므로 그 연결 상태도 하나다.
// period.svelte.ts 와 같은 모듈 단위 룬 상태를 쓴다.

import { at } from "$lib/utils/array";

/** BACKOFF 는 자동 재시도 간격(초)이다. 실패가 이어질수록 뜸해진다. */
const BACKOFF = [5, 10, 20, 30, 60] as const;

/**
 * DOWN_AFTER 는 끊김으로 판정하기까지의 연속 실패 수다.
 *
 * 한 번 실패에 화면을 덮으면 안 된다. 폴링은 60초마다 도는데 일시적인 실패 한 번으로
 * 전체 화면이 바뀌었다가 다음 폴링에 돌아오면 트레이를 열 때마다 도박이 된다. 창 열기
 * 갱신도 실패로 세므로 실제로는 2분보다 훨씬 빨리 판정된다.
 */
const DOWN_AFTER = 2;

export const reconnect = $state({
  /** 끊김으로 판정됐다. 화면을 대체할지 결정하는 값이다. */
  down: false,
  /** 지금 재시도가 나가 있다. */
  retrying: false,
  /** 몇 번째 자동 재시도인가 (0 = 아직 한 번도 안 했다). */
  attempt: 0,
  /** 다음 자동 재시도까지 남은 초. */
  left: 0,
  /** 연속 실패 수. DOWN_AFTER 에 닿으면 down 이 된다. */
  failures: 0,
});

// run 은 실제 재시도 동작이다. 화면이 등록한다 — 이 모듈은 무엇을 다시 부를지 모른다.
let run: (() => Promise<unknown>) | undefined;
let timer: ReturnType<typeof setInterval> | undefined;

/** bindRetry 는 재시도로 무엇을 부를지 등록한다. 마지막 등록이 이긴다. */
export function bindRetry(fn: () => Promise<unknown>): void {
  run = fn;
}

function startTimer(): void {
  if (timer !== undefined) return;
  timer = setInterval(() => {
    if (!reconnect.down || reconnect.retrying) return;
    if (reconnect.left > 1) {
      reconnect.left -= 1;
      return;
    }
    reconnect.left = 0;
    void retryNow();
  }, 1000);
}

function stopTimer(): void {
  if (timer === undefined) return;
  clearInterval(timer);
  timer = undefined;
}

/** noteFailure 는 조회 실패를 알린다. 같은 실패를 여러 화면이 알려도 결과는 같다. */
export function noteFailure(): void {
  if (reconnect.down) return;
  reconnect.failures += 1;
  if (reconnect.failures < DOWN_AFTER) return;
  reconnect.down = true;
  reconnect.attempt = 0;
  reconnect.left = BACKOFF[0];
  startTimer();
}

/** noteSuccess 는 조회 성공을 알린다. 끊김이었다면 그 자리에서 회복한다. */
export function noteSuccess(): void {
  reconnect.failures = 0;
  if (!reconnect.down) return;
  reconnect.down = false;
  reconnect.retrying = false;
  reconnect.attempt = 0;
  reconnect.left = 0;
  stopTimer();
}

/**
 * retryNow 는 지금 다시 연결한다. 버튼과 자동 재시도가 같은 경로를 쓴다.
 *
 * **프로세스를 띄우지 않는다.** GUI 에 데몬을 기동하는 경로가 없다 — 여기서 하는 것은
 * 조회를 다시 시도하는 것이고, 데몬이 다른 경로로 살아났으면 그때 회복된다.
 */
export async function retryNow(): Promise<void> {
  if (reconnect.retrying || !run) return;
  reconnect.retrying = true;
  try {
    await run();
  } catch {
    // 성공/실패 판정은 noteSuccess·noteFailure 가 조회 상태를 보고 한다.
  } finally {
    reconnect.retrying = false;
    if (reconnect.down) {
      const next = Math.min(reconnect.attempt + 1, BACKOFF.length - 1);
      reconnect.attempt = next;
      reconnect.left = at(BACKOFF, next);
    }
  }
}

/** retryNote 는 재시도 안내 문구다. */
export function retryNote(): string {
  if (reconnect.retrying) return "다시 연결하고 있어요";
  if (reconnect.attempt === 0) return `${reconnect.left}초 후 자동 재시도`;
  return `${reconnect.left}초 후 재시도 · ${reconnect.attempt + 1}번째`;
}
