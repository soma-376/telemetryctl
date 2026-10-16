// localTimeZone 은 "오늘" 의 경계를 정한다. 빈 문자열을 백엔드에 넘기면 UTC 로 읽어
// 자정 근처에서 남의 날짜를 그린다.
//
// 바인딩을 건드리지 않는 순수 브라우저 유틸이라 ipc 가 아니라 여기에 둔다.
export function localTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}
