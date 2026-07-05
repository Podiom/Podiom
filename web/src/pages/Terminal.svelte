<script lang="ts">
  import HATerminalPanel from "../lib/HATerminalPanel.svelte";
</script>

<main class="terminal-page">
  <header>
    <div>
      <div class="eyebrow">Home Assistant</div>
      <h1>Terminal</h1>
    </div>
  </header>
  <div class="terminal-shell">
    <HATerminalPanel flow="shell" showToolbar={true} />
  </div>
</main>

<style>
  .terminal-page {
    height: 100%;
    min-height: 0;
    display: grid;
    grid-template-rows: auto 1fr;
    background: #f7f4ee;
  }

  header {
    padding: 24px 28px 16px;
    border-bottom: 1px solid #e4dacf;
  }

  .eyebrow {
    color: #817266;
    font: 700 12px "JetBrains Mono", monospace;
    text-transform: uppercase;
  }

  h1 {
    margin: 3px 0 0;
    font: 800 30px "Hanken Grotesk";
    color: #251f19;
  }

  .terminal-shell {
    min-height: 0;
    padding: 18px;
  }

  .terminal-shell :global(.terminal-panel) {
    border: 1px solid #262820;
    border-radius: 8px;
    overflow: hidden;
  }

  @media (max-width: 760px) {
    header {
      padding: 18px;
    }

    .terminal-shell {
      padding: 0;
    }

    .terminal-shell :global(.terminal-panel) {
      border-left: 0;
      border-right: 0;
      border-radius: 0;
    }
  }
</style>
