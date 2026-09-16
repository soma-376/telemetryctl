import { cssStyle } from "$lib/react-utils";
import collectingAlt from "../../assets/pulse/collecting-alt.png";
import confused from "../../assets/pulse/confused.png";
import found from "../../assets/pulse/found.png";
import noData from "../../assets/pulse/no-data.png";
import viewFront from "../../assets/pulse/view-front.png";
import warning from "../../assets/pulse/warning.png";
import type { MascotPose } from "./mascot.types";

export default function Mascot({
  pose,
  height = 128,
  alt = "pulse",
}: {
  pose: MascotPose;
  height?: number;
  alt?: string;
}) {
  const SRC: Record<MascotPose, string> = {
    "view-front": viewFront,
    "collecting-alt": collectingAlt,
    found,
    warning,
    "no-data": noData,
    confused,
  };

  return (
    <>
      <img
        src={SRC[pose]}
        alt={alt}
        style={cssStyle("height:" + String(height) + "px")}
        className="w-[auto]"
      />
    </>
  );
}
