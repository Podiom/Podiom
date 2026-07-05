<script lang="ts">
  import { resolveSkillURL } from "../../lib/api";
  import type { SkillSummary } from "../../lib/types";

  let { onclose, onpick }: { onclose: () => void; onpick: (s: SkillSummary) => void } = $props();

  let url = $state("");
  let resolving = $state(false);
  let error = $state<string | null>(null);
  let candidates = $state<SkillSummary[]>([]);

  async function resolve() {
    resolving = true;
    error = null;
    candidates = [];
    try {
      const rows = await resolveSkillURL(url.trim());
      if (rows.length === 0) {
        error = "No SKILL.md found at that URL.";
      } else if (rows.length === 1) {
        onpick(rows[0]);
      } else {
        candidates = rows; // monorepo: let the user pick (FR-23)
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      resolving = false;
    }
  }
</script>

<div class="overlay" role="button" tabindex="0" onclick={onclose} onkeydown={(e) => e.key === "Escape" && onclose()}>
  <div class="modal" role="dialog" aria-modal="true" onclick={(e) => e.stopPropagation()} onkeydown={() => {}} tabindex="-1">
    <h2>Install from GitHub URL</h2>
    <p>Paste a repo, a subdirectory, or a link straight to a <code>SKILL.md</code>. You'll inspect it before anything installs.</p>
    <div class="field">
      <input
        bind:value={url}
        placeholder="https://github.com/owner/repo/tree/main/skills/my-skill"
        onkeydown={(e) => e.key === "Enter" && url.trim() && resolve()}
      />
      <button class="primary" disabled={resolving || !url.trim()} onclick={resolve}>
        {resolving ? "Resolving…" : "Resolve & inspect"}
      </button>
    </div>

    {#if error}<div class="err">{error}</div>{/if}

    {#if candidates.length > 0}
      <div class="pick">
        <b>{candidates.length} skills found — pick one:</b>
        {#each candidates as c (c.id)}
          <button class="cand" onclick={() => onpick(c)}>
            <b>{c.name}</b>
            <span>{c.ref.path || c.owner + "/" + c.ref.repo}</span>
          </button>
        {/each}
      </div>
    {/if}

    <div class="actions">
      <button class="ghost" onclick={onclose}>Close</button>
    </div>
  </div>
</div>

<style>
  .overlay { position: fixed; inset: 0; background: rgba(43, 37, 32, 0.32); display: flex; align-items: center; justify-content: center; padding: 24px; z-index: 60; }
  .modal { width: 100%; max-width: 560px; background: #fffdfb; border: 1px solid #ede4d9; border-radius: 18px; padding: 22px 24px; box-shadow: 0 24px 60px -20px rgba(43, 37, 32, 0.4); }
  h2 { margin: 0 0 6px; font: 800 18px "Hanken Grotesk"; color: #2b2520; }
  p { margin: 0 0 16px; font: 400 13px/1.55 "Hanken Grotesk"; color: #8a7f73; }
  code { font: 500 12px "JetBrains Mono", monospace; color: #7a6f62; }
  .field { display: flex; gap: 10px; }
  .field input { flex: 1; padding: 10px 12px; border: 1px solid #eae0d4; border-radius: 11px; background: #fffdfb; font: 500 13px "Hanken Grotesk"; color: #2b2520; outline: none; }
  .err { margin-top: 14px; padding: 10px 12px; border-radius: 10px; background: #f8ebe2; border: 1px solid #ecd3c2; color: #b0572f; font: 600 12.5px "Hanken Grotesk"; }
  .pick { margin-top: 16px; display: flex; flex-direction: column; gap: 8px; }
  .pick > b { font: 700 12.5px "Hanken Grotesk"; color: #6f6459; }
  .cand { display: flex; flex-direction: column; gap: 3px; align-items: flex-start; text-align: left; padding: 11px 13px; border: 1px solid #ede4d9; border-radius: 11px; background: #fbf7f1; cursor: pointer; }
  .cand b { font: 700 13.5px "JetBrains Mono", monospace; color: #241f1a; }
  .cand span { font: 500 11.5px "JetBrains Mono", monospace; color: #8a7f73; }
  .actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 20px; }
  .primary { border: 0; border-radius: 11px; background: #3f8f7e; color: #fff; padding: 9px 16px; font: 800 13px "Hanken Grotesk"; cursor: pointer; white-space: nowrap; }
  .primary:disabled { opacity: 0.5; cursor: default; }
  .ghost { border: 1px solid #eae0d4; border-radius: 11px; background: #fffdfb; color: #6f6459; padding: 9px 16px; font: 700 13px "Hanken Grotesk"; cursor: pointer; }
</style>
