<script lang="ts">
  // Drives one provider sign-in: the daemon runs the provider's own login CLI,
  // hands back an authorization URL, and we open it in a popup. Claude then
  // wants the code from that page pasted back; Codex shows a one-time code and
  // finishes on its own.
  //
  // Shared by Settings (per-profile sign-in) and Chat (the CTA shown when a
  // turn dies signed out) so both behave identically.
  import { onDestroy } from "svelte";
  import { cancelProviderLogin, pollProviderLogin, startProviderLogin, submitProviderLoginCode } from "./api";
  import { closeExternal, openExternal } from "./native";
  import type { Provider, ProviderLoginSession } from "./types";

  let {
    provider,
    profile = "",
    // Label for the start button before a session exists.
    startLabel = "Sign in",
    // Called once the account is authenticated, so the host can refresh state.
    onSignedIn = () => {},
  }: {
    provider: Provider;
    profile?: string;
    startLabel?: string;
    onSignedIn?: () => void;
  } = $props();

  let login = $state<ProviderLoginSession | null>(null);
  let busy = $state(false);
  let code = $state("");
  let error = $state<string | null>(null);
  let authWindow: Window | null = null;
  let pollTimer: number | undefined;

  export function active(): boolean {
    return login !== null;
  }

  function clearPolling() {
    if (pollTimer) window.clearTimeout(pollTimer);
    pollTimer = undefined;
  }

  function schedulePoll(ms = 1500) {
    clearPolling();
    if (!login) return;
    pollTimer = window.setTimeout(() => void poll(), ms);
  }

  function closeAuthWindow() {
    closeExternal();
    try {
      authWindow?.close();
      window.focus();
    } catch {
      // Some browsers ignore cross-tab focus/close requests.
    }
    authWindow = null;
  }

  export async function start() {
    busy = true;
    error = null;
    code = "";
    try {
      login = await startProviderLogin(provider, profile);
      schedulePoll(400); // the URL lands as soon as the CLI prints it
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  async function poll() {
    if (!login) return;
    try {
      const next = await pollProviderLogin(login.id);
      const firstURL = !login.url && !!next.url;
      login = next;
      if (firstURL && next.url) {
        authWindow = openExternal(next.url, "podiom-provider-auth", "popup,width=760,height=860");
      }
      if (next.phase === "succeeded") {
        clearPolling();
        closeAuthWindow();
        login = null;
        code = "";
        onSignedIn();
        return;
      }
      if (next.phase === "failed") {
        clearPolling();
        closeAuthWindow();
        error = next.message || "Sign-in failed.";
        login = null;
        return;
      }
      schedulePoll();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      clearPolling();
      login = null;
    }
  }

  async function submitCode() {
    if (!login || !code.trim()) return;
    busy = true;
    error = null;
    try {
      login = await submitProviderLoginCode(login.id, code.trim());
      code = "";
      schedulePoll(400);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  export async function abort() {
    const id = login?.id;
    clearPolling();
    closeAuthWindow();
    login = null;
    code = "";
    error = null;
    if (id) {
      try {
        await cancelProviderLogin(id);
      } catch {
        // The session may already have expired on its own; nothing to undo.
      }
    }
  }

  // A login left running would keep a provider CLI alive on the daemon.
  onDestroy(() => void abort());
</script>

{#if !login}
  <button class="signin-btn" disabled={busy} onclick={start}>{busy ? "Starting…" : startLabel}</button>
{:else}
  {#if login.url}
    <p class="signin-help">
      Authorize this account in the window Podiom opened. If nothing appeared,
      <a href={login.url} target="_blank" rel="noopener noreferrer">open the sign-in page</a>.
    </p>
  {:else}
    <p class="signin-help">Starting the provider's sign-in…</p>
  {/if}

  {#if login.needs_code}
    <p class="signin-help">Then paste the code that page gives you back here.</p>
    <div class="signin-code-row">
      <input
        class="signin-input"
        placeholder="code#state"
        bind:value={code}
        onkeydown={(e) => { if (e.key === "Enter") { e.preventDefault(); void submitCode(); } }} />
      <button class="signin-btn" disabled={busy || !code.trim()} onclick={submitCode}>
        {busy ? "Sending…" : "Submit code"}
      </button>
    </div>
  {:else if login.user_code}
    <div class="signin-user-code">{login.user_code}</div>
    <p class="signin-help">Enter this one-time code on the sign-in page. Podiom finishes on its own.</p>
  {/if}

  {#if login.message}
    <div class="signin-error">{login.message}</div>
  {/if}
  <button class="signin-cancel" onclick={abort}>Cancel sign-in</button>
{/if}

{#if error}
  <div class="signin-error">{error}</div>
{/if}

<style>
  .signin-btn {
    border: none;
    border-radius: 11px;
    padding: 8px 15px;
    background: var(--teal);
    color: #fff;
    font: 700 12.5px "Hanken Grotesk";
    cursor: pointer;
    flex: none;
  }

  .signin-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .signin-cancel {
    margin-top: 12px;
    border: 1px solid var(--field-line);
    border-radius: 11px;
    padding: 8px 15px;
    background: #fff;
    color: var(--muted-2);
    font: 600 12.5px "Hanken Grotesk";
    cursor: pointer;
  }

  .signin-help {
    margin: 10px 0 0;
    font: 500 12.5px "Hanken Grotesk";
    color: var(--muted-2);
    line-height: 1.5;
  }

  .signin-code-row {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    margin-top: 4px;
  }

  .signin-input {
    flex: 1;
    min-width: 0;
    border: 1px solid var(--field-line);
    border-radius: 11px;
    padding: 10px 13px;
    font: 500 12px "JetBrains Mono", monospace;
    color: var(--ink);
    outline: none;
    background: #fff;
  }

  .signin-user-code {
    margin-top: 10px;
    padding: 10px 13px;
    border: 1px solid var(--field-line);
    border-radius: 11px;
    background: #fff;
    font: 700 17px "JetBrains Mono", monospace;
    letter-spacing: 0.16em;
    text-align: center;
    color: var(--ink);
    user-select: all;
  }

  .signin-error {
    margin-top: 10px;
    padding: 8px 12px;
    border-radius: 10px;
    background: #fdecea;
    border: 1px solid #f3cfc8;
    color: #8f3b2a;
    font: 500 12px "Hanken Grotesk";
  }
</style>
