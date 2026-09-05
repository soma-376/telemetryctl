<script lang="ts">
  import type { MascotPose } from "./mascot.types";
  import viewFront from "../../assets/pulse/view-front.png";
  import collectingAlt from "../../assets/pulse/collecting-alt.png";
  import found from "../../assets/pulse/found.png";
  import warning from "../../assets/pulse/warning.png";
  import noData from "../../assets/pulse/no-data.png";
  import confused from "../../assets/pulse/confused.png";

  const SRC: Record<MascotPose, string> = {
    "view-front": viewFront,
    "collecting-alt": collectingAlt,
    found,
    warning,
    "no-data": noData,
    confused,
  };

  let {
    pose,
    height = 128,
    alt = "pulse",
  }: { pose: MascotPose; height?: number; alt?: string } = $props();
</script>

<img src={SRC[pose]} {alt} style="height:{height}px;width:auto" />
