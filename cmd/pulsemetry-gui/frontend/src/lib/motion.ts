// CSS cubic-bezier()와 동일한 이징 함수를 만든다.
// Svelte 트랜지션은 JS 이징만 받으므로, 디자인 스펙의 베지어 곡선을 그대로 쓰기 위한 변환기.
export function cubicBezier(
  x1: number,
  y1: number,
  x2: number,
  y2: number,
): (t: number) => number {
  const sampleX = (t: number) =>
    3 * x1 * t * (1 - t) ** 2 + 3 * x2 * t ** 2 * (1 - t) + t ** 3;
  const sampleY = (t: number) =>
    3 * y1 * t * (1 - t) ** 2 + 3 * y2 * t ** 2 * (1 - t) + t ** 3;

  return (x: number) => {
    if (x <= 0) return 0;
    if (x >= 1) return 1;
    // 이분 탐색으로 x에 대응하는 곡선 매개변수 t를 찾는다.
    let lo = 0;
    let hi = 1;
    let t = x;
    for (let i = 0; i < 24; i++) {
      if (sampleX(t) < x) lo = t;
      else hi = t;
      t = (lo + hi) / 2;
    }
    return sampleY(t);
  };
}

// 드로어 시트 슬라이드 곡선 — cubic-bezier(0.32, 0.72, 0, 1)
export const sheetEase = cubicBezier(0.32, 0.72, 0, 1);

// CSS "ease-out" — cubic-bezier(0, 0, 0.58, 1)
export const easeOut = cubicBezier(0, 0, 0.58, 1);
