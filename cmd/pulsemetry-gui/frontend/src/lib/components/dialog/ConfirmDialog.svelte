<script lang="ts">
  let {
    open,
    title,
    message,
    confirmLabel = "확인",
    cancelLabel = "취소",
    danger = false,
    onConfirm,
    onCancel,
  }: {
    open: boolean;
    title: string;
    message: string;
    confirmLabel?: string;
    cancelLabel?: string;
    danger?: boolean;
    onConfirm: () => void;
    onCancel: () => void;
  } = $props();

  // 확인 다이얼로그는 실수 방지가 목적이므로 열릴 때 포커스를 취소 버튼에 둔다.
  // Enter=취소, 확정은 반드시 명시적 클릭/탭 이동으로만.
  let cancelButton = $state<HTMLButtonElement | null>(null);
  $effect(() => {
    if (open) cancelButton?.focus();
  });
</script>

<svelte:window
  onkeydown={(e) => {
    if (open && e.key === "Escape") {
      e.preventDefault();
      onCancel();
    }
  }}
/>

{#if open}
  <!-- 설정 모달(z-60) 위에서도 뜰 수 있도록 한 층 위에 둔다 -->
  <div
    class="fixed inset-0 flex items-center justify-center"
    style="z-index:70"
  >
    <button
      type="button"
      aria-label={cancelLabel}
      onclick={onCancel}
      class="absolute inset-0 cursor-default border-none"
      style="background:rgba(27,26,24,0.22);animation:fadeIn 160ms ease-out"
    ></button>

    <div
      class="bg-surface border-border relative flex flex-col border"
      role="alertdialog"
      aria-modal="true"
      aria-label={title}
      style="width:340px;max-width:calc(100vw - 48px);border-radius:16px;box-shadow:0 18px 48px rgba(27,26,24,0.16);animation:popIn 200ms cubic-bezier(0.32,0.72,0,1);padding:20px 22px 18px"
    >
      <div
        class="text-text font-bold"
        style="font-size:15px;letter-spacing:-0.01em;margin-bottom:8px"
      >
        {title}
      </div>
      <div
        class="text-text-secondary whitespace-pre-line"
        style="font-size:12.5px;line-height:1.6;margin-bottom:18px"
      >
        {message}
      </div>
      <div class="flex justify-end" style="gap:8px">
        <button
          bind:this={cancelButton}
          type="button"
          onclick={onCancel}
          class="border-border text-text hover:border-border-strong cursor-pointer border bg-transparent font-semibold whitespace-nowrap transition-colors duration-[120ms] ease-in-out"
          style="border-radius:9px;padding:8px 14px;font-size:12.5px"
        >
          {cancelLabel}
        </button>
        <button
          type="button"
          onclick={onConfirm}
          class="{danger
            ? 'bg-danger hover:bg-danger-strong'
            : 'bg-accent hover:bg-accent-hover'} cursor-pointer border-none font-semibold whitespace-nowrap transition-colors duration-[120ms] ease-in-out"
          style="color:#ffffff;border-radius:9px;padding:8px 14px;font-size:12.5px"
        >
          {confirmLabel}
        </button>
      </div>
    </div>
  </div>
{/if}
