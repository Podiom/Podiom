<script lang="ts">
  import { clearMemory, updateMemory } from "./api";
  import { parseMemory, relativeTime, shortDate } from "./memory";
  import type { Dream, MemoryInfo } from "./types";

  let {
    agentName,
    memory,
    dreams = [],
    onChanged = () => {},
    onDreamNow = () => {},
  }: {
    agentName: string;
    memory: MemoryInfo;
    dreams?: Dream[];
    onChanged?: () => void;
    onDreamNow?: () => void;
  } = $props();

  let editing = $state(false);
  let editText = $state("");
  let saving = $state(false);
  let clearArmed = $state(false);
  let clearing = $state(false);
  let error = $state<string | null>(null);

  const parsed = $derived(parseMemory(memory.memory, dreams));
  const isEmpty = $derived(parsed.sections.length === 0);
  const hasPending = $derived(memory.pending_sessions > 0);
  const budgetPct = $derived(
    Math.min(100, Math.round((memory.lines / Math.max(1, memory.budget_lines)) * 100)),
  );

  function startEdit() {
    editText = memory.memory;
    error = null;
    editing = true;
  }

  async function save() {
    saving = true;
    error = null;
    try {
      await updateMemory(agentName, editText);
      editing = false;
      onChanged();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  async function confirmClear() {
    clearing = true;
    error = null;
    try {
      await clearMemory(agentName);
      clearArmed = false;
      onChanged();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      clearing = false;
    }
  }
</script>

<div class="mem">
  <div class="mem-head">
    <div class="mem-headtext">
      <div class="mem-title-row">
        <span class="mem-moon" aria-hidden="true">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="#574AB0"
            ><path d="M20 14.5A8.5 8.5 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z" /></svg
          >
        </span>
        <span class="mem-title">Memory</span>
        <span class="mem-badge">dreamed nightly</span>
      </div>
      <div class="mem-sub">
        Not a profile of you — a record of us. What we're making, what you think, what we've
        settled between us, and the small things worth keeping. It grows a little each night.
      </div>
    </div>
    <button
      class="mem-dream-btn"
      class:enabled={hasPending}
      disabled={!hasPending}
      onclick={onDreamNow}
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"
        ><path d="M20 14.5A8.5 8.5 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z" /></svg
      >
      {hasPending ? "Dream now" : "Caught up"}
    </button>
  </div>

  <div class="mem-status">
    <div class="mem-lastdream">
      <span class="mem-dot"></span>
      <span>Last dreamed <b>{relativeTime(memory.last_dream?.RanAt)}</b></span>
    </div>
    <span class="mem-pending" class:on={hasPending}>
      {hasPending
        ? `${memory.pending_sessions} conversation${memory.pending_sessions === 1 ? "" : "s"} from today, not yet slept on`
        : "the day is dreamed · nothing left waiting"}
    </span>
    <span class="mem-spacer"></span>
    <div class="mem-budget">
      <div class="mem-budget-label">
        {memory.lines} / {memory.budget_lines} lines carried into every conversation
      </div>
      <div class="mem-budget-track"><div class="mem-budget-fill" style="width:{budgetPct}%"></div></div>
    </div>
  </div>

  {#if error}
    <div class="mem-error">{error}</div>
  {/if}

  {#if editing}
    <div class="mem-edit">
      <textarea class="mem-textarea" rows="15" bind:value={editText}></textarea>
      <div class="mem-hint">
        This memory is yours to shape. Anything you take out, the dreams won't put back.
      </div>
      <div class="mem-actions">
        <button class="mem-save" onclick={save} disabled={saving}>
          {saving ? "Saving…" : "Save memory"}
        </button>
        <button class="mem-cancel" onclick={() => (editing = false)} disabled={saving}>Cancel</button>
      </div>
    </div>
  {:else if isEmpty}
    <div class="mem-empty">
      <div class="mem-empty-title">Nothing kept yet</div>
      <div class="mem-empty-sub">
        {agentName} hasn't slept on a day with you yet. Spend some time together, and the nights
        will start to remember.
      </div>
    </div>
    {@render footer()}
  {:else}
    <div class="mem-sections">
      {#each parsed.sections as section}
        <div class="mem-section">
          <div class="mem-section-title">{section.title}</div>
          <div class="mem-items">
            {#each section.items as item}
              <div class="mem-item">
                <span class="mem-item-dot"></span>
                <div class="mem-item-body">
                  <span class="mem-item-text">{item.text}</span>
                  {#if item.since}<span class="mem-item-since">{shortDate(item.since)}</span>{/if}
                  {#if item.isNew}<span class="mem-item-new">NEW</span>{/if}
                </div>
              </div>
            {/each}
          </div>
        </div>
      {/each}
    </div>
    {@render footer()}
  {/if}
</div>

{#snippet footer()}
  <div class="mem-footer">
    <button class="mem-edit-btn" onclick={startEdit}>
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
        ><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" /></svg
      >
      Edit memory
    </button>
    <span class="mem-spacer"></span>
    {#if clearArmed}
      <span class="mem-clear-q">Clear all memory?</span>
      <button class="mem-clear-yes" onclick={confirmClear} disabled={clearing}>
        {clearing ? "Clearing…" : "Yes, clear"}
      </button>
      <button class="mem-cancel-sm" onclick={() => (clearArmed = false)}>Cancel</button>
    {:else}
      <button class="mem-clear" onclick={() => (clearArmed = true)}>Clear</button>
    {/if}
  </div>
{/snippet}

<style>
  .mem {
    position: relative;
    overflow: hidden;
    background: linear-gradient(180deg, #fcfaff, #fffdfb 130px);
    border: 1px solid #e6def6;
    border-radius: 20px;
    padding: 22px 22px 16px;
    box-shadow:
      0 1px 2px rgba(43, 37, 32, 0.04),
      0 22px 50px -34px rgba(75, 60, 150, 0.4);
  }
  .mem-head {
    display: flex;
    align-items: flex-start;
    gap: 14px;
  }
  .mem-headtext {
    flex: 1;
    min-width: 0;
  }
  .mem-title-row {
    display: flex;
    align-items: center;
    gap: 9px;
    flex-wrap: wrap;
  }
  .mem-moon {
    width: 26px;
    height: 26px;
    border-radius: 9px;
    background: #eeeafb;
    display: flex;
    align-items: center;
    justify-content: center;
    flex: none;
  }
  .mem-title {
    font: 800 19px "Hanken Grotesk";
    letter-spacing: -0.01em;
  }
  .mem-badge {
    padding: 3px 9px;
    border-radius: 999px;
    background: #eeeafb;
    border: 1px solid #ded6f5;
    font: 600 10px "JetBrains Mono", monospace;
    color: #574ab0;
  }
  .mem-sub {
    font: 400 13px/1.5 "Hanken Grotesk";
    color: #8a7f73;
    margin-top: 6px;
    max-width: 470px;
  }
  .mem-dream-btn {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    padding: 9px 15px;
    border: 1px solid #e4dcf3;
    border-radius: 11px;
    font: 600 13px "Hanken Grotesk";
    cursor: default;
    color: #a99fc8;
    background: #f4f0fc;
    flex: none;
  }
  .mem-dream-btn.enabled {
    border: none;
    cursor: pointer;
    color: #fff;
    background: linear-gradient(150deg, #6b5bd6, #4b3f9e);
    box-shadow: 0 8px 18px -8px rgba(75, 60, 158, 0.8);
  }
  .mem-status {
    display: flex;
    align-items: center;
    gap: 14px;
    flex-wrap: wrap;
    margin-top: 16px;
    padding: 12px 14px;
    border-radius: 13px;
    background: #f7f4fe;
    border: 1px solid #ebe5fa;
  }
  .mem-lastdream {
    display: flex;
    align-items: center;
    gap: 8px;
    font: 600 12.5px "Hanken Grotesk";
    color: #4a4138;
  }
  .mem-lastdream b {
    color: #574ab0;
  }
  .mem-dot {
    width: 7px;
    height: 7px;
    border-radius: 99px;
    background: #6b5bd6;
    flex: none;
  }
  .mem-pending {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 11px;
    border-radius: 999px;
    background: #e5f1ec;
    border: 1px solid #c7e2da;
    font: 600 11.5px "Hanken Grotesk";
    color: #2f6e60;
  }
  .mem-pending.on {
    background: #fbeee6;
    border: 1px solid #f1d9c9;
    color: #b5713a;
  }
  .mem-spacer {
    flex: 1;
    min-width: 20px;
  }
  .mem-budget {
    min-width: 158px;
  }
  .mem-budget-label {
    font: 500 10.5px "JetBrains Mono", monospace;
    color: #9a8e80;
    margin-bottom: 4px;
  }
  .mem-budget-track {
    height: 5px;
    border-radius: 99px;
    background: #e9e2f6;
    overflow: hidden;
  }
  .mem-budget-fill {
    height: 100%;
    border-radius: 99px;
    background: linear-gradient(90deg, #6b5bd6, #8b7be8);
  }
  .mem-error {
    margin-top: 12px;
    padding: 8px 12px;
    border-radius: 9px;
    background: #f7e7e2;
    border: 1px solid #e9cbc0;
    color: #b0492a;
    font: 500 12.5px "Hanken Grotesk";
  }
  .mem-edit {
    margin-top: 14px;
  }
  .mem-textarea {
    width: 100%;
    border: 1px solid #ded6f5;
    border-radius: 13px;
    padding: 14px 16px;
    font: 400 13px/1.7 "JetBrains Mono", monospace;
    color: #3a332c;
    outline: none;
    background: #fbfaff;
    resize: vertical;
    box-sizing: border-box;
  }
  .mem-hint {
    font: 400 11.5px "Hanken Grotesk";
    color: #9a8e80;
    margin-top: 8px;
  }
  .mem-actions {
    display: flex;
    gap: 9px;
    margin-top: 12px;
  }
  .mem-save {
    padding: 9px 18px;
    border: none;
    border-radius: 11px;
    background: #3f8f7e;
    color: #fff;
    font: 600 13px "Hanken Grotesk";
    cursor: pointer;
  }
  .mem-cancel {
    padding: 9px 16px;
    border: 1px solid #eae0d4;
    border-radius: 11px;
    background: #fff;
    color: #6f6459;
    font: 600 13px "Hanken Grotesk";
    cursor: pointer;
  }
  .mem-empty {
    margin: 20px 0 4px;
    text-align: center;
    padding: 26px;
    border: 1px dashed #ded6f5;
    border-radius: 14px;
    background: #fbfaff;
  }
  .mem-empty-title {
    font: 700 15px "Hanken Grotesk";
    color: #4a4138;
  }
  .mem-empty-sub {
    font: 400 13px/1.5 "Hanken Grotesk";
    color: #8a7f73;
    margin: 5px auto 0;
    max-width: 380px;
  }
  .mem-sections {
    margin-top: 16px;
    display: flex;
    flex-direction: column;
    gap: 15px;
  }
  .mem-section-title {
    font: 600 10.5px "JetBrains Mono", monospace;
    letter-spacing: 0.12em;
    color: #a89c8e;
    text-transform: uppercase;
    margin-bottom: 4px;
    padding-left: 12px;
  }
  .mem-items {
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .mem-item {
    display: flex;
    gap: 9px;
    align-items: baseline;
    padding: 4px 12px;
    border-radius: 8px;
  }
  .mem-item-dot {
    width: 5px;
    height: 5px;
    border-radius: 99px;
    background: #cdbff2;
    flex: none;
    transform: translateY(-2px);
  }
  .mem-item-body {
    flex: 1;
    min-width: 0;
  }
  .mem-item-text {
    font: 400 14px/1.55 "Hanken Grotesk";
    color: #3a332c;
  }
  .mem-item-since {
    font: 500 11px "JetBrains Mono", monospace;
    color: #b3a794;
    margin-left: 8px;
    white-space: nowrap;
  }
  .mem-item-new {
    margin-left: 8px;
    padding: 1px 7px;
    border-radius: 6px;
    background: #6b5bd6;
    color: #fff;
    font: 700 9px "JetBrains Mono", monospace;
    letter-spacing: 0.08em;
    vertical-align: middle;
  }
  .mem-footer {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 16px;
    padding: 13px 2px 4px;
    border-top: 1px solid #f0ecf9;
  }
  .mem-edit-btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 7px 13px;
    border: 1px solid #e4dcf3;
    border-radius: 10px;
    background: #fff;
    color: #574ab0;
    font: 600 12.5px "Hanken Grotesk";
    cursor: pointer;
  }
  .mem-clear {
    padding: 7px 13px;
    border: 1px solid #edd9d3;
    border-radius: 10px;
    background: #fff;
    color: #b5563f;
    font: 600 12.5px "Hanken Grotesk";
    cursor: pointer;
  }
  .mem-clear-q {
    font: 500 12px "Hanken Grotesk";
    color: #b5563f;
  }
  .mem-clear-yes {
    padding: 7px 13px;
    border: none;
    border-radius: 10px;
    background: #c0492a;
    color: #fff;
    font: 600 12.5px "Hanken Grotesk";
    cursor: pointer;
  }
  .mem-cancel-sm {
    padding: 7px 11px;
    border: 1px solid #eae0d4;
    border-radius: 10px;
    background: #fff;
    color: #6f6459;
    font: 600 12.5px "Hanken Grotesk";
    cursor: pointer;
  }
</style>
