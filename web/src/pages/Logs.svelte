<script lang="ts">
  import { onMount, tick } from "svelte";
  import { followLogs, getLogs } from "../lib/api";
  import type { LogStreamEvent } from "../lib/types";

  // When embedded (e.g. inside the Settings page) the page chrome is dropped and
  // the viewport is height-constrained so it sits inside a card.
  let { embedded = false }: { embedded?: boolean } = $props();

  let path = $state("");
  let lines = $state<string[]>([]);
  let error = $state<string | null>(null);
  let following = $state(true);
  let loading = $state(true);
  let stream: AbortController | null = null;
  let viewport: HTMLDivElement;

  onMount(() => {
    void refresh(true);
    return () => stopFollow();
  });

  async function refresh(start = following) {
    loading = true;
    error = null;
    try {
      const snap = await getLogs(200);
      path = snap.path;
      lines = snap.lines;
      await scrollBottom();
      if (start) startFollow();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  function startFollow() {
    stopFollow();
    following = true;
    const controller = new AbortController();
    stream = controller;
    void followLogs(0, controller.signal, handleEvent).catch((e) => {
      if (controller.signal.aborted) return;
      error = e instanceof Error ? e.message : String(e);
      following = false;
    });
  }

  function stopFollow() {
    if (stream) {
      stream.abort();
      stream = null;
    }
  }

  function pause() {
    following = false;
    stopFollow();
  }

  function resume() {
    if (!following) startFollow();
  }

  function handleEvent(event: LogStreamEvent) {
    if (event.type === "reopen") {
      pushLine("[podiom logs reopened]");
      return;
    }
    if (event.line !== undefined) pushLine(event.line);
  }

  function pushLine(line: string) {
    lines = [...lines, line].slice(-5000);
    void scrollBottom();
  }

  async function scrollBottom() {
    await tick();
    if (viewport) viewport.scrollTop = viewport.scrollHeight;
  }

  function clear() {
    lines = [];
  }
</script>

<div class="logs-page" class:embedded>
  <div class="logs-head">
    <div class="logs-head-text">
      {#if !embedded}
        <div class="logs-title">Logs</div>
      {/if}
      <div class="logs-path mono">{path || "$PODIOM_HOME/logs/podiomd.log"}</div>
    </div>
    <div class="logs-actions">
      <button class="log-btn" onclick={clear} disabled={lines.length === 0}>Clear</button>
      <button class="log-btn" onclick={() => void refresh(following)} disabled={loading}>Refresh</button>
      {#if following}
        <button class="log-btn primary" onclick={pause}>Pause</button>
      {:else}
        <button class="log-btn primary" onclick={resume}>Follow</button>
      {/if}
    </div>
  </div>

  {#if error}
    <div class="log-error">{error}</div>
  {/if}

  <div class="log-shell">
    <div class="log-toolbar">
      <span class="status-dot" class:live={following}></span>
      <span class="mono">{following ? "following" : "paused"}</span>
      <span class="sep"></span>
      <span class="mono">{lines.length} lines</span>
    </div>
    <div class="log-view" bind:this={viewport}>
      {#if loading && lines.length === 0}
        <div class="log-empty">Loading logs…</div>
      {:else if lines.length === 0}
        <div class="log-empty">No log lines yet.</div>
      {:else}
        {#each lines as line, i (i)}
          <div class="log-line"><span class="line-no">{i + 1}</span><span class="line-text">{line}</span></div>
        {/each}
      {/if}
    </div>
  </div>
</div>

<style>
  .logs-page {
    height: 100%;
    padding: 24px;
    display: flex;
    flex-direction: column;
    gap: 16px;
    min-height: 0;
  }

  /* Embedded inside the Settings card: shed the full-height page chrome. */
  .logs-page.embedded {
    height: auto;
    padding: 0;
    gap: 12px;
  }

  .logs-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    flex-wrap: wrap;
  }

  .logs-head-text {
    min-width: 0;
  }

  .logs-title {
    font: 800 24px "Hanken Grotesk";
    letter-spacing: 0;
  }

  .logs-path {
    margin-top: 5px;
    color: var(--muted-2);
    font-size: 12px;
    overflow-wrap: anywhere;
  }

  .logs-actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .log-btn {
    border: 1px solid var(--line);
    background: var(--surface);
    color: var(--muted);
    border-radius: 8px;
    padding: 9px 13px;
    font: 700 12.5px "Hanken Grotesk";
  }

  .log-btn.primary {
    border-color: var(--teal);
    background: var(--teal);
    color: #fff;
  }

  .log-error {
    border: 1px solid #e7c3b5;
    background: #fff4ef;
    color: #9f3f23;
    border-radius: 8px;
    padding: 10px 12px;
    font: 500 13px/1.45 "Hanken Grotesk";
  }

  .log-shell {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    border: 1px solid var(--line-2);
    background: var(--surface);
    border-radius: 8px;
    overflow: hidden;
    box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04);
  }

  /* Constrain to a scrolling panel rather than filling the viewport. */
  .embedded .log-shell {
    flex: none;
    height: 440px;
    border-radius: 14px;
  }

  .log-toolbar {
    flex: none;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 13px;
    border-bottom: 1px solid var(--line-3);
    background: var(--surface-3);
    color: var(--muted);
    font-size: 11px;
  }

  .status-dot {
    width: 8px;
    height: 8px;
    border-radius: 99px;
    background: #c0492a;
    box-shadow: 0 0 0 3px rgba(192, 73, 42, 0.16);
  }

  .status-dot.live {
    background: #4f9e78;
    box-shadow: 0 0 0 3px rgba(79, 158, 120, 0.16);
  }

  .sep {
    width: 1px;
    height: 14px;
    background: var(--line);
  }

  .log-view {
    flex: 1;
    min-height: 0;
    overflow: auto;
    background: #211d19;
    color: #f7eee2;
    padding: 12px 0;
  }

  .log-line {
    display: grid;
    grid-template-columns: 64px minmax(0, 1fr);
    gap: 12px;
    padding: 2px 16px 2px 0;
    font: 500 12px/1.55 "JetBrains Mono", monospace;
  }

  .log-line:hover {
    background: rgba(255, 255, 255, 0.05);
  }

  .line-no {
    color: #8f8579;
    text-align: right;
    user-select: none;
  }

  .line-text {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .log-empty {
    color: #b7aa9b;
    font: 500 13px "Hanken Grotesk";
    padding: 18px;
  }

  .mono {
    font-family: "JetBrains Mono", monospace;
  }
</style>
