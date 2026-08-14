<script lang="ts">
  let { label }: { label: string } = $props();
</script>

<div class="flex items-end" style="gap:12px;margin-bottom:18px">
  <h1
    class="text-text m-0 font-bold"
    style="font-size:37px;letter-spacing:-0.035em;line-height:1"
  >
    Overview
  </h1>
  <div class="text-text-secondary" style="font-size:14px;padding-bottom:6px">
    ({label})
  </div>
</div>
