<script lang="ts">
  import { onMount } from "svelte";
  import { CredentialExistsError, deleteCredential, listCredentials, storeCredential } from "../lib/api";
  import ConfirmModal from "../lib/ConfirmModal.svelte";
  import type { CredentialInfo } from "../lib/types";

  // Declared locally, as the sibling pages do — ChatTarget is App.svelte's own
  // shape and is not exported from lib/types.
  interface ChatTarget {
    sessionId?: string;
    agentName?: string;
    seed?: string;
  }

  let { onOpenChat = (_t: ChatTarget) => {} }: { onOpenChat?: (t: ChatTarget) => void } = $props();

  let credentials = $state<CredentialInfo[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let pendingDelete = $state<CredentialInfo | null>(null);
  let deleteBusy = $state(false);

  // Add form. addValue holds the secret only for as long as the request takes;
  // it is cleared the moment the call settles, either way.
  let adding = $state(false);
  let addName = $state("");
  let addValue = $state("");
  let addPurpose = $state("");
  let addBusy = $state(false);
  let addError = $state<string | null>(null);
  // Set when the daemon's overwrite guard fires: the value is held back for the
  // one retry the user confirms, and dropped if they cancel.
  let pendingReplace = $state<{ name: string; value: string; purpose: string } | null>(null);
  let replaceBusy = $state(false);

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

  function openAdd() {
    adding = true;
    addError = null;
  }

  function closeAdd() {
    adding = false;
    addName = "";
    addValue = "";
    addPurpose = "";
    addError = null;
  }

  async function submitAdd(e: Event) {
    e.preventDefault();
    if (addBusy) return;
    const name = addName.trim();
    const value = addValue;
    const purpose = addPurpose.trim();
    if (!name || !value.trim()) {
      addError = "A name and a value are both required.";
      return;
    }
    addBusy = true;
    addError = null;
    try {
      await storeCredential({ name, value, purpose });
      closeAdd();
      await refresh();
    } catch (err) {
      if (err instanceof CredentialExistsError) {
        pendingReplace = { name, value, purpose };
      } else {
        addError = err instanceof Error ? err.message : "Could not store the credential.";
      }
    } finally {
      addBusy = false;
      // The secret leaves component state as soon as the call settles. On the
      // overwrite path it lives on only in pendingReplace, for that one retry.
      addValue = "";
    }
  }

  async function confirmReplace() {
    if (!pendingReplace || replaceBusy) return;
    replaceBusy = true;
    try {
      await storeCredential({ ...pendingReplace, overwrite: true });
      pendingReplace = null;
      closeAdd();
      await refresh();
    } catch (err) {
      addError = err instanceof Error ? err.message : "Could not replace the credential.";
      pendingReplace = null;
    } finally {
      replaceBusy = false;
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
      <div class="card-sub">Secrets available to your agents — added here, stored by an agent, or granted through an access request. Values are kept on this machine, injected into agent environments, and never shown again.</div>
    </div>
    {#if !adding}
      <button class="add-btn" onclick={openAdd}>Add credential</button>
    {/if}
  </div>

  {#if adding}
    <form class="add-form" onsubmit={submitAdd}>
      <div class="add-grid">
        <label class="add-field">
          <span class="label-mono">Name</span>
          <input class="field-input mono" bind:value={addName} placeholder="GITHUB_TOKEN" autocomplete="off" spellcheck="false" />
        </label>
        <label class="add-field">
          <span class="label-mono">Value</span>
          <input class="field-input" type="password" bind:value={addValue} placeholder="The secret itself" autocomplete="new-password" />
        </label>
        <label class="add-field wide">
          <span class="label-mono">Purpose</span>
          <input class="field-input" bind:value={addPurpose} placeholder="What this is for — shown here and to your agents" />
        </label>
      </div>
      <div class="add-note">Agents see the name and purpose, never the value. It reaches their environment as <code>${addName.trim() || "NAME"}</code> on their next run.</div>
      {#if addError}<div class="error-banner" style="margin-top:12px">{addError}</div>{/if}
      <div class="add-actions">
        <button class="btn-teal add-save" type="submit" disabled={addBusy}>{addBusy ? "Storing…" : "Store credential"}</button>
        <button class="add-cancel" type="button" onclick={closeAdd} disabled={addBusy}>Cancel</button>
      </div>
    </form>
  {/if}

  {#if error}
    <div class="error-banner" style="margin-bottom:12px">{error}</div>
  {/if}

  {#if loading}
    <div class="empty">Loading…</div>
  {:else if credentials.length === 0}
    <div class="empty">No stored credentials. Add one above, or let an agent store one it was given — anything here becomes an environment variable in every agent session.</div>
  {:else}
    <table class="cred-table">
      <thead>
        <tr><th>Name</th><th>Purpose</th><th>Stored by</th><th>Goal</th><th>Added</th><th></th></tr>
      </thead>
      <tbody>
        {#each credentials as c (c.name)}
          <tr>
            <td class="mono">{c.name}</td>
            <td>{c.purpose || "—"}</td>
            <td>
              {#if c.created_by_agent}
                <button
                  class="by-agent"
                  disabled={!c.created_by_session}
                  title={c.created_by_session ? "Open the conversation this credential was stored in" : "Stored by an agent"}
                  onclick={() => c.created_by_session && onOpenChat({ sessionId: c.created_by_session })}
                >{c.created_by_agent}</button>
              {:else}
                <span class="by-you">You</span>
              {/if}
            </td>
            <td class="mono">{c.goal_id || "—"}</td>
            <td>{fmtDate(c.created_at)}</td>
            <td class="actions"><button class="delete-btn" type="button" onclick={() => (pendingDelete = c)}>Delete</button></td>
          </tr>
        {/each}
      </tbody>
    </table>
    <div class="hint">To rotate a value, add the same name again and confirm the replacement. Deleting is the only way to take one away — agents can add and replace, never remove.</div>
  {/if}
</section>

{#if pendingReplace}
  <ConfirmModal
    title="Replace this credential?"
    message={`${pendingReplace.name} already exists. Replacing it overwrites the stored value everywhere it is used; agents pick the new one up on their next run. The old value cannot be recovered.`}
    confirmLabel="Replace value"
    busy={replaceBusy}
    onConfirm={confirmReplace}
    onCancel={() => (pendingReplace = null)} />
{/if}

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

  .card-icon.teal {
    background: #eaf2ee;
    color: #2f6e60;
  }

  .card-title {
    font: 800 17px "Hanken Grotesk";
    letter-spacing: -0.01em;
  }

  .card-sub {
    font: 400 12.5px "Hanken Grotesk";
    color: var(--muted-2);
  }

  .add-btn {
    margin-left: auto;
    flex: none;
    align-self: flex-start;
    border: 1px solid var(--line-2);
    background: var(--surface-3, #fbfaf8);
    border-radius: 11px;
    padding: 9px 15px;
    font: 600 13px "Hanken Grotesk";
    color: var(--ink);
  }

  .add-form {
    margin-top: 18px;
    padding: 18px;
    border: 1px solid var(--line-2);
    border-radius: 16px;
    background: var(--surface-3, #fbfaf8);
  }

  .add-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 14px;
  }

  .add-field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .add-field.wide {
    grid-column: 1 / -1;
  }

  .add-note {
    margin-top: 12px;
    font: 400 12.5px "Hanken Grotesk";
    color: var(--muted-2);
  }

  .add-note code {
    font: 600 12px "JetBrains Mono", monospace;
  }

  .add-actions {
    display: flex;
    align-items: center;
    gap: 9px;
    margin-top: 16px;
  }

  .add-save {
    padding: 11px 20px;
  }

  .add-cancel {
    border: 1px solid var(--line-2);
    background: transparent;
    border-radius: 11px;
    padding: 10px 16px;
    font: 600 13px "Hanken Grotesk";
    color: var(--muted-2);
  }

  .cred-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13.5px;
    margin-top: 14px;
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
  .delete-btn {
    border: 0;
    border-radius: 9px;
    padding: 7px 12px;
    background: var(--orange);
    color: #fff;
    font: 700 12px "Hanken Grotesk";
  }
  .delete-btn:hover {
    background: var(--orange-ink);
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

  @media (max-width: 640px) {
    .add-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
