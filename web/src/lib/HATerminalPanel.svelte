<script lang="ts">
  import ProviderLogo from "./ProviderLogo.svelte";
  import { terminalUrl, validTerminalProfile, type TerminalFlow } from "./terminal";

  interface Props {
    initialFlow?: TerminalFlow;
  }

  let { initialFlow = "claude" }: Props = $props();

  let flow = $state<TerminalFlow>("claude");
  let profile = $state("");
  let frameKey = $state(0);

  $effect(() => {
    if (frameKey === 0) flow = initialFlow;
  });

  const profileOK = $derived(validTerminalProfile(profile));
  const src = $derived(terminalUrl(flow, profileOK ? profile : ""));
  const showProfile = $derived(flow === "claude" || flow === "codex");

  function open(flowName: TerminalFlow) {
    flow = flowName;
    frameKey += 1;
  }

  function openTab() {
    window.open(src, "_blank", "noopener,noreferrer");
  }
</script>

<section class="terminal-panel">
  <div class="terminal-toolbar">
    <div class="flow-buttons" role="group" aria-label="Terminal flow">
      <button class:active={flow === "claude"} onclick={() => open("claude")}>
        <ProviderLogo provider="claude" />Claude
      </button>
      <button class:active={flow === "codex"} onclick={() => open("codex")}>
        <ProviderLogo provider="codex" />Codex
      </button>
      <button class:active={flow === "onboard"} onclick={() => open("onboard")}>
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14" /><path d="M13 6l6 6-6 6" /></svg>
        Onboard
      </button>
      <button class:active={flow === "shell"} onclick={() => open("shell")}>
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m4 17 6-6-6-6" /><path d="M12 19h8" /></svg>
        Shell
      </button>
    </div>
    {#if showProfile}
      <label class="profile-field" class:invalid={!profileOK}>
        <span>profile</span>
        <input bind:value={profile} placeholder="default" spellcheck="false" />
      </label>
    {/if}
    <button class="tab-button" onclick={openTab} title="Open terminal in a new tab" aria-label="Open terminal in a new tab">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M15 3h6v6" /><path d="M10 14 21 3" /><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /></svg>
    </button>
  </div>

  {#if !profileOK}
    <div class="profile-error">Use letters, numbers, dots, underscores, or dashes.</div>
  {/if}

  {#key frameKey}
    <iframe
      class="terminal-frame"
      title="Podiom terminal"
      src={src}
      allow="clipboard-read; clipboard-write"></iframe>
  {/key}
</section>

<style>
  .terminal-panel {
    min-height: 0;
    height: 100%;
    display: flex;
    flex-direction: column;
    background: #151713;
    color: #f5efe6;
  }

  .terminal-toolbar {
    min-height: 60px;
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 14px;
    border-bottom: 1px solid rgba(245, 239, 230, 0.14);
    background: #20231d;
  }

  .flow-buttons {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }

  button {
    height: 38px;
    border: 1px solid rgba(245, 239, 230, 0.18);
    background: rgba(245, 239, 230, 0.06);
    color: #f5efe6;
    border-radius: 8px;
    padding: 0 12px;
    font: 700 13px "Hanken Grotesk";
    display: inline-flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
  }

  button.active {
    border-color: #94d0bf;
    background: #2f6e60;
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

  .profile-field {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 8px;
    color: #cfc7bb;
    font: 700 11px "JetBrains Mono", monospace;
    text-transform: uppercase;
  }

  .profile-field input {
    width: 148px;
    height: 38px;
    border-radius: 8px;
    border: 1px solid rgba(245, 239, 230, 0.2);
    background: #11130f;
    color: #f5efe6;
    padding: 0 10px;
    font: 600 13px "JetBrains Mono", monospace;
  }

  .profile-field.invalid input {
    border-color: #d47c6a;
  }

  .tab-button {
    width: 38px;
    padding: 0;
    justify-content: center;
  }

  .profile-error {
    padding: 8px 14px;
    background: #3a211d;
    color: #ffd9cf;
    font: 600 12px "Hanken Grotesk";
  }

  .terminal-frame {
    width: 100%;
    flex: 1;
    min-height: 420px;
    border: 0;
    background: #0d0f0b;
  }

  @media (max-width: 760px) {
    .terminal-toolbar {
      align-items: stretch;
      flex-direction: column;
    }

    .profile-field {
      margin-left: 0;
      justify-content: space-between;
    }

    .profile-field input {
      flex: 1;
      width: auto;
      min-width: 0;
    }

    .tab-button {
      position: absolute;
      top: 10px;
      right: 14px;
    }
  }
</style>
