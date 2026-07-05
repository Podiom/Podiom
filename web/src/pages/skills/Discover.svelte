<script lang="ts">
  import { searchSkills } from "../../lib/api";
  import type { SkillRegistry, SkillSummary } from "../../lib/types";
  import { formatDate, popularity, registryChip, registryLabel, scriptsChip } from "./shared";

  let {
    onopen,
    oninstall,
    onopengithub,
  }: {
    onopen: (registry: string, id: string) => void;
    oninstall: (s: SkillSummary) => void;
    onopengithub: () => void;
  } = $props();

  let query = $state("");
  let registry = $state<"all" | SkillRegistry>("all");
  let sort = $state<"relevance" | "popularity" | "recency">("relevance");
  let results = $state<SkillSummary[]>([]);
  let warnings = $state<string[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let featured = $state(true);

  let debounce: ReturnType<typeof setTimeout> | undefined;

  const FILTERS: { key: "all" | SkillRegistry; label: string }[] = [
    { key: "all", label: "All" },
    { key: "skillsmp", label: "SkillsMP" },
    { key: "anthropics", label: "Verified" },
    { key: "github", label: "GitHub" },
  ];

  $effect(() => {
    // Re-run search when query/registry/sort change (query is debounced).
    const q = query;
    void registry;
    void sort;
    clearTimeout(debounce);
    debounce = setTimeout(() => void run(), q.trim() ? 260 : 0);
    return () => clearTimeout(debounce);
  });

  async function run() {
    loading = true;
    error = null;
    try {
      const res = await searchSkills(query.trim(), registry, 1, sort);
      results = res.results;
      warnings = res.warnings ?? [];
      featured = query.trim() === "";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      results = [];
    } finally {
      loading = false;
    }
  }

  function installLabel(s: SkillSummary): string {
    if (s.installed) return s.update_available ? "Update" : "Installed";
    return "Install";
  }
</script>

<div class="discover">
  <div class="toolbar">
    <input bind:value={query} placeholder="Search across every registry…" />
    <select bind:value={sort} aria-label="Sort">
      <option value="relevance">Relevance</option>
      <option value="popularity">Popularity</option>
      <option value="recency">Recency</option>
    </select>
    <button class="gh" onclick={onopengithub}>Install from GitHub URL</button>
  </div>

  <div class="pills">
    {#each FILTERS as f (f.key)}
      <button class:active={registry === f.key} onclick={() => (registry = f.key)}>{f.label}</button>
    {/each}
  </div>

  {#each warnings as w (w)}
    <div class="warn">{w}</div>
  {/each}

  {#if error}
    <div class="err">{error}</div>
  {/if}

  {#if featured && !loading}
    <div class="section-label">Featured — curated, Verified skills</div>
  {/if}

  {#if loading && results.length === 0}
    <div class="state">Searching…</div>
  {:else if results.length === 0}
    <div class="state">No skills found. Try a different search, or install from a GitHub URL.</div>
  {:else}
    <div class="grid">
      {#each results as s (s.registry + ":" + s.id)}
        <article class="card">
          <button class="card-body" onclick={() => onopen(s.registry, s.id)}>
            <div class="card-top">
              <span style={registryChip(s.registry)}>{registryLabel(s.registry)}</span>
              {#if s.has_scripts}<span style={scriptsChip()}>scripts</span>{/if}
            </div>
            <h3>{s.name}</h3>
            <p>{s.description || "No description provided."}</p>
            <div class="card-meta">
              <span>{s.owner}</span>
              {#if popularity(s.stars, s.installs)}<span>· {popularity(s.stars, s.installs)}</span>{/if}
              {#if formatDate(s.updated_at)}<span>· {formatDate(s.updated_at)}</span>{/if}
            </div>
          </button>
          <div class="card-foot">
            <button
              class="install"
              class:done={s.installed && !s.update_available}
              disabled={s.installed && !s.update_available}
              onclick={() => oninstall(s)}
            >
              {installLabel(s)}
            </button>
          </div>
        </article>
      {/each}
    </div>
  {/if}
</div>

<style>
  .discover { display: flex; flex-direction: column; gap: 14px; }
  .toolbar { display: flex; gap: 12px; flex-wrap: wrap; margin-top: 4px; }
  .toolbar input { flex: 1; min-width: 240px; padding: 10px 12px; border: 1px solid #eae0d4; border-radius: 11px; background: #fffdfb; font: 500 13px "Hanken Grotesk"; color: #2b2520; outline: none; }
  select { padding: 10px 12px; border: 1px solid #eae0d4; border-radius: 11px; background: #fffdfb; font: 600 12.5px "Hanken Grotesk"; color: #4a4138; }
  .gh { border: 1px solid #cfe2da; border-radius: 11px; background: #eaf2ee; color: #2f6e60; padding: 10px 14px; font: 700 12.5px "Hanken Grotesk"; cursor: pointer; white-space: nowrap; }
  .pills { display: inline-flex; gap: 4px; padding: 4px; border-radius: 12px; background: #efe7dc; border: 1px solid #e6dbcc; align-self: flex-start; }
  .pills button { border: 0; background: transparent; color: #8a7f73; cursor: pointer; border-radius: 9px; padding: 7px 12px; font: 700 12.5px "Hanken Grotesk"; }
  .pills button.active { background: #fffdfb; color: #2b2520; box-shadow: 0 1px 3px rgba(43, 37, 32, 0.12); }
  .warn { padding: 10px 14px; border-radius: 11px; background: #fbf1dd; border: 1px solid #ecd8a6; color: #9a6b1a; font: 600 12.5px "Hanken Grotesk"; }
  .err { padding: 12px 14px; border-radius: 11px; background: #f8ebe2; border: 1px solid #ecd3c2; color: #b0572f; font: 600 13px "Hanken Grotesk"; }
  .section-label { font: 600 10px "JetBrains Mono", monospace; letter-spacing: 0.13em; color: #a89c8e; text-transform: uppercase; }
  .state { padding: 40px; text-align: center; color: #8a7f73; font: 500 14px "Hanken Grotesk"; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 12px; }
  .card { background: #fffdfb; border: 1px solid #ede4d9; border-radius: 16px; display: flex; flex-direction: column; overflow: hidden; box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04); }
  .card-body { border: 0; background: transparent; text-align: left; cursor: pointer; padding: 16px 18px 12px; display: flex; flex-direction: column; gap: 8px; }
  .card-top { display: flex; gap: 6px; flex-wrap: wrap; }
  .card h3 { margin: 0; font: 700 15px "JetBrains Mono", monospace; color: #241f1a; }
  .card p { margin: 0; font: 400 12.5px/1.5 "Hanken Grotesk"; color: #6f6459; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .card-meta { display: flex; gap: 4px; flex-wrap: wrap; font: 500 11px "JetBrains Mono", monospace; color: #a89c8e; }
  .card-foot { border-top: 1px solid #f1eae0; padding: 10px 18px; display: flex; justify-content: flex-end; }
  .install { border: 0; border-radius: 10px; background: #3f8f7e; color: #fff; padding: 7px 16px; font: 800 12.5px "Hanken Grotesk"; cursor: pointer; }
  .install.done { background: #efe7dc; color: #6f6459; cursor: default; }
  @media (max-width: 768px) {
    .toolbar { flex-direction: column; }
    .grid { grid-template-columns: 1fr; }
  }
</style>
