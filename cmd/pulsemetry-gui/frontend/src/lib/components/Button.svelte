<script lang="ts">
  import type { Snippet } from "svelte";

  let {
    variant = "primary",
    disabled = false,
    onclick,
    children,
  }: {
    variant?: "primary" | "info" | "secondary";
    disabled?: boolean;
    onclick?: () => void;
    children: Snippet;
  } = $props();

  const VARIANT = {
    primary:
      "bg-accent text-surface border border-transparent hover:bg-accent-hover",
    info: "bg-info text-surface border border-transparent hover:bg-info-hover",
    secondary:
      "bg-surface text-text border border-border hover:border-border-strong",
  } as const;
</script>

<button
  {disabled}
  {onclick}
  class="cursor-pointer rounded-[10px] font-semibold transition-colors duration-[120ms] ease-in-out disabled:cursor-not-allowed disabled:opacity-50 {VARIANT[
    variant
  ]}"
  style="font-size:14px;padding:11px 18px;font-family:inherit"
>
  {@render children()}
</button>
