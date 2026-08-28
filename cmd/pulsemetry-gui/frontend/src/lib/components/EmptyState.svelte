<script lang="ts">
  import type { Snippet } from "svelte";
  import Mascot from "./Mascot.svelte";
  import type { MascotPose } from "../types";

  // 빈 상태. "없다"는 사실만 알리지 말고 다음에 무엇을 하면 되는지까지 담는다 —
  // 첫 실행에서는 이 화면이 사용자가 처음 만나는 안내다.
  //
  // lg = 차트나 표 본문을 통째로 대체하는 자리 (세로 중앙 정렬)
  // sm = 트레이 팝업이나 카드 안처럼 한 줄로 끝내야 하는 자리 (가로 배치)
  //
  // 포즈는 상태에 맞춰 고른다: 데이터 없음 no-data / 데몬 꺼짐 offline /
  // 수집은 되는데 아직 결과 없음 collecting-alt.
  let {
    pose = "no-data",
    title,
    description = "",
    size = "lg",
    action,
  }: {
    pose?: MascotPose;
    title: string;
    description?: string;
    size?: "lg" | "sm";
    action?: Snippet;
  } = $props();
</script>

{#if size === "lg"}
  <div
    class="flex flex-col items-center justify-center text-center"
    style="gap:14px;padding:36px 24px"
  >
    <Mascot {pose} height={88} />
    <div>
      <div class="text-text font-bold" style="font-size:15px;margin-bottom:6px">
        {title}
      </div>
      {#if description}
        <div
          class="text-text-secondary"
          style="font-size:12.5px;line-height:1.6"
        >
          {description}
        </div>
      {/if}
    </div>
    {@render action?.()}
  </div>
{:else}
  <div class="flex items-center" style="gap:10px;padding:14px 2px">
    <Mascot {pose} height={30} />
    <div style="min-width:0;flex:1">
      <div class="text-text truncate font-semibold" style="font-size:12.5px">
        {title}
      </div>
      {#if description}
        <div
          class="text-text-muted truncate"
          style="font-size:10.5px;margin-top:2px"
        >
          {description}
        </div>
      {/if}
    </div>
    {@render action?.()}
  </div>
{/if}
