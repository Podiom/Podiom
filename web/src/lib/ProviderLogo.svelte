<script lang="ts">
  import { providerMeta } from "./providers";
  import type { Provider } from "./providers";

  let { provider, size = 16 }: { provider: Provider | string; size?: number } = $props();

  const Logo = $derived(providerMeta(provider).logo);
</script>

<Logo {size} />
