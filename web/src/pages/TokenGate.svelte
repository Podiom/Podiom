<script lang="ts">
  // TokenGate — the only view reachable without the gateway token (HA10). It
  // never displays a token, only accepts one; where to *get* the value depends
  // on the deployment: HA's Configuration page vs `podiom token show` (HA8).
  import { onMount } from "svelte";
  import { auth } from "../lib/auth.svelte";
  import { apiUrl, deployment } from "../lib/base";
  import { verifyToken } from "../lib/http";

  const mode = deployment();

  let value = $state("");
  let checking = $state(false);
  let error = $state<string | null>(null);
  let daemonUp = $state<boolean | null>(null);

  onMount(() => {
    void probe();
    const timer = window.setInterval(probe, 10_000);
    return () => window.clearInterval(timer);
  });

  async function probe() {
    try {
      const res = await fetch(apiUrl("healthz"));
      daemonUp = res.ok;
    } catch {
      daemonUp = false;
    }
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    const candidate = value.trim();
    if (!candidate || checking) return;
    checking = true;
    error = null;
    const ok = await verifyToken(candidate);
    checking = false;
    if (!ok) {
      error = daemonUp === false
        ? "Podiom isn't reachable right now — is podiomd running?"
        : "That token wasn't accepted. Copy the current value and try again.";
      return;
    }
    auth.setToken(candidate);
  }
</script>

<div class="gate-root">
  <div class="gate-card">
    <div class="gate-brand">
      <div class="gate-logo">P</div>
      <div>
        <div class="gate-name">Podiom</div>
        <div class="gate-tag mono">conductor</div>
      </div>
    </div>

    <h1>Enter your gateway token</h1>
    <p class="gate-lead">
      Podiom protects its API with a gateway token. Enter it once — this
      browser remembers it.
    </p>

    {#if mode === "ha"}
      <ol class="gate-steps">
        <li>Open the Podiom app's <strong>Configuration</strong> page in Home Assistant (Settings → Add-ons → Podiom → Configuration).</li>
        <li>Copy the <span class="mono">gateway_token</span> value.</li>
        <li>Paste it below.</li>
      </ol>
    {:else}
      <ol class="gate-steps">
        <li>On the machine running Podiom, run <span class="mono">podiom token show</span>.</li>
        <li>Paste the printed value below.</li>
      </ol>
    {/if}

    <form onsubmit={submit}>
      <input
        class="gate-input mono"
        type="password"
        bind:value
        placeholder="gateway token"
        autocomplete="off"
        spellcheck="false"
        aria-label="Gateway token"
      />
      {#if error}
        <div class="gate-error">{error}</div>
      {/if}
      <button class="gate-submit" type="submit" disabled={checking || !value.trim()}>
        {checking ? "Checking…" : "Unlock"}
      </button>
    </form>

    <div class="gate-daemon">
      <span class="gate-dot" class:up={daemonUp === true} class:down={daemonUp === false}></span>
      {daemonUp === null ? "Checking daemon…" : daemonUp ? "podiomd is reachable" : "podiomd is not reachable"}
    </div>
  </div>
</div>

<style>
  .gate-root {
    min-height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
  }
  .gate-card {
    width: 100%;
    max-width: 420px;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: 16px;
    padding: 28px;
    box-shadow: 0 8px 30px rgba(43, 37, 32, 0.06);
  }
  .gate-brand {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 20px;
  }
  .gate-logo {
    width: 36px;
    height: 36px;
    border-radius: 10px;
    background: var(--teal);
    color: #fff;
    display: flex;
    align-items: center;
    justify-content: center;
    font-weight: 800;
  }
  .gate-name {
    font-weight: 700;
  }
  .gate-tag {
    font-size: 11px;
    color: var(--muted-2);
  }
  h1 {
    font-size: 18px;
    margin: 0 0 6px;
  }
  .gate-lead {
    margin: 0 0 14px;
    color: var(--muted);
    font-size: 14px;
  }
  .gate-steps {
    margin: 0 0 16px;
    padding-left: 18px;
    color: var(--ink-soft);
    font-size: 13.5px;
    display: grid;
    gap: 6px;
  }
  .gate-input {
    width: 100%;
    border: 1px solid var(--field-line);
    border-radius: 10px;
    padding: 10px 12px;
    font-size: 13px;
    background: var(--surface-2);
    color: var(--ink);
  }
  .gate-input:focus {
    outline: 2px solid var(--teal);
    outline-offset: 1px;
  }
  .gate-error {
    margin-top: 8px;
    color: var(--orange-ink);
    font-size: 13px;
  }
  .gate-submit {
    margin-top: 12px;
    width: 100%;
    border: 0;
    border-radius: 10px;
    padding: 10px 12px;
    background: var(--teal);
    color: #fff;
    font-weight: 600;
    cursor: pointer;
  }
  .gate-submit:disabled {
    opacity: 0.55;
    cursor: default;
  }
  .gate-daemon {
    margin-top: 16px;
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--muted);
    font-size: 12.5px;
  }
  .gate-dot {
    width: 8px;
    height: 8px;
    border-radius: 99px;
    background: var(--faint);
  }
  .gate-dot.up {
    background: var(--teal);
  }
  .gate-dot.down {
    background: var(--orange);
  }
</style>
