<script lang="ts">
  let { kpi }: { kpi: string[] } = $props();

  const tile = "bg-surface border-border border flex flex-col";
</script>

<div
  class="grid"
  style="grid-template-columns:repeat(4,minmax(0,1fr));gap:10px"
>
  <div class={tile} style="border-radius:11px;padding:12px 13px">
    <svg
      viewBox="0 0 24 24"
      style="width:16px;height:16px;margin-bottom:8px"
      fill="none"
      stroke="var(--color-text-secondary)"
      stroke-width="1.9"
      stroke-linecap="round"
      ><circle cx="12" cy="12" r="8.5"></circle><path d="M12 7.5V12l3 2"
      ></path></svg
    >
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
    <svg
      viewBox="0 0 24 24"
      style="width:16px;height:16px;margin-bottom:8px"
      fill="none"
      stroke="var(--color-info)"
      stroke-width="1.9"
      stroke-linecap="round"
      stroke-linejoin="round"><path d="M3 13h3l2.5-7 4 14 2.5-7H21"></path></svg
    >
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
    <svg
      viewBox="0 0 24 24"
      style="width:16px;height:16px;margin-bottom:8px"
      fill="none"
      stroke="var(--color-danger)"
      stroke-width="1.9"
      stroke-linecap="round"
      ><path d="M20 12a8 8 0 1 1-2.4-5.7"></path><path d="M20 4v4h-4"
      ></path></svg
    >
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
    <svg
      viewBox="0 0 24 24"
      style="width:16px;height:16px;margin-bottom:8px"
      fill="none"
      stroke="var(--color-success)"
      stroke-width="1.9"
      stroke-linecap="round"
      stroke-linejoin="round"><path d="M20 7 10 17l-6-6"></path></svg
    >
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
