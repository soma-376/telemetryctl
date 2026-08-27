<script lang="ts">
  import ClockIcon from "../../../lib/icons/ClockIcon.svelte";
  import PulseIcon from "../../../lib/icons/PulseIcon.svelte";
  import RefreshIcon from "../../../lib/icons/RefreshIcon.svelte";
  import CheckIcon from "../../../lib/icons/CheckIcon.svelte";

  let { kpi }: { kpi: string[] } = $props();

  const tile = "bg-surface border-border border flex flex-col";
</script>

<div
  class="grid"
  style="grid-template-columns:repeat(4,minmax(0,1fr));gap:10px"
>
  <div class={tile} style="border-radius:11px;padding:12px 13px">
    <ClockIcon
      size={16}
      class="text-text-secondary"
      style="margin-bottom:8px"
    />
    <div
      class="text-text"
      style="font-size:19px;font-weight:700;letter-spacing:-0.02em;margin-bottom:4px"
    >
      {kpi[0]}
    </div>
    <div class="text-text-secondary whitespace-nowrap" style="font-size:11.5px">
      소요 시간
    </div>
  </div>
  <div class={tile} style="border-radius:11px;padding:12px 13px">
    <PulseIcon
      size={16}
      style="color:var(--color-info);margin-bottom:8px"
    />
    <div
      class="text-text"
      style="font-size:19px;font-weight:700;letter-spacing:-0.02em;margin-bottom:4px"
    >
      {kpi[1]}
    </div>
    <div class="text-text-secondary whitespace-nowrap" style="font-size:11.5px">
      토큰 사용량
    </div>
  </div>
  <div class={tile} style="border-radius:11px;padding:12px 13px">
    <svg
      viewBox="0 0 24 24"
      style="width:16px;height:16px;margin-bottom:8px"
      fill="none"
      stroke="var(--color-text-secondary)"
      stroke-width="1.9"
      stroke-linecap="round"
      stroke-linejoin="round"
      ><path d="M14.5 4.5a3.5 3.5 0 0 1 5 5L9 20l-5 1 1-5z"></path></svg
    >
    <div
      class="text-text"
      style="font-size:19px;font-weight:700;letter-spacing:-0.02em;margin-bottom:4px"
    >
      {kpi[2]}
    </div>
    <div class="text-text-secondary whitespace-nowrap" style="font-size:11.5px">
      툴 호출 수
    </div>
  </div>
  <div class={tile} style="border-radius:11px;padding:12px 13px">
    <RefreshIcon
      size={16}
      strokeWidth={1.9}
      style="color:var(--color-danger);margin-bottom:8px"
    />
    <div
      class="text-text"
      style="font-size:19px;font-weight:700;letter-spacing:-0.02em;margin-bottom:4px"
    >
      {kpi[3]}
    </div>
    <div class="text-text-secondary whitespace-nowrap" style="font-size:11.5px">
      재시도
    </div>
  </div>
  <div class={tile} style="border-radius:11px;padding:12px 13px">
    <div
      style="width:16px;height:16px;margin-bottom:8px;color:var(--color-success);font-size:15px;font-weight:700;line-height:1"
    >
      $
    </div>
    <div
      class="text-text"
      style="font-size:19px;font-weight:700;letter-spacing:-0.02em;margin-bottom:4px"
    >
      {kpi[4]}
    </div>
    <div class="text-text-secondary whitespace-nowrap" style="font-size:11.5px">
      예상 비용
    </div>
  </div>
  <div class={tile} style="border-radius:11px;padding:12px 13px">
    <svg
      viewBox="0 0 24 24"
      style="width:16px;height:16px;margin-bottom:8px"
      fill="none"
      stroke="var(--color-session)"
      stroke-width="1.9"
      stroke-linecap="round"
      stroke-linejoin="round"
      ><rect x="3" y="5" width="18" height="14" rx="3"></rect><path
        d="M7 12h5l1.5-2.5L15 14l1-2h1"
      ></path></svg
    >
    <div
      class="text-text"
      style="font-size:19px;font-weight:700;letter-spacing:-0.02em;margin-bottom:4px"
    >
      {kpi[5]}
    </div>
    <div class="text-text-secondary whitespace-nowrap" style="font-size:11.5px">
      캐시 리드
    </div>
  </div>
  <div class={tile} style="border-radius:11px;padding:12px 13px">
    <svg
      viewBox="0 0 24 24"
      style="width:16px;height:16px;margin-bottom:8px"
      fill="none"
      stroke="var(--color-session)"
      stroke-width="1.9"
      stroke-linecap="round"
      stroke-linejoin="round"
      ><path d="M12 4v9"></path><path d="m8.5 9.5 3.5 3.5 3.5-3.5"></path><path
        d="M4 16v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"
      ></path></svg
    >
    <div
      class="text-text"
      style="font-size:19px;font-weight:700;letter-spacing:-0.02em;margin-bottom:4px"
    >
      {kpi[6]}
    </div>
    <div class="text-text-secondary whitespace-nowrap" style="font-size:11.5px">
      캐시 라이트
    </div>
  </div>
  <div
    class="flex flex-col border"
    style="background:var(--color-success-soft);border-color:#c9e7d6;border-radius:11px;padding:12px 13px"
  >
    <CheckIcon style="color:var(--color-success);margin-bottom:8px" />
    <div
      style="font-size:19px;font-weight:700;letter-spacing:-0.02em;margin-bottom:4px;color:#1b7c46"
    >
      {kpi[7]}
    </div>
    <div class="whitespace-nowrap" style="font-size:11.5px;color:#2f7e55">
      캐시 절감
    </div>
  </div>
</div>
