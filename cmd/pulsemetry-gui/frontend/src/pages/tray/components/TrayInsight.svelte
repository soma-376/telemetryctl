<script lang="ts">
  import Mascot from "$lib/components/ui/Mascot.svelte";
</script>

<aside class="flex items-center" style="gap:10px;padding:0 14px 10px">
  <span class="flex-none self-end"><Mascot pose="normal" height={44} /></span>
  <div
    style="flex:1;min-width:0;font-size:11.5px;line-height:1.6;color:#5d5852;word-break:keep-all"
  >
    Codex 주간 한도가 <strong class="text-accent" style="font-weight:700">55%</strong
    >로<br />가장 빠듯해. 오늘은 여유 있어.
  </div>
</aside>
