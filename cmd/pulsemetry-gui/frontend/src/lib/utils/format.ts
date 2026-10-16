// 원본 토큰 수를 표시할 때만 축약한다. 집계와 차트 좌표는 원본 수치를 유지한다.
export function formatTokens(tokens: number): string {
  if (tokens < 1000) return Math.round(tokens).toLocaleString();
  const divisor = tokens >= 1_000_000 ? 1_000_000 : 1000;

  return `${Number((tokens / divisor).toFixed(2))}${divisor === 1000 ? "k" : "M"}`;
}

// 소요 시간은 tabular-nums 를 쓰는 표의 열에 들어가므로 분은 항상 두 자리로 채운다 —
// 자릿수가 들쭉날쭉하면 등폭 숫자를 쓰는 의미가 없어진다.
export function formatDuration(minutes: number): string {
  minutes = Math.max(0, Math.floor(minutes));
  if (minutes < 60) return `${minutes}m`;
  const m = minutes % 60;

  return `${Math.floor(minutes / 60)}h ${m < 10 ? `0${m}` : m}m`;
}
