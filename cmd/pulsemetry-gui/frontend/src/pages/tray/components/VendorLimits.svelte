<script lang="ts">
  import type { TrayVendor } from "../types";
  import VendorLimitCard from "./VendorLimitCard.svelte";

  let { vendors }: { vendors: TrayVendor[] } = $props();
</script>

<section style="padding:10px 14px 3px" aria-label="벤더 한도">
  {#each vendors as vendor (vendor.id)}
    <VendorLimitCard {vendor} />
  {/each}
</section>
