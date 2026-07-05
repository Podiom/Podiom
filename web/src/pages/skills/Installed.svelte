<script lang="ts">
  import { onMount } from "svelte";
  import { applySkillUpdate, checkSkillUpdate, listInstalledSkills, uninstallSkill } from "../../lib/api";
  import type { InstalledSkill, SkillUpdateStatus } from "../../lib/types";
  import { formatDate, registryChip, registryLabel, shortSHA } from "./shared";

  let items = $state<InstalledSkill[]>([]);
  let error = $state<string | null>(null);
  let loading = $state(true);
  let filter = $state<"all" | "managed" | "unmanaged">("all");
  let open = $state<Record<string, boolean>>({});
  let confirming = $state<string | null>(null);
  let busy = $state<Record<string, string>>({}); // name -> action label
  let updates = $state<Record<string, SkillUpdateStatus>>({});

  onMount(load);

  async function load() {
    loading = true;
    error = null;
    try {
      items = await listInstalledSkills();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  const filtered = $derived(
    items.filter((s) => filter === "all" || (filter === "managed" ? s.managed : !s.managed)),
  );
  const managedCount = $derived(items.filter((s) => s.managed).length);

  async function doUninstall(name: string) {
    busy = { ...busy, [name]: "Removing…" };
    error = null;
    try {
      await uninstallSkill(name);
      confirming = null;
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      const { [name]: _, ...rest } = busy;
      busy = rest;
    }
  }

  async function doCheck(name: string) {
    busy = { ...busy, [name]: "Checking…" };
    error = null;
    try {
      updates = { ...updates, [name]: await checkSkillUpdate(name) };
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      const { [name]: _, ...rest } = busy;
      busy = rest;
    }
  }

  async function doUpdate(name: string) {
    busy = { ...busy, [name]: "Updating…" };
    error = null;
    try {
      await applySkillUpdate(name, true);
      const { [name]: _, ...rest } = updates;
      updates = rest;
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      const { [name]: _, ...rest } = busy;
      busy = rest;
    }
  }
</script>

<div class="installed">
  <div class="pills">
    <button class:active={filter === "all"} onclick={() => (filter = "all")}>All <span>{items.length}</span></button>
    <button class:active={filter === "managed"} onclick={() => (filter = "managed")}>Managed <span>{managedCount}</span></button>
    <button class:active={filter === "unmanaged"} onclick={() => (filter = "unmanaged")}>Unmanaged <span>{items.length - managedCount}</span></button>
  </div>

  {#if error}<div class="err">{error}</div>{/if}

  {#if loading}
    <div class="state">Loading installed skills…</div>
  {:else if filtered.length === 0}
    <div class="state">No skills here yet. Head to Discover to install one.</div>
  {:else}
    <div class="rows">
      {#each filtered as s (s.name)}
        <article class="row">
          <button class="row-head" onclick={() => (open = { ...open, [s.name]: !open[s.name] })}>
            <span class:rot={open[s.name]} class="chev">›</span>
            <div class="row-main"><b>{s.name}</b><p>{s.description}</p></div>
            <div class="badges">
              {#if s.managed && s.registry}
                <span style={registryChip(s.registry)}>{registryLabel(s.registry)}</span>
              {:else}
                <span class="unmanaged">Unmanaged</span>
              {/if}
              {#if s.update_available}<span class="update">update available</span>{/if}
            </div>
          </button>

          {#if open[s.name]}
            <div class="expanded">
              {#if s.managed}
                <dl>
                  <div><dt>Registry</dt><dd>{registryLabel(s.registry!)}</dd></div>
                  {#if s.sha}<div><dt>Pinned</dt><dd><code>{shortSHA(s.sha)}</code></dd></div>{/if}
                  {#if s.installed_at}<div><dt>Installed</dt><dd>{formatDate(s.installed_at)}</dd></div>{/if}
                  {#if s.repo_url}<div><dt>Source</dt><dd><a href={s.repo_url} target="_blank" rel="noreferrer">{s.owner}/{s.repo}</a></dd></div>{/if}
                  <div><dt>Roots</dt><dd>{s.roots.join(", ")}</dd></div>
                </dl>

                {#if updates[s.name]?.available}
                  <div class="diff">
                    <b>Update available → {shortSHA(updates[s.name].latest_sha)}</b>
                    {#if updates[s.name].changed?.length}
                      <ul>
                        {#each updates[s.name].changed! as c (c)}<li>{c}</li>{/each}
                      </ul>
                    {/if}
                  </div>
                {/if}

                <div class="actions">
                  {#if updates[s.name]?.available}
                    <button class="primary" disabled={!!busy[s.name]} onclick={() => doUpdate(s.name)}>{busy[s.name] ?? "Apply update"}</button>
                  {:else}
                    <button class="ghost" disabled={!!busy[s.name]} onclick={() => doCheck(s.name)}>{busy[s.name] ?? "Check for updates"}</button>
                  {/if}
                  {#if confirming === s.name}
                    <span class="confirm">Remove {s.name}?</span>
                    <button class="danger" disabled={!!busy[s.name]} onclick={() => doUninstall(s.name)}>{busy[s.name] ?? "Confirm"}</button>
                    <button class="ghost" onclick={() => (confirming = null)}>Cancel</button>
                  {:else}
                    <button class="ghost" onclick={() => (confirming = s.name)}>Uninstall</button>
                  {/if}
                </div>
              {:else}
                <p class="muted">This skill was placed on disk by hand. Podiom never modifies or removes unmanaged skills.</p>
                <dl><div><dt>Roots</dt><dd>{s.roots.join(", ")}</dd></div></dl>
              {/if}
            </div>
          {/if}
        </article>
      {/each}
    </div>
  {/if}
</div>

<style>
  .installed { display: flex; flex-direction: column; gap: 14px; }
  .pills { display: inline-flex; gap: 4px; padding: 4px; border-radius: 12px; background: #efe7dc; border: 1px solid #e6dbcc; align-self: flex-start; }
  .pills button { border: 0; background: transparent; color: #8a7f73; cursor: pointer; border-radius: 9px; padding: 7px 12px; font: 700 12.5px "Hanken Grotesk"; }
  .pills button.active { background: #fffdfb; color: #2b2520; box-shadow: 0 1px 3px rgba(43, 37, 32, 0.12); }
  .pills span { margin-left: 6px; font: 600 11px "JetBrains Mono", monospace; color: #a89c8e; }
  .err { padding: 12px 14px; border-radius: 11px; background: #f8ebe2; border: 1px solid #ecd3c2; color: #b0572f; font: 600 13px "Hanken Grotesk"; }
  .state { padding: 40px; text-align: center; color: #8a7f73; font: 500 14px "Hanken Grotesk"; }
  .rows { display: flex; flex-direction: column; gap: 11px; }
  .row { background: #fffdfb; border: 1px solid #ede4d9; border-radius: 16px; overflow: hidden; }
  .row-head { width: 100%; border: 0; background: transparent; text-align: left; cursor: pointer; display: flex; gap: 12px; align-items: flex-start; padding: 16px 20px; }
  .row-main { min-width: 0; }
  .row-head b { font: 700 15px "JetBrains Mono", monospace; color: #241f1a; }
  .row-head p { margin: 5px 0 0; font: 400 13px/1.5 "Hanken Grotesk"; color: #6f6459; }
  .chev { display: inline-flex; width: 18px; height: 18px; align-items: center; justify-content: center; color: #b7ac9e; font-size: 22px; transition: transform 0.16s ease; flex: none; }
  .chev.rot { transform: rotate(90deg); }
  .badges { margin-left: auto; display: flex; gap: 6px; flex-wrap: wrap; justify-content: flex-end; align-items: center; }
  .unmanaged { padding: 4px 9px; border-radius: 8px; background: #efe7dc; border: 1px solid #e6dbcc; color: #8a7560; font: 600 11px "JetBrains Mono", monospace; }
  .update { padding: 4px 9px; border-radius: 8px; background: #fbf1dd; border: 1px solid #ecd8a6; color: #9a6b1a; font: 600 11px "JetBrains Mono", monospace; }
  .expanded { border-top: 1px solid #f1eae0; padding: 16px 22px 20px; }
  dl { display: flex; flex-direction: column; gap: 8px; margin: 0; }
  dl div { display: flex; gap: 12px; align-items: baseline; }
  dt { width: 90px; flex: none; font: 600 11px "JetBrains Mono", monospace; color: #a89c8e; text-transform: uppercase; }
  dd { margin: 0; font: 500 12.5px "Hanken Grotesk"; color: #4a4138; }
  dd a { color: #2f6e60; }
  code { font: 500 12px "JetBrains Mono", monospace; color: #7a6f62; }
  .diff { margin-top: 14px; padding: 12px 14px; border-radius: 12px; background: #fbf1dd; border: 1px solid #ecd8a6; }
  .diff b { font: 700 12.5px "Hanken Grotesk"; color: #9a6b1a; }
  .diff ul { margin: 8px 0 0; padding-left: 18px; }
  .diff li { font: 500 12px "JetBrains Mono", monospace; color: #7a6f62; }
  .actions { display: flex; gap: 10px; align-items: center; margin-top: 16px; flex-wrap: wrap; }
  .confirm { font: 600 12.5px "Hanken Grotesk"; color: #b0572f; }
  .primary { border: 0; border-radius: 10px; background: #3f8f7e; color: #fff; padding: 8px 15px; font: 800 12.5px "Hanken Grotesk"; cursor: pointer; }
  .ghost { border: 1px solid #eae0d4; border-radius: 10px; background: #fffdfb; color: #6f6459; padding: 8px 14px; font: 700 12.5px "Hanken Grotesk"; cursor: pointer; }
  .danger { border: 0; border-radius: 10px; background: #b0572f; color: #fff; padding: 8px 14px; font: 800 12.5px "Hanken Grotesk"; cursor: pointer; }
  .muted { font: 400 12.5px/1.55 "Hanken Grotesk"; color: #8a7f73; margin: 0 0 12px; }
  button:disabled { opacity: 0.6; cursor: default; }
</style>
