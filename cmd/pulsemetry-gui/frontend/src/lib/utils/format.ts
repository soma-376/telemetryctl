// 화면 전반의 소요 시간 표기를 한 곳에서 정한다.
// 이 값들은 tabular-nums 를 쓰는 표의 열에 들어가므로 분은 항상 두 자리로 채운다 —
// 자릿수가 들쭉날쭉하면 등폭 숫자를 쓰는 의미가 없어진다.
export function formatDuration(minutes: number): string {
  if (minutes < 60) return `${minutes}m`;
  const m = minutes % 60;
  return `${Math.floor(minutes / 60)}h ${m < 10 ? `0${m}` : m}m`;
}
