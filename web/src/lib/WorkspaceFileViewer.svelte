<script lang="ts">
  import { tick } from "svelte";
  import { getWorkspaceFileSnapshot, WorkspaceFileRequestError } from "./api";
  import AgentMarkdown from "./AgentMarkdown.svelte";
  import type { WorkspaceFileSnapshot } from "./types";
  import { closeWorkspaceFile, workspaceFileViewer } from "./workspaceFiles.svelte";

  let snapshot = $state<WorkspaceFileSnapshot | null>(null);
  let loading = $state(false);
  let error = $state("");
  let notFound = $state(false);
  let tab = $state<"preview" | "raw">("preview");
  let copied = $state("");
  let dialog = $state<HTMLDivElement | null>(null);
  let requestVersion = 0;

  const markdown = $derived(!!snapshot && /\.(md|markdown)$/i.test(snapshot.Filename));

  $effect(() => {
    const id = $workspaceFileViewer;
    if (!id) {
      requestVersion++;
      snapshot = null;
      error = "";
      loading = false;
      return;
    }
    void load(id);
  });

  async function load(id: string) {
    const version = ++requestVersion;
    loading = true;
    error = "";
    notFound = false;
    snapshot = null;
    tab = "preview";
    copied = "";
    await tick();
    dialog?.focus();
    try {
      const result = await getWorkspaceFileSnapshot(id);
      if (version === requestVersion) snapshot = result;
    } catch (e) {
      if (version === requestVersion) {
        notFound = e instanceof WorkspaceFileRequestError && e.status === 404;
        error = e instanceof Error ? e.message.trim() : String(e);
      }
    } finally {
      if (version === requestVersion) loading = false;
    }
  }

  function sizeLabel(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} KiB`;
  }

  function dateLabel(value: string): string {
    const date = new Date(value.includes("Z") ? value : value + "Z");
    return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
  }

  async function copyContent() {
    if (!snapshot) return;
    try {
      await navigator.clipboard.writeText(snapshot.Content);
      copied = "Copied";
    } catch {
      copied = "Could not copy";
    }
  }

  function keydown(event: KeyboardEvent) {
    if (event.key === "Escape" && $workspaceFileViewer) closeWorkspaceFile();
  }

  function dialogKeydown(event: KeyboardEvent) {
    if (event.key === "Escape") {
      event.preventDefault();
      closeWorkspaceFile();
      return;
    }
    if (event.key !== "Tab" || !dialog) return;
    const focusable = Array.from(dialog.querySelectorAll<HTMLElement>('button:not([disabled]), [href], input:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'));
    if (focusable.length === 0) {
      event.preventDefault();
      dialog.focus();
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
</script>

<svelte:window onkeydown={keydown} />

{#if $workspaceFileViewer}
  <div class="modal-backdrop snapshot-backdrop" role="presentation" onclick={closeWorkspaceFile}>
    <div bind:this={dialog} class="modal-card snapshot-modal" role="dialog" aria-modal="true" aria-label="Workspace file" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={dialogKeydown}>
      <div class="snapshot-head">
        <div class="snapshot-heading">
          <span class="snapshot-icon">▤</span>
          <div>
            <div class="snapshot-title">{snapshot?.Label || "Workspace file"}</div>
            {#if snapshot}<div class="snapshot-path">{snapshot.SourcePath}</div>{/if}
          </div>
        </div>
        <button class="close" type="button" aria-label="Close workspace file" onclick={closeWorkspaceFile}>×</button>
      </div>

      <div class="snapshot-body">
        {#if loading}
          <div class="state">Loading file…</div>
        {:else if error}
          <div class="state error">
            {notFound ? "This workspace file snapshot was not found." : "Podiom could not load this workspace file."}
            <small>{error}</small>
          </div>
        {:else if snapshot}
          <div class="metadata">
            <span>{snapshot.Filename}</span>
            <span>{sizeLabel(snapshot.SizeBytes)}</span>
            <span>shared by {snapshot.CreatorAgent || "agent"}</span>
            <span>{dateLabel(snapshot.CreatedAt)}</span>
          </div>
          <div class="toolbar">
            {#if markdown}
              <div class="tabs">
                <button class:active={tab === "preview"} onclick={() => (tab = "preview")}>Preview</button>
                <button class:active={tab === "raw"} onclick={() => (tab = "raw")}>Raw</button>
              </div>
            {/if}
            <button class="copy" type="button" onclick={copyContent}>{copied || "Copy content"}</button>
          </div>
          <div class="content">
            {#if markdown && tab === "preview"}
              <div class="preview"><AgentMarkdown content={snapshot.Content} /></div>
            {:else}
              <pre>{snapshot.Content}</pre>
            {/if}
          </div>
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .snapshot-backdrop { padding: 12px; }
  .snapshot-modal { width: min(880px, 96vw); max-height: min(860px, calc(100dvh - 24px)); }
  .snapshot-head { display: flex; align-items: flex-start; gap: 16px; border-bottom: 1px solid var(--line-3); padding: 18px 20px; }
  .snapshot-heading { display: flex; min-width: 0; flex: 1; align-items: flex-start; gap: 10px; }
  .snapshot-icon { display: grid; width: 32px; height: 32px; flex: none; place-items: center; border-radius: 10px; background: #e7f1ed; color: #2f7868; }
  .snapshot-title { color: var(--ink); font: 700 16px "Hanken Grotesk"; overflow-wrap: anywhere; }
  .snapshot-path { margin-top: 3px; color: var(--faint); font: 500 11px "JetBrains Mono", monospace; overflow-wrap: anywhere; }
  .close { display: grid; width: 32px; height: 32px; flex: none; place-items: center; border: 0; border-radius: 9px; background: var(--surface-3); color: var(--muted); cursor: pointer; font-size: 21px; }
  .snapshot-modal > .snapshot-body { min-height: 0; flex: 1 1 auto; overflow: auto; overscroll-behavior: contain; padding: 16px 20px 20px; }
  .metadata { display: flex; flex-wrap: wrap; gap: 7px 16px; color: var(--faint); font: 500 10.5px "JetBrains Mono", monospace; }
  .toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin: 14px 0 8px; }
  .tabs { display: flex; gap: 4px; border-radius: 9px; padding: 3px; background: var(--surface-3); }
  .tabs button, .copy { border: 0; border-radius: 7px; padding: 7px 11px; background: transparent; color: var(--muted); cursor: pointer; font: 600 11.5px "Hanken Grotesk"; }
  .tabs button.active { background: var(--surface); color: var(--teal-deep); box-shadow: 0 1px 4px rgba(43, 37, 32, 0.12); }
  .copy { margin-left: auto; border: 1px solid #c9ded7; background: #edf5f2; color: #286b5d; }
  .content { min-height: 180px; border: 1px solid var(--line-3); border-radius: 13px; background: #fffdfb; overflow: hidden; }
  .preview { padding: 18px 20px; color: var(--ink); font: 400 14px/1.65 "Hanken Grotesk"; overflow-wrap: anywhere; }
  .preview :global(:first-child) { margin-top: 0; }
  .preview :global(:last-child) { margin-bottom: 0; }
  .preview :global(pre) { overflow-x: auto; border-radius: 9px; padding: 12px; background: var(--surface-3); }
  pre { min-height: 180px; max-height: 62vh; margin: 0; overflow: auto; padding: 16px 18px; color: #423a33; white-space: pre-wrap; overflow-wrap: anywhere; font: 500 12px/1.65 "JetBrains Mono", monospace; }
  .state { display: grid; min-height: 240px; place-items: center; color: var(--muted); text-align: center; font: 600 14px "Hanken Grotesk"; }
  .state.error { color: #a94e31; }
  .state small { display: block; max-width: 560px; margin-top: 8px; color: var(--faint); font: 500 11px/1.5 "JetBrains Mono", monospace; }
  @media (max-width: 600px) {
    .snapshot-head { padding: 15px; }
    .snapshot-body { padding: 13px 15px 16px; }
    .metadata { display: grid; gap: 4px; }
    .preview { padding: 15px; }
  }
</style>
