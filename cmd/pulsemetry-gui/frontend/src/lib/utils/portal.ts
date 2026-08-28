import type { Action } from "svelte/action";

// 오버레이(드로어·모달)를 document.body 로 옮긴다.
//
// position:fixed 는 조상이 스태킹 컨텍스트를 만들면 그 안에 갇힌다. 본문 스크롤
// 영역은 가장자리 페이드용 mask-image 를 쓰므로, 그 안에서 열린 오버레이는 창
// 전체가 아니라 스크롤 영역 크기로 잘려 헤더·내비에 가려진다. body 로 빼내면
// 조상이 무엇을 하든 창 전체를 덮는다.
export const portal: Action<HTMLElement> = (node) => {
  document.body.appendChild(node);
  return {
    destroy() {
      node.remove();
    },
  };
};
