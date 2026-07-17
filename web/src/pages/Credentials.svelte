<script lang="ts">
  import { onMount } from "svelte";
  import { deleteCredential, listCredentials } from "../lib/api";
  import ConfirmModal from "../lib/ConfirmModal.svelte";
  import type { CredentialInfo } from "../lib/types";

  let credentials = $state<CredentialInfo[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let pendingDelete = $state<CredentialInfo | null>(null);
  let deleteBusy = $state(false);

  async function refresh() {
    try {
      credentials = await listCredentials();
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load credentials.";
    } finally {
      loading = false;
    }
  }

  async function confirmDelete() {
    if (!pendingDelete) return;
    deleteBusy = true;
    try {
      await deleteCredential(pendingDelete.name);
      pendingDelete = null;
      await refresh();
    } catch (e) {
      error = e instanceof Error ? e.message : "Delete failed.";
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

  onMount(() => {
    void refresh();
  });
</script>

<section class="card">
  <div class="card-head">
    <div class="card-icon teal">
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0 3 3L22 7l-3-3m-3.5 3.5L19 4"/></svg>
    </div>
    <div>
      <div class="card-title">Credentials</div>
      <div class="card-sub">Secrets you granted to agents via access requests. Values are stored on this machine, injected into agent environments, and never shown again.</div>
    </div>
  </div>

  {#if error}
    <div class="error-banner" style="margin-bottom:12px">{error}</div>
  {/if}

  {#if loading}
    <div class="empty">Loading…</div>
  {:else if credentials.length === 0}
    <div class="empty">No stored credentials. When you approve an agent's credential request with a value, it appears here.</div>
  {:else}
    <table class="cred-table">
      <thead>
        <tr><th>Name</th><th>Purpose</th><th>Goal</th><th>Added</th><th></th></tr>
      </thead>
      <tbody>
        {#each credentials as c (c.name)}
          <tr>
            <td class="mono">{c.name}</td>
            <td>{c.purpose || "—"}</td>
            <td class="mono">{c.goal_id || "—"}</td>
            <td>{fmtDate(c.created_at)}</td>
            <td class="actions"><button class="btn danger" onclick={() => (pendingDelete = c)}>Delete</button></td>
          </tr>
        {/each}
      </tbody>
    </table>
    <div class="hint">To rotate a value, delete it here and have the agent re-request it, or approve a new request for the same name.</div>
  {/if}
</section>

{#if pendingDelete}
  <ConfirmModal
    title="Delete this credential?"
    message={`${pendingDelete.name} is removed from Podiom's store and disappears from agent environments on their next run. This cannot be undone.`}
    confirmLabel="Delete credential"
    busy={deleteBusy}
    onConfirm={confirmDelete}
    onCancel={() => (pendingDelete = null)} />
{/if}

<style>
  .cred-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13.5px;
  }
  .cred-table th {
    text-align: left;
    font-weight: 600;
    padding: 8px 10px;
    border-bottom: 1px solid var(--border, #e3e0da);
    color: var(--text-dim, #6f6a61);
    font-size: 12px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .cred-table td {
    padding: 10px;
    border-bottom: 1px solid var(--border, #eeebe5);
    vertical-align: middle;
  }
  .cred-table .actions {
    text-align: right;
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
