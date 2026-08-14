<script lang="ts">
  type BadgeState =
    | "active"
    | "complete"
    | "paused"
    | "warning"
    | "error"
    | "offline";

  let { state }: { state: BadgeState | string } = $props();

  const STYLE: Record<BadgeState, { label: string; fg: string; bg: string }> = {
    active: { label: "진행 중", fg: "var(--color-accent-hover)", bg: "var(--color-accent-soft)" },
    complete: { label: "완료", fg: "var(--color-success)", bg: "var(--color-success-soft)" },
    paused: { label: "대기 중", fg: "var(--color-warning)", bg: "var(--color-warning-soft)" },
    warning: { label: "주의", fg: "var(--color-warning)", bg: "var(--color-warning-soft)" },
    error: { label: "오류", fg: "var(--color-danger)", bg: "var(--color-danger-soft)" },
    offline: { label: "연결 안됨", fg: "var(--color-inactive)", bg: "var(--color-inactive-soft)" },
  };

  const s = $derived(STYLE[state as BadgeState] ?? STYLE.active);
</script>

<span
  class="inline-block font-semibold whitespace-nowrap"
  style="font-size:11.5px;border-radius:8px;padding:6px 11px;color:{s.fg};background:{s.bg}"
>
  {s.label}
</span>
