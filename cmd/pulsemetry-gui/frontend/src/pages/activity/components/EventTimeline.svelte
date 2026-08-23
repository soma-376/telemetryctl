<script lang="ts">
  import type { ActivityEvent } from "../data";

  let { events }: { events: ActivityEvent[] } = $props();
</script>

<div
  class="bg-surface border-border flex flex-col border"
  style="border-radius:12px;padding:14px 16px"
>
  <div
    class="text-text-secondary font-semibold"
    style="font-size:12.5px;margin-bottom:10px"
  >
    최근 작업
  </div>
  {#each events as ev (ev.time + ev.text)}
    <div
      class="grid items-center"
      style="grid-template-columns:44px 8px minmax(0,1fr);gap:9px;padding:5px 0"
    >
      <span
        class="text-text-secondary"
        style="font-size:12px;font-variant-numeric:tabular-nums">{ev.time}</span
      >
      <span
        class="inline-block"
        style="width:6px;height:6px;border-radius:50%;background:{ev.dot}"
      ></span>
      <span
        class="text-text overflow-hidden text-ellipsis whitespace-nowrap"
        style="font-size:12.5px">{ev.text}</span
      >
    </div>
  {/each}
  <button
    type="button"
    class="bg-surface border-border text-text hover:border-border-strong w-full cursor-pointer border"
    style="font-size:12px;font-weight:500;padding:9px;border-radius:9px;margin-top:10px"
    >모든 이벤트 보기</button
  >
</div>
