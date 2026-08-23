<script lang="ts">
  import { onMount } from "svelte";
  import { installToolsetTool, listToolset, removeToolsetTool } from "../lib/api";
  import ConfirmModal from "../lib/ConfirmModal.svelte";
  import type { ToolsetEntry } from "../lib/types";

  // Declared locally, as the sibling pages do — ChatTarget is App.svelte's own
  // shape and is not exported from lib/types.
  interface ChatTarget {
    sessionId?: string;
    agentName?: string;
    seed?: string;
  }

  let { onOpenChat = (_t: ChatTarget) => {} }: { onOpenChat?: (t: ChatTarget) => void } = $props();

  let tools = $state<ToolsetEntry[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let pendingDelete = $state<ToolsetEntry | null>(null);
  let deleteBusy = $state(false);
  // The tool currently being reinstalled, so only its own row shows a spinner.
  let reinstalling = $state<string | null>(null);

  async function refresh() {
    try {
      tools = await listToolset();
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load the toolset.";
    } finally {
      loading = false;
    }
  }

  // reinstall replays the manifest's own spec. Nothing new is decided here —
  // an entry marked needs_reinstall already knows its installer and package,
  // which is what makes a migrated tool a one-click restore.
  async function reinstall(t: ToolsetEntry) {
    if (reinstalling) return;
    reinstalling = t.tool;
    error = null;
    try {
      await installToolsetTool({
        tool: t.tool,
        installer: t.installer,
        package: t.package,
        version: t.version,
        url: t.url,
        sha256: t.sha256,
        path: t.path,
      });
      await refresh();
    } catch (e) {
      error = e instanceof Error ? e.message : `Could not reinstall ${t.tool}.`;
    } finally {
      reinstalling = null;
    }
  }

  async function confirmDelete() {
    if (!pendingDelete) return;
    deleteBusy = true;
    try {
      await removeToolsetTool(pendingDelete.tool);
      pendingDelete = null;
      await refresh();
    } catch (e) {
      error = e instanceof Error ? e.message : "Remove failed.";
      pendingDelete = null;
    } finally {
      deleteBusy = false;
    }
  }

  function fmtDate(iso?: string): string {
    if (!iso) return "—";
    const d = new Date(iso);
    return isNaN(d.getTime()) ? iso : d.toLocaleDateString();
  }

  // What the tool was installed from, in the terms the installer uses.
  function source(t: ToolsetEntry): string {
    if (t.package) return t.version ? `${t.package}@${t.version}` : t.package;
    if (t.url) {
      try {
        return new URL(t.url).pathname.split("/").pop() || t.url;
      } catch {
        return t.url;
      }
    }
    return "—";
  }

  onMount(() => {
    void refresh();
  });
</script>

<section class="card">
  <div class="card-head">
    <div class="card-icon amber">
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a4 4 0 0 0 5 5l-9 9a2.8 2.8 0 0 1-4-4l9-9z"/><path d="M14.7 6.3 18 3l3 3-3.3 3.3"/></svg>
    </div>
    <div>
      <div class="card-title">Toolset</div>
      <div class="card-sub">
        Command-line tools your agents installed for themselves. They live in one shared directory on this
        machine, on the PATH of every agent session — and, in the Home Assistant app, they survive app updates.
      </div>
    </div>
  </div>

  {#if error}
    <div class="error-banner" style="margin-bottom:12px">{error}</div>
  {/if}

  {#if loading}
    <div class="empty">Loading…</div>
  {:else if tools.length === 0}
    <div class="empty">
      No tools installed. Agents add them here themselves when work needs one — nothing to set up.
    </div>
  {:else}
    <table class="tool-table">
      <thead>
        <tr><th>Tool</th><th>Source</th><th>Version</th><th>Installed by</th><th>Added</th><th></th></tr>
      </thead>
      <tbody>
        {#each tools as t (t.tool)}
          <tr class:pending={t.needs_reinstall}>
            <td>
              <span class="mono tool-name">{t.tool}</span>
              <span class="installer">{t.installer}</span>
              {#if t.needs_reinstall}
                <span class="flag pending-flag" title="Carried over from the old per-agent layout. Podiom knows how it was installed but has no files for it yet.">needs reinstall</span>
              {:else if t.broken}
                <span class="flag broken-flag" title="The manifest lists this tool but its executable is missing on disk.">broken</span>
              {/if}
            </td>
            <td class="mono src">{source(t)}</td>
            <td class="mono version">{t.version_output || "—"}</td>
            <td>
              {#if t.installed_by}
                <button
                  class="by-agent"
                  disabled={!t.session_id}
                  title={t.session_id ? "Open the conversation this tool was installed in" : "Installed by an agent"}
                  onclick={() => t.session_id && onOpenChat({ sessionId: t.session_id })}
                >{t.installed_by}</button>
              {:else}
                <span class="by-you">—</span>
              {/if}
            </td>
            <td>{fmtDate(t.installed_at)}</td>
            <td class="actions">
              {#if t.needs_reinstall}
                <button class="btn" disabled={reinstalling !== null} onclick={() => reinstall(t)}>
                  {reinstalling === t.tool ? "Installing…" : "Reinstall"}
                </button>
              {/if}
              <button class="btn danger" onclick={() => (pendingDelete = t)}>Remove</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
    <div class="hint">
      Every agent shares this toolset, so removing one takes it away from all of them. Agents can add and
      replace tools; only you can remove them.
    </div>
  {/if}
</section>

{#if pendingDelete}
  <ConfirmModal
    title="Remove this tool?"
    message={`${pendingDelete.tool} is uninstalled and drops off every agent's PATH. Any agent still relying on it will have to install it again.`}
    confirmLabel="Remove tool"
    busy={deleteBusy}
    onConfirm={confirmDelete}
    onCancel={() => (pendingDelete = null)} />
{/if}

<style>
  /* Card chrome, matching the sibling cards on the Settings tab this renders
     inside — component-scoped styles do not reach here from the host. */
  .card {
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 20px;
    padding: 24px 26px;
    margin-bottom: 18px;
  }

  .card-head {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .card-icon {
    width: 38px;
    height: 38px;
    border-radius: 12px;
    display: flex;
    align-items: center;
    justify-content: center;
    flex: none;
  }

  .card-icon.amber {
    background: #f6efe2;
    color: #8a6a2f;
  }

  .card-title {
    font: 800 17px "Hanken Grotesk";
    letter-spacing: -0.01em;
  }

  .card-sub {
    font: 400 12.5px "Hanken Grotesk";
    color: var(--muted-2);
  }

  .tool-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13.5px;
    margin-top: 14px;
  }
  .tool-table th {
    text-align: left;
    font-weight: 600;
    padding: 8px 10px;
    border-bottom: 1px solid var(--border, #e3e0da);
    color: var(--text-dim, #6f6a61);
    font-size: 12px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .tool-table td {
    padding: 10px;
    border-bottom: 1px solid var(--border, #eeebe5);
    vertical-align: middle;
  }
  .tool-table tr.pending td {
    color: var(--text-dim, #6f6a61);
  }
  .tool-table .actions {
    text-align: right;
    white-space: nowrap;
  }
  .tool-name {
    font-weight: 600;
    color: var(--ink);
  }
  .installer {
    margin-left: 7px;
    font: 600 11px "JetBrains Mono", monospace;
    color: var(--text-dim, #6f6a61);
  }
  .src,
  .version {
    font-size: 12px;
    color: var(--text-dim, #6f6a61);
    overflow-wrap: anywhere;
  }
  .flag {
    margin-left: 7px;
    font: 600 10.5px "Hanken Grotesk";
    padding: 2px 8px;
    border-radius: 999px;
    white-space: nowrap;
  }
  .pending-flag {
    background: #f2eee2;
    color: #7a6a33;
    border: 1px solid #e2dac2;
  }
  .broken-flag {
    background: #fbeae0;
    color: #b14e2a;
    border: 1px solid #f2d6c5;
  }
  .by-agent {
    border: 1px solid var(--line-2);
    background: var(--surface-3, #fbfaf8);
    border-radius: 999px;
    padding: 3px 10px;
    font: 600 12px "Hanken Grotesk";
    color: var(--ink);
  }
  .by-agent:disabled {
    cursor: default;
  }
  .by-you {
    color: var(--text-dim, #6f6a61);
  }
  .hint {
    margin-top: 10px;
    font-size: 12.5px;
    color: var(--text-dim, #6f6a61);
  }
  .empty {
    padding: 18px 4px;
    color: var(--text-dim, #6f6a61);
    font-size: 13.5px;
  }
</style>
