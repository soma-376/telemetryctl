<script lang="ts">
  import type { MascotPose } from "../types";
  import viewFront from "../assets/pulse/view-front.png";
  import normal from "../assets/pulse/normal.png";
  import tired from "../assets/pulse/tired.png";
  import watching from "../assets/pulse/watching.png";
  import collectingAlt from "../assets/pulse/collecting-alt.png";
  import found from "../assets/pulse/found.png";
  import foundAlt from "../assets/pulse/found-alt.png";
  import warning from "../assets/pulse/warning.png";
  import noData from "../assets/pulse/no-data.png";
  import offline from "../assets/pulse/offline.png";
  import done from "../assets/pulse/done.png";
  import iconPlain from "../assets/pulse/icon-plain.png";

  const SRC: Record<MascotPose, string> = {
    "view-front": viewFront,
    normal,
    tired,
    watching,
    "collecting-alt": collectingAlt,
    found,
    "found-alt": foundAlt,
    warning,
    "no-data": noData,
    offline,
    done,
    "icon-plain": iconPlain,
  };

  let {
    pose,
    height = 128,
    alt = "pulse",
  }: { pose: MascotPose; height?: number; alt?: string } = $props();
</script>

<img src={SRC[pose]} {alt} style="height:{height}px;width:auto" />
