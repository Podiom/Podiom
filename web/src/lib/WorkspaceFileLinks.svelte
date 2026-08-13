<script lang="ts">
  import { extractWorkspaceFileLinks, openWorkspaceFile } from "./workspaceFiles.svelte";

  let { content }: { content: string } = $props();
  const links = $derived(extractWorkspaceFileLinks(content ?? ""));
</script>

{#if links.length > 0}
  <div class="workspace-links" aria-label="Attached workspace files">
    {#each links as link (link.id)}
      <button type="button" onclick={() => openWorkspaceFile(link.id)}><span>▤</span>{link.label}</button>
    {/each}
  </div>
{/if}

<style>
  .workspace-links { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 7px; }
  button { display: inline-flex; align-items: center; gap: 6px; max-width: 100%; border: 1px solid #c9ded7; border-radius: 9px; padding: 5px 9px; background: #edf5f2; color: #286b5d; cursor: pointer; font: 600 11px "JetBrains Mono", monospace; }
  button:hover { border-color: #82b4a7; background: #e2f0eb; }
</style>
