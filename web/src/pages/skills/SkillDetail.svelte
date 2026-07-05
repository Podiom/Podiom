<script lang="ts">
  import { skillDetail, skillFile } from "../../lib/api";
  import { renderMarkdown } from "../../lib/markdown";
  import type { SkillDetail } from "../../lib/types";
  import { registryChip, registryLabel, scriptsChip, shortSHA } from "./shared";

  let {
    registry,
    id,
    onback,
    oninstall,
  }: { registry: string; id: string; onback: () => void; oninstall: (d: SkillDetail) => void } = $props();

  let detail = $state<SkillDetail | null>(null);
  let error = $state<string | null>(null);
  let loading = $state(true);

  let viewerPath = $state<string | null>(null);
  let viewerContent = $state("");
  let viewerLoading = $state(false);

  $effect(() => {
    void load(registry, id);
  });

  async function load(reg: string, sid: string) {
    loading = true;
    error = null;
    detail = null;
    try {
      detail = await skillDetail(reg, sid);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function openFile(path: string) {
    viewerPath = path;
    viewerLoading = true;
    viewerContent = "";
    try {
      const file = await skillFile(registry, id, path);
      viewerContent = file.content;
    } catch (e) {
      viewerContent = e instanceof Error ? e.message : String(e);
    } finally {
      viewerLoading = false;
    }
  }

  // The SKILL.md body without its leading YAML frontmatter (shown as a table).
  const body = $derived.by(() => {
    if (!detail) return "";
    const md = detail.skill_md;
    const m = md.match(/^﻿?\s*---\s*[\s\S]*?\n---\s*\n?/);
    return renderMarkdown(m ? md.slice(m[0].length) : md);
  });

  function depth(path: string): number {
    return (path.match(/\//g) || []).length;
  }
  function leaf(path: string): string {
    const parts = path.replace(/\/$/, "").split("/");
    return parts[parts.length - 1];
  }
</script>

<div class="detail">
  <div class="topbar">
    <button class="back" onclick={onback}>← Back</button>
    {#if detail}
      <button class="primary" class:installed={detail.installed} onclick={() => oninstall(detail!)}>
        {detail.installed ? (detail.update_available ? "Update" : "Installed") : "Install"}
      </button>
    {/if}
  </div>

  {#if loading}
    <div class="state">Loading skill…</div>
  {:else if error}
    <div class="state err">Could not load skill: {error}</div>
  {:else if detail}
    <header class="hd">
      <div class="hd-main">
        <h1>{detail.name}</h1>
        <p>{detail.description}</p>
        <div class="hd-badges">
          <span style={registryChip(detail.registry)}>{registryLabel(detail.registry)}</span>
          {#if detail.has_executable}<span style={scriptsChip()}>Contains executable scripts</span>{/if}
          {#if detail.license}<span class="meta-chip">{detail.license}</span>{/if}
          {#if detail.ref.sha}<span class="meta-chip">pinned {shortSHA(detail.ref.sha)}</span>{/if}
        </div>
      </div>
      <a class="repo" href={`https://github.com/${detail.owner}/${detail.ref.repo}`} target="_blank" rel="noreferrer">
        {detail.owner}/{detail.ref.repo}
      </a>
    </header>

    {#if detail.scan_findings.length > 0}
      <section class="scan">
        <b>Static scan findings — Podiom informs, you decide</b>
        {#each detail.scan_findings as f (f.file + f.rule)}
          <div class="finding" class:warn={f.severity === "warn"}>
            <code>{f.file}</code>
            <span>{f.message}</span>
          </div>
        {/each}
      </section>
    {/if}

    <div class="panes">
      <div class="left">
        {#if detail.frontmatter.length > 0}
          <table class="fm">
            <tbody>
              {#each detail.frontmatter as f (f.key)}
                <tr><th>{f.key}</th><td>{f.value}</td></tr>
              {/each}
            </tbody>
          </table>
        {/if}
        <!-- eslint-disable-next-line svelte/no-at-html-tags -->
        <div class="md">{@html body}</div>
      </div>

      <div class="right">
        <div class="tree">
          <div class="tree-head">Files — inspect anything before installing</div>
          {#each detail.tree as node (node.path)}
            {#if node.is_dir}
              <div class="node dir" style={`padding-left:${depth(node.path) * 14 + 12}px`}>{leaf(node.path)}/</div>
            {:else}
              <button
                class="node file"
                class:active={viewerPath === node.path}
                style={`padding-left:${depth(node.path) * 14 + 12}px`}
                onclick={() => openFile(node.path)}
              >
                <span>{leaf(node.path)}</span>
                {#if node.executable}<em class="exec">exec</em>{/if}
              </button>
            {/if}
          {/each}
        </div>
        <div class="viewer">
          {#if !viewerPath}
            <div class="viewer-empty">Select a file to read its contents.</div>
          {:else if viewerLoading}
            <div class="viewer-empty">Loading {viewerPath}…</div>
          {:else}
            <div class="viewer-head">{viewerPath}</div>
            <pre>{viewerContent}</pre>
          {/if}
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .detail { display: flex; flex-direction: column; gap: 14px; }
  .topbar { display: flex; justify-content: space-between; align-items: center; }
  .back { border: 1px solid #eae0d4; border-radius: 10px; background: #fffdfb; color: #6f6459; padding: 8px 14px; font: 700 12.5px "Hanken Grotesk"; cursor: pointer; }
  .primary { border: 0; border-radius: 11px; background: #3f8f7e; color: #fff; padding: 9px 18px; font: 800 13px "Hanken Grotesk"; cursor: pointer; }
  .primary.installed { background: #efe7dc; color: #6f6459; }
  .state { padding: 40px; text-align: center; color: #8a7f73; font: 500 14px "Hanken Grotesk"; }
  .state.err { color: #b0572f; }
  .hd { display: flex; justify-content: space-between; gap: 16px; align-items: flex-start; background: #fffdfb; border: 1px solid #ede4d9; border-radius: 16px; padding: 18px 20px; }
  .hd h1 { margin: 0; font: 800 22px "Hanken Grotesk"; letter-spacing: -0.02em; color: #241f1a; }
  .hd p { margin: 6px 0 0; font: 400 13.5px/1.55 "Hanken Grotesk"; color: #6f6459; max-width: 620px; }
  .hd-badges { display: flex; gap: 7px; flex-wrap: wrap; margin-top: 12px; }
  .meta-chip { padding: 4px 9px; border-radius: 8px; background: #fbf7f1; border: 1px solid #efe6db; color: #7a6f62; font: 600 11px "JetBrains Mono", monospace; }
  .repo { font: 600 12px "JetBrains Mono", monospace; color: #2f6e60; text-decoration: none; white-space: nowrap; }
  .scan { background: #fbf1dd; border: 1px solid #ecd8a6; border-radius: 14px; padding: 14px 16px; }
  .scan > b { display: block; font: 700 12.5px "Hanken Grotesk"; color: #7a5a1a; margin-bottom: 10px; }
  .finding { display: flex; gap: 10px; align-items: baseline; padding: 5px 0; font: 500 12.5px "Hanken Grotesk"; color: #6f6459; }
  .finding.warn { color: #9a6b1a; }
  .finding code { font: 600 11px "JetBrains Mono", monospace; color: #8a7560; flex: none; }
  .panes { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
  .left { background: #fffdfb; border: 1px solid #ede4d9; border-radius: 16px; padding: 18px 20px; overflow: hidden; }
  .fm { width: 100%; border-collapse: collapse; margin-bottom: 16px; }
  .fm th { text-align: left; width: 130px; vertical-align: top; padding: 6px 10px 6px 0; font: 600 11px "JetBrains Mono", monospace; color: #a89c8e; text-transform: uppercase; }
  .fm td { padding: 6px 0; font: 500 12.5px "Hanken Grotesk"; color: #4a4138; word-break: break-word; }
  .md :global(h1), .md :global(h2), .md :global(h3) { font-family: "Hanken Grotesk"; color: #241f1a; }
  .md { font: 400 13.5px/1.6 "Hanken Grotesk"; color: #4a4138; overflow-wrap: break-word; }
  .md :global(pre) { background: #fbf7f1; border: 1px solid #efe6db; border-radius: 10px; padding: 12px 14px; overflow-x: auto; }
  .md :global(code) { font: 500 12px "JetBrains Mono", monospace; }
  .right { display: flex; flex-direction: column; gap: 12px; }
  .tree { background: #fffdfb; border: 1px solid #ede4d9; border-radius: 16px; padding: 8px 0; max-height: 260px; overflow-y: auto; }
  .tree-head { padding: 6px 14px 10px; font: 600 10px "JetBrains Mono", monospace; letter-spacing: 0.1em; color: #a89c8e; text-transform: uppercase; }
  .node { display: flex; align-items: center; gap: 8px; width: 100%; text-align: left; padding: 6px 12px; font: 500 12.5px "JetBrains Mono", monospace; }
  .node.dir { color: #a89c8e; }
  .node.file { border: 0; background: transparent; cursor: pointer; color: #4a4138; }
  .node.file:hover { background: #fbf7f1; }
  .node.file.active { background: #e7f0ec; color: #2f6e60; }
  .exec { font-style: normal; font: 600 10px "JetBrains Mono", monospace; color: #9a6b1a; background: #fbf1dd; border: 1px solid #ecd8a6; border-radius: 6px; padding: 1px 5px; margin-left: auto; }
  .viewer { background: #fffdfb; border: 1px solid #ede4d9; border-radius: 16px; overflow: hidden; min-height: 160px; }
  .viewer-empty { padding: 28px; text-align: center; color: #a89c8e; font: 500 13px "Hanken Grotesk"; }
  .viewer-head { padding: 10px 14px; border-bottom: 1px solid #f1eae0; font: 600 12px "JetBrains Mono", monospace; color: #7a6f62; }
  .viewer pre { margin: 0; padding: 14px 16px; font: 500 12px/1.6 "JetBrains Mono", monospace; color: #4a4138; white-space: pre-wrap; word-break: break-word; max-height: 360px; overflow-y: auto; }
  @media (max-width: 860px) {
    .panes { grid-template-columns: 1fr; }
  }
</style>
