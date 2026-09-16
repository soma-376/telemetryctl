import { cssStyle } from "$lib/react-utils";

export default function Dot({
  size = 7,
  color,
  pulse = false,
  className: klass = "",
  style = "",
}: {
  size?: number;
  color: string;
  pulse?: boolean;
  className?: string;
  style?: string;
}) {
  return (
    <>
      <span
        style={cssStyle(
          "width:" +
            String(size) +
            "px;height:" +
            String(size) +
            "px;border-radius:50%;background:" +
            String(color) +
            String(pulse ? ";animation:livePulse 2s ease-out infinite" : "") +
            ";" +
            String(style),
        )}
        className={"block flex-none " + String(klass)}
      />
    </>
  );
}
