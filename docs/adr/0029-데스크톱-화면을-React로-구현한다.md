# 0029. 데스크톱 화면을 React로 구현한다

## Status

Accepted — ADR 0015의 TanStack Svelte Query 선택을 React Query로 대체한다.

## Context

사용자는 React에 익숙하며 데스크톱 프런트엔드의 전체 전환을 요청했다.
Wails와 Go의 로컬 API 계약은 프런트엔드 프레임워크에 종속되지 않는다.

## Decision

- 화면을 React·TypeScript·Vite로 구현한다. Wails 런타임과 생성된 Go 바인딩은 유지한다.
- 조회 캐시는 웹뷰당 하나의 TanStack React Query 클라이언트가 소유한다. ADR 0015의
  신선도·재시도·수동 갱신·창 가시성 정책은 유지한다.
- 컴포넌트 상태는 React 훅으로, 공유 기간과 재연결 상태는 구독 가능한 저장소로 관리한다.
- DOM 측정과 이벤트 구독은 정리 함수를 가진 효과로 관리한다. 모달은 React portal을 사용한다.
- 기존 화면·필터·차트·트레이·키보드 조작과 모션 감소 설정을 유지한다.
- Next.js 서버와 SSR은 추가하지 않는다. Vite 정적 산출물을 Go에 임베드한다.

## Alternatives Considered

- Svelte 유지: 재작성 비용은 없지만 사용자의 유지보수 도구 선택과 맞지 않는다.
- 프레임워크 혼용: 상태와 빌드 경계를 두 벌 유지해야 하므로 최종 구조로 채택하지 않는다.

## Consequences/Tradeoffs

### Positive

사용자가 익숙한 React로 화면을 유지보수하고 일반적인 React 도구를 활용할 수 있다.

### Negative

이벤트 수명·스타일 범위·전환 애니메이션에서 회귀 가능성이 있어 타입 검사와 화면 검증이 필요하다.
전환 자체가 성능 향상을 보장하지 않는다.

## Acceptance Criteria

Svelte 런타임 의존을 제거하고 타입 검사·프런트 빌드·Wails 제품 빌드를 통과한다.
메인·트레이 화면과 날짜 선택·세션 상세·오류 상태를 확인한다.
