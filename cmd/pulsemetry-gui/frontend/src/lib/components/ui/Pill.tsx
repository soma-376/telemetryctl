import { cssStyle } from "$lib/react-utils";
import Dot from "./Dot";

export default function Pill({
  label,
  fg,
  bg,
  fontSize = 11,
  radius = 7,
  padding = "4px 9px",
  gap = 6,
  dot = false,
  pulse = false,
  className: klass = "",
}: {
  label: string;
  fg: string;
  bg: string;
  fontSize?: number;
  radius?: number;
  padding?: string;
  gap?: number;
  dot?: boolean;
  pulse?: boolean;
  className?: string;
}) {
  return (
    <>
      <span
        style={cssStyle(
          String(dot ? `gap:${gap}px;` : "") +
            "font-size:" +
            String(fontSize) +
            "px;border-radius:" +
            String(radius) +
            "px;padding:" +
            String(padding) +
            ";color:" +
            String(fg) +
            ";background:" +
            String(bg),
        )}
        className={
          "font-semibold whitespace-nowrap " +
          String(dot ? "inline-flex items-center" : "") +
          " " +
          String(klass)
        }
      >
        {dot ? (
          <>
            <Dot size={6} color={fg} pulse={pulse} />
          </>
        ) : null}
        {label}
      </span>
    </>
  );
}
