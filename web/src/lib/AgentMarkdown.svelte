<script lang="ts">
  import { tick } from "svelte";
  import { renderMarkdown } from "./markdown";
  import { openWorkspaceFile, workspaceFileIDFromHref } from "./workspaceFiles.svelte";

  let { content, className = "" }: { content: string; className?: string } = $props();
  let root = $state<HTMLDivElement | null>(null);
  const html = $derived(renderMarkdown(content ?? ""));

  $effect(() => {
    html;
    void markWorkspaceFileLinks();
  });

  async function markWorkspaceFileLinks() {
    await tick();
    for (const anchor of root?.querySelectorAll("a") ?? []) {
      const workspaceFile = !!workspaceFileIDFromHref(anchor.getAttribute("href") ?? "");
      anchor.classList.toggle("workspace-file-link", workspaceFile);
      if (workspaceFile) {
        anchor.removeAttribute("target");
        anchor.removeAttribute("rel");
      } else {
        anchor.target = "_blank";
        anchor.rel = "noopener noreferrer";
      }
    }
  }

  function handleClick(event: MouseEvent) {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const target = event.target;
    if (!(target instanceof Element)) return;
    const anchor = target.closest("a");
    if (!anchor) return;
    const id = workspaceFileIDFromHref(anchor.getAttribute("href") ?? "");
    if (!id) return;
    event.preventDefault();
    openWorkspaceFile(id);
  }
</script>

<div bind:this={root} class={`agent-markdown ${className}`} role="presentation" onclick={handleClick} onkeydown={() => {}}>{@html html}</div>

<style>
  .agent-markdown { min-width: 0; }
  .agent-markdown :global(a.workspace-file-link) {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    max-width: 100%;
    border: 1px solid #c9ded7;
    border-radius: 9px;
    padding: 3px 8px;
    background: #edf5f2;
    color: #286b5d;
    text-decoration: none;
    font: 600 12px "JetBrains Mono", monospace;
    vertical-align: middle;
  }
  .agent-markdown :global(a.workspace-file-link::before) {
    content: "▤";
    flex: none;
    font-size: 12px;
  }
  .agent-markdown :global(a.workspace-file-link:hover) {
    border-color: #82b4a7;
    background: #e2f0eb;
  }
</style>
