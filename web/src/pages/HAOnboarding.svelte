<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { completeOnboarding, getOnboardingState, getOnboardingToken } from "../lib/api";
  import { auth } from "../lib/auth.svelte";
  import HATerminalPanel from "../lib/HATerminalPanel.svelte";
  import type { OnboardingState } from "../lib/types";

  interface Props {
    onUnlocked: () => void;
  }

  let { onUnlocked }: Props = $props();

  let onboarding = $state<OnboardingState | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let tokenCopied = $state(false);
  let finishing = $state(false);
  let poll: number | undefined;

  onMount(() => {
    void refresh();
    poll = window.setInterval(() => void refresh(true), 3_000);
  });

  onDestroy(() => {
    if (poll) window.clearInterval(poll);
  });

  async function refresh(silent = false) {
    if (!silent) {
      loading = true;
      error = null;
    }
    try {
      onboarding = await getOnboardingState();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function copyGatewayToken(): Promise<boolean> {
    error = null;
    try {
      const result = await getOnboardingToken();
      try {
        await navigator.clipboard.writeText(result.token);
        tokenCopied = true;
      } catch {
        tokenCopied = false;
      }
      auth.setToken(result.token);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async function finish() {
    finishing = true;
    error = null;
    try {
      if (!onboarding?.completed) {
        onboarding = await completeOnboarding();
      }
      if (!auth.token && !(await copyGatewayToken())) return;
      onUnlocked();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      finishing = false;
    }
  }
</script>

<main class="ha-onboarding">
  <section class="copy">
    <div>
      <div class="eyebrow">Home Assistant setup</div>
      <h1>Podiom</h1>
    </div>
    {#if onboarding?.completed}
      <div class="status complete">Onboarding complete</div>
    {:else}
      <div class="status">Waiting for wizard</div>
    {/if}
  </section>

  {#if error}
    <div class="error">{error}</div>
  {/if}

  {#if loading && !onboarding}
    <div class="loading">Loading setup state...</div>
  {:else if onboarding?.completed}
    <section class="finish-pane">
      <div>
        <h2>Gateway token</h2>
        <p>The dashboard uses the gateway token for API and WebSocket calls. Copying it here also stores it in this browser.</p>
      </div>
      <div class="finish-actions">
        <button class="secondary" onclick={copyGatewayToken}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></svg>
          {auth.token ? (tokenCopied ? "Copied" : "Stored") : "Copy token"}
        </button>
        <button class="primary" disabled={finishing || !auth.token} onclick={finish}>
          {finishing ? "Opening..." : "Finished"}
        </button>
      </div>
    </section>
  {:else}
    <section class="intro">
      <h2>Authenticate providers, then run the onboarding wizard.</h2>
      <p>Use Claude or Codex for default logins, enter a profile name for profile-specific logins, then run Onboard to create your first agent.</p>
    </section>
    <div class="terminal-wrap">
      <HATerminalPanel initialFlow="claude" />
    </div>
  {/if}
</main>

<style>
  .ha-onboarding {
    min-height: 100vh;
    display: grid;
    grid-template-rows: auto auto auto 1fr;
    background: #f7f4ee;
    color: #261f19;
  }

  .copy {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 18px;
    padding: 28px clamp(18px, 4vw, 46px) 16px;
  }

  .eyebrow {
    color: #7f7165;
    font: 700 12px "JetBrains Mono", monospace;
    text-transform: uppercase;
  }

  h1 {
    margin: 2px 0 0;
    font: 800 34px "Hanken Grotesk";
  }

  h2 {
    margin: 0 0 6px;
    font: 800 21px "Hanken Grotesk";
  }

  p {
    margin: 0;
    max-width: 720px;
    color: #65594e;
    font: 500 15px/1.45 "Hanken Grotesk";
  }

  .status {
    border: 1px solid #e2d7ca;
    background: #fff;
    color: #67594d;
    border-radius: 8px;
    padding: 9px 12px;
    font: 700 12px "JetBrains Mono", monospace;
    text-transform: uppercase;
  }

  .status.complete {
    border-color: #bfe0d6;
    background: #e3f1ec;
    color: #2f6e60;
  }

  .error,
  .loading,
  .intro,
  .finish-pane {
    margin: 0 clamp(18px, 4vw, 46px) 16px;
  }

  .error {
    border: 1px solid #efc7bd;
    background: #fff3ef;
    color: #7c382b;
    border-radius: 8px;
    padding: 11px 12px;
    font: 650 14px "Hanken Grotesk";
  }

  .loading {
    color: #7f7165;
    font: 700 13px "JetBrains Mono", monospace;
  }

  .intro,
  .finish-pane {
    border-top: 1px solid #e6ddd2;
    padding-top: 16px;
  }

  .finish-pane {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
  }

  .finish-actions {
    display: flex;
    gap: 10px;
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  button {
    height: 40px;
    border-radius: 8px;
    padding: 0 14px;
    border: 1px solid #d9cfc2;
    background: #fff;
    color: #2d261f;
    cursor: pointer;
    font: 800 13px "Hanken Grotesk";
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }

  button.primary {
    border-color: #2f6e60;
    background: #2f6e60;
    color: #fff;
  }

  button:disabled {
    cursor: not-allowed;
    opacity: 0.55;
  }

  svg {
    width: 16px;
    height: 16px;
    fill: none;
    stroke: currentColor;
    stroke-width: 2;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .terminal-wrap {
    min-height: 520px;
    margin: 0 clamp(18px, 4vw, 46px) 32px;
    border-radius: 8px;
    overflow: hidden;
    border: 1px solid #262820;
  }

  @media (max-width: 760px) {
    .copy,
    .finish-pane {
      align-items: flex-start;
      flex-direction: column;
    }

    .finish-actions {
      justify-content: flex-start;
    }

    .terminal-wrap {
      margin-inline: 0;
      border-left: 0;
      border-right: 0;
      border-radius: 0;
    }
  }
</style>
