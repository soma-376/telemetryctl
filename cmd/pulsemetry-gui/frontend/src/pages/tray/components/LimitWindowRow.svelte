<script lang="ts">
  import type { TrayLimitWindow } from "../types";
  import { limitTone } from "../tray";

  let { window, accent }: { window: TrayLimitWindow; accent: string } =
    $props();
  const tone = $derived(limitTone(window.pct, accent));
</script>

<div style="margin-bottom:10px;animation:rowIn 200ms ease-out">
  <div
    class="grid items-center"
    style="grid-template-columns:minmax(0,1fr) auto 42px;gap:8px;margin-bottom:6px"
  >
    <span
      class="text-text-secondary truncate"
      style="font-size:12.5px;min-width:0">{window.label}</span
    >
    <span class="whitespace-nowrap" style="font-size:10.5px;color:#b3aba0">
      {window.reset}
    </span>
    <span
      class="text-right font-bold whitespace-nowrap"
      style="font-size:13px;font-variant-numeric:tabular-nums;color:{tone.value}"
      >{window.remain}</span
    >
  </div>
  <div class="bg-track" style="height:5px;border-radius:999px">
    <div
      style="height:100%;border-radius:999px;background:{tone.bar};width:{window.pct}%"
    ></div>
  </div>
</div>
