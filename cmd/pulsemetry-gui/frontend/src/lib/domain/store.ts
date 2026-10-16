import { useSyncExternalStore } from "react";

// 단일 웹뷰에서 공유하는 얕은 상태. 속성 변경 시 구독자에게 새 버전을 알린다.
export function createStore<T extends object>(initial: T) {
  let version = 0;
  const listeners = new Set<() => void>();
  const state = new Proxy(initial, {
    set(target, property, value) {
      if (Reflect.get(target, property) === value) return true;
      Reflect.set(target, property, value);
      version++;
      listeners.forEach((listener) => listener());

      return true;
    },
  });
  const subscribe = (listener: () => void) => {
    listeners.add(listener);

    return () => {
      listeners.delete(listener);
    };
  };
  const snapshot = () => version;

  function useStore() {
    useSyncExternalStore(subscribe, snapshot, snapshot);

    return state;
  }

  return { state, useStore };
}
