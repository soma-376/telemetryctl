<script lang="ts">
  let { label }: { label: string } = $props();
</script>

<div class="flex items-baseline" style="gap:12px;margin-bottom:14px">
  <h1
    class="text-text m-0 font-bold"
    style="font-size:38px;letter-spacing:-0.035em;line-height:1"
  >
    Home
  </h1>
  <div class="text-text-muted" style="font-size:14px">({label})</div>
</div>
