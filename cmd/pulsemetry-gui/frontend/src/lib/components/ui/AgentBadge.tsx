import { AGENT_STYLE } from "$lib/domain/agent";
import type { AgentId } from "$lib/domain/agent.types";
import { cssStyle } from "$lib/react-utils";

type BadgeSize = 24 | 28 | 30 | 32 | 40;
export default function AgentBadge({
  agent,
  size = 32,
  fontSize,
}: {
  agent: AgentId;
  size?: BadgeSize;
  fontSize?: number;
}) {
  const SIZES = {
    24: { radius: 7, font: "sm", cap: Infinity },
    28: { radius: 9, font: "sm", cap: Infinity },
    30: { radius: 9, font: "md", cap: 15 },
    32: { radius: 10, font: "md", cap: Infinity },
    40: { radius: 12, font: "md", cap: Infinity },
  } as const;
  const style = AGENT_STYLE[agent];
  const spec = SIZES[size];
  const font =
    fontSize ??
    Math.min(spec.font === "sm" ? style.fontSm : style.fontMd, spec.cap);

  return (
    <>
      <div
        style={cssStyle(
          "width:" +
            String(size) +
            "px;height:" +
            String(size) +
            "px;border-radius:" +
            String(spec.radius) +
            "px;background:" +
            String(style.bg) +
            ";color:" +
            String(style.fg) +
            ";font-size:" +
            String(font) +
            "px;font-weight:" +
            String(style.weight),
        )}
        className="flex flex-none items-center justify-center"
      >
        {style.glyph}
      </div>
    </>
  );
}
