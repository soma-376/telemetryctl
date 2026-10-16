import { cssStyle } from "$lib/react-utils";

export default function ProgressBar({
  pct,
  color,
  height = 5,
  animate = false,
  className: klass = "",
  style = "",
}: {
  pct: number | string;
  color: string;
  height?: number;
  animate?: boolean;
  className?: string;
  style?: string;
}) {
  const width = typeof pct === "number" ? `${pct}%` : pct;

  return (
    <>
      <span
        style={cssStyle("height:" + String(height) + "px;" + String(style))}
        className={"bg-track block rounded-[999px] " + String(klass)}
      >
        <span
          style={cssStyle(
            "background:" +
              String(color) +
              ";width:" +
              String(width) +
              String(animate ? ";transition:width 300ms ease" : ""),
          )}
          className="block h-[100%] rounded-[999px]"
        />
      </span>
    </>
  );
}
