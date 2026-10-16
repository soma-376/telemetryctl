import ChevronDownIcon from "$lib/icons/ChevronDownIcon";
import type { ReactNode as Snippet } from "react";
import "./Select.css";

export default function Select({
  value = "",
  children,
  className: klass = "",
  onValueChange,
  onChange,
  ...rest
}: React.SelectHTMLAttributes<HTMLSelectElement> & {
  value?: string;
  onValueChange?: (value: string) => void;
  children?: Snippet;
}) {
  return (
    <>
      <span
        className={[
          "relative inline-flex items-center " + String(klass),
          "scope-1o3rpsw",
        ]
          .filter(Boolean)
          .join(" ")}
      >
        <select
          value={value}
          onChange={(e) => {
            onValueChange?.(e.currentTarget.value);
            onChange?.(e);
          }}
          {...rest}
          className="field bg-surface border-border text-text w-full cursor-pointer appearance-none border whitespace-nowrap scope-1o3rpsw rounded-[10px] p-[10px_35px_10px_14px] text-[13px] [font-family:inherit]"
        >
          {children}
        </select>
        <ChevronDownIcon
          size={13}
          strokeWidth={2.2}
          className="text-text-muted pointer-events-none absolute [right:14px]"
        />
      </span>
    </>
  );
}
