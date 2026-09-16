import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

// 기존 디자인의 CSS 문자열을 React 스타일로 해석한다. CSS 변수와 단위는 보존한다.
export function cssStyle(value?: string): CSSProperties {
  const result: Record<string, string> = {};

  for (const declaration of (value ?? "").split(";")) {
    const colon = declaration.indexOf(":");

    if (colon < 0) continue;
    const property = declaration.slice(0, colon).trim();
    const key = property.startsWith("--")
      ? property
      : property
          .replace(/^-ms-/, "ms-")
          .replace(/-([a-z])/g, (_, c: string) => c.toUpperCase());

    result[key] = declaration.slice(colon + 1).trim();
  }

  return result;
}
export function useWindowEvent<K extends keyof WindowEventMap>(
  name: K,
  handler: (event: WindowEventMap[K]) => void,
) {
  const current = useRef(handler);

  current.current = handler;

  useEffect(() => {
    const listener = (event: WindowEventMap[K]) => current.current(event);

    window.addEventListener(name, listener);

    return () => window.removeEventListener(name, listener);
  }, [name]);
}
export function useWidth(setWidth: (width: number) => void) {
  const observer = useRef<ResizeObserver | null>(null);

  useEffect(() => () => observer.current?.disconnect(), []);

  return useCallback(
    (element: HTMLElement | null) => {
      observer.current?.disconnect();
      if (!element) return;
      const measure = () => setWidth(element.clientWidth);

      measure();
      observer.current = new ResizeObserver(measure);
      observer.current.observe(element);
    },
    [setWidth],
  );
}
export function Portal({ children }: { children: ReactNode }) {
  return createPortal(children, document.body);
}
// 닫히는 전환이 끝날 때까지 내용을 유지한다. 모션 감소 설정에서는 즉시 제거한다.
export function usePresence(open: boolean, duration: number) {
  const [present, setPresent] = useState(open);

  useEffect(() => {
    if (open) {
      setPresent(true);

      return;
    }
    const ms = window.matchMedia("(prefers-reduced-motion: reduce)").matches
      ? 0
      : duration;
    const timer = setTimeout(() => setPresent(false), ms);

    return () => clearTimeout(timer);
  }, [open, duration]);

  return open || present;
}
