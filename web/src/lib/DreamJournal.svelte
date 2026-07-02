<script lang="ts">
  import type { Dream } from "./types";

  let { agentName, dreams = [] }: { agentName: string; dreams?: Dream[] } = $props();

  // Only successful dreams belong in the journal; a failed dream changed nothing.
  const entries = $derived(dreams.filter((d) => d.Status === "success"));

  function dateLabel(iso: string): string {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  }
  function timeLabel(iso: string): string {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return "";
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  }
</script>

<div class="dj">
  <div class="dj-head">
    <span class="dj-title">Dream journal</span>
    <span class="dj-sub">
      Each night, while you sleep, {agentName} goes back over the hours you spent together and keeps
      only what deserves to stay. A day you don't speak leaves nothing to dream on.
    </span>
  </div>

  {#if entries.length === 0}
    <div class="dj-empty">No dreams yet — the nights are still quiet.</div>
  {:else}
    <div class="dj-timeline">
      {#each entries as j}
        <div class="dj-row">
          <div class="dj-rail">
            <span class="dj-dot"></span>
            <span class="dj-line"></span>
          </div>
          <div class="dj-body">
            <div class="dj-meta">
              <span class="dj-date">{dateLabel(j.RanAt)}</span>
              <span class="dj-time">{timeLabel(j.RanAt)}</span>
              <span class="dj-from">· from {j.SessionCount} session{j.SessionCount === 1 ? "" : "s"}</span>
            </div>
            <div class="dj-chips">
              <span class="dj-chip kept">kept {j.Kept}</span>
              <span class="dj-chip merged">merged {j.Merged}</span>
              <span class="dj-chip pruned">pruned {j.Pruned}</span>
            </div>
            {#if j.Note}<div class="dj-note">{j.Note}</div>{/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .dj {
    margin-top: 28px;
  }
  .dj-head {
    display: flex;
    align-items: baseline;
    gap: 10px;
    flex-wrap: wrap;
  }
  .dj-title {
    font: 800 18px "Hanken Grotesk";
    letter-spacing: -0.01em;
  }
  .dj-sub {
    font: 400 13px "Hanken Grotesk";
    color: #8a7f73;
    max-width: 520px;
  }
  .dj-empty {
    margin-top: 14px;
    font: 400 13px "Hanken Grotesk";
    color: #a89c8e;
  }
  .dj-timeline {
    margin-top: 16px;
    padding-left: 4px;
  }
  .dj-row {
    display: flex;
    gap: 16px;
  }
  .dj-rail {
    flex: none;
    display: flex;
    flex-direction: column;
    align-items: center;
  }
  .dj-dot {
    width: 11px;
    height: 11px;
    border-radius: 99px;
    background: #6b5bd6;
    border: 2px solid #eeeafb;
    flex: none;
  }
  .dj-line {
    flex: 1;
    width: 2px;
    background: #ede4d9;
    margin-top: 3px;
    margin-bottom: -1px;
  }
  .dj-body {
    flex: 1;
    min-width: 0;
    padding-bottom: 20px;
  }
  .dj-meta {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .dj-date {
    font: 700 14px "Hanken Grotesk";
    color: #2b2520;
  }
  .dj-time {
    font: 500 11.5px "JetBrains Mono", monospace;
    color: #a89c8e;
  }
  .dj-from {
    font: 500 11.5px "Hanken Grotesk";
    color: #8a7f73;
  }
  .dj-chips {
    display: flex;
    gap: 7px;
    flex-wrap: wrap;
    margin-top: 7px;
  }
  .dj-chip {
    padding: 2px 8px;
    border-radius: 7px;
    font: 600 10.5px "JetBrains Mono", monospace;
  }
  .dj-chip.kept {
    background: #e5f1ec;
    color: #2f6e60;
  }
  .dj-chip.merged {
    background: #eeeafb;
    color: #574ab0;
  }
  .dj-chip.pruned {
    background: #f3ece1;
    color: #9a6e1e;
  }
  .dj-note {
    font: 400 13.5px/1.55 "Hanken Grotesk";
    color: #5a5048;
    margin-top: 8px;
    max-width: 660px;
  }
</style>
