import SearchIcon from "$lib/icons/SearchIcon";
import "./Input.css";

export default function Input({
  value = "",
  icon = "none",
  className: klass = "",
  onValueChange,
  onChange,
  ...rest
}: React.InputHTMLAttributes<HTMLInputElement> & {
  value?: string;
  onValueChange?: (value: string) => void;
  icon?: "none" | "search";
}) {
  return (
    <>
      <span
        className={[
          [
            "box bg-surface border-border inline-flex items-center border " +
              String(klass),
            "scope-n2qvko",
          ]
            .filter(Boolean)
            .join(" "),
          "gap-[9px] rounded-[10px] p-[10px_12px]",
        ]
          .filter(Boolean)
          .join(" ")}
      >
        {icon === "search" ? (
          <>
            <SearchIcon className="text-text-muted flex-none" />
          </>
        ) : null}
        <input
          value={value}
          onChange={(e) => {
            onValueChange?.(e.currentTarget.value);
            onChange?.(e);
          }}
          {...rest}
          className="field text-text w-full min-w-0 flex-1 border-none bg-transparent outline-none scope-n2qvko text-[13px]"
        />
      </span>
    </>
  );
}
