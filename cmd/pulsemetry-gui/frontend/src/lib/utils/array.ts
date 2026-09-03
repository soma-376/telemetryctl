// 인덱싱이 undefined 를 낼 수 있다는 한 가지 사실을 다루는 도구들.
// tsconfig 의 noUncheckedIndexedAccess 를 켜면서 필요해졌다.

/** 최소 한 개를 보장하는 배열. 첫 원소 접근이 undefined 가 되지 않는다. */
export type NonEmpty<T> = [T, ...T[]];

// isNonEmpty 는 길이 검사를 타입 좁히기로 승격한다. 이미 하고 있는 런타임 검사
// 자리에 두면 "여기부터는 최소 하나" 를 컴파일러도 알게 된다.
export function isNonEmpty<T>(a: T[]): a is NonEmpty<T> {
  return a.length > 0;
}

// at 은 인덱싱을 조용한 undefined 대신 명시적 오류로 바꾼다.
//
// 사람이 보기에 범위 안이 확실한 자리가 많은데, 그때 `!` 를 쓰면 검사를 지우기만 하고
// 틀렸을 때 화면 어딘가에서 "undefined 의 속성" 으로 터진다. 여기서 던지면 어디가
// 범위를 벗어났는지가 바로 남는다.
export function at<T>(arr: ArrayLike<T>, i: number): T {
  const v = arr[i];
  if (v === undefined) {
    throw new RangeError(`인덱스 ${i} 가 범위를 벗어났습니다 (길이 ${arr.length})`);
  }
  return v;
}
