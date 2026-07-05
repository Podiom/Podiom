<script lang="ts">
  import { terminalUrl, type TerminalFlow } from "./terminal";

  interface Props {
    flow?: TerminalFlow;
    showToolbar?: boolean;
  }

  let { flow = "onboard", showToolbar = false }: Props = $props();

  let frameKey = $state(0);

  const src = $derived(terminalUrl(flow));

  function reload() {
    frameKey += 1;
  }

  function openTab() {
    window.open(src, "_blank", "noopener,noreferrer");
  }
</script>

<section class="terminal-panel">
  {#if showToolbar}
    <div class="terminal-toolbar">
      <button onclick={reload} title="Reload terminal" aria-label="Reload terminal">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 12a9 9 0 1 1-2.64-6.36" /><path d="M21 3v6h-6" /></svg>
      </button>
      <button onclick={openTab} title="Open terminal in a new tab" aria-label="Open terminal in a new tab">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M15 3h6v6" /><path d="M10 14 21 3" /><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /></svg>
      </button>
    </div>
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
    min-height: 48px;
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: 8px;
    padding: 8px 10px;
    border-bottom: 1px solid rgba(245, 239, 230, 0.14);
    background: #20231d;
  }

  button {
    width: 34px;
    height: 34px;
    border: 1px solid rgba(245, 239, 230, 0.18);
    background: rgba(245, 239, 230, 0.06);
    color: #f5efe6;
    border-radius: 8px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
  }

  button:hover {
    border-color: rgba(148, 208, 191, 0.7);
    background: rgba(95, 179, 160, 0.16);
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

  .terminal-frame {
    width: 100%;
    flex: 1;
    min-height: 400px;
    border: 0;
    background: #0d0f0b;
  }
</style>
