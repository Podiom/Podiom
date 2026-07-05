<script lang="ts">
  import { installSkill } from "../../lib/api";
  import type { InstalledSkill } from "../../lib/types";
  import { installPath, shortSHA } from "./shared";

  interface Target {
    name: string;
    registry?: string;
    id?: string;
    url?: string;
    hasScripts: boolean;
    sha?: string;
    size?: number;
  }

  let {
    target,
    onclose,
    oninstalled,
  }: { target: Target; onclose: () => void; oninstalled: (s: InstalledSkill) => void } = $props();

  let acknowledged = $state(false);
  let installing = $state(false);
  let error = $state<string | null>(null);

  const canInstall = $derived(!installing && (!target.hasScripts || acknowledged));

  async function doInstall() {
    installing = true;
    error = null;
    try {
      const result = await installSkill({
        registry: target.registry as never,
        id: target.id,
        url: target.url,
        acknowledge: acknowledged || !target.hasScripts,
      });
      oninstalled(result);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      installing = false;
    }
  }

  function humanSize(bytes?: number): string {
    if (!bytes) return "";
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
</script>

<div
  class="overlay"
  role="button"
  tabindex="0"
  onclick={onclose}
  onkeydown={(e) => e.key === "Escape" && onclose()}
>
  <div class="modal" role="dialog" aria-modal="true" onclick={(e) => e.stopPropagation()} onkeydown={() => {}} tabindex="-1">
    <h2>Install {target.name}</h2>
    <dl>
      <div><dt>Writes to</dt><dd><code>{installPath(target.name)}</code></dd></div>
      {#if target.sha}<div><dt>Pinned commit</dt><dd><code>{shortSHA(target.sha)}</code></dd></div>{/if}
      {#if target.size}<div><dt>Size</dt><dd>{humanSize(target.size)}</dd></div>{/if}
    </dl>

    {#if target.hasScripts}
      <label class="warn">
        <input type="checkbox" bind:checked={acknowledged} />
        <span>This skill contains scripts that agents may execute. I understand and want to install it.</span>
      </label>
    {/if}

    {#if error}<div class="err">{error}</div>{/if}

    <div class="actions">
      <button class="ghost" onclick={onclose} disabled={installing}>Cancel</button>
      <button class="primary" disabled={!canInstall} onclick={doInstall}>
        {installing ? "Installing…" : "Install"}
      </button>
    </div>
  </div>
</div>

<style>
  .overlay { position: fixed; inset: 0; background: rgba(43, 37, 32, 0.32); display: flex; align-items: center; justify-content: center; padding: 24px; z-index: 60; }
  .modal { width: 100%; max-width: 460px; background: #fffdfb; border: 1px solid #ede4d9; border-radius: 18px; padding: 22px 24px; box-shadow: 0 24px 60px -20px rgba(43, 37, 32, 0.4); }
  h2 { margin: 0 0 14px; font: 800 18px "Hanken Grotesk"; color: #2b2520; }
  dl { margin: 0 0 4px; display: flex; flex-direction: column; gap: 9px; }
  dl div { display: flex; gap: 12px; align-items: baseline; }
  dt { width: 110px; flex: none; font: 600 11px "JetBrains Mono", monospace; color: #a89c8e; text-transform: uppercase; letter-spacing: 0.06em; }
  dd { margin: 0; font: 500 13px "Hanken Grotesk"; color: #4a4138; }
  code { font: 500 12px "JetBrains Mono", monospace; color: #7a6f62; }
  .warn { display: flex; gap: 10px; align-items: flex-start; margin-top: 16px; padding: 12px 14px; border-radius: 12px; background: #fbf1dd; border: 1px solid #ecd8a6; font: 500 12.5px/1.5 "Hanken Grotesk"; color: #7a5a1a; cursor: pointer; }
  .warn input { margin-top: 2px; }
  .err { margin-top: 14px; padding: 10px 12px; border-radius: 10px; background: #f8ebe2; border: 1px solid #ecd3c2; color: #b0572f; font: 600 12.5px "Hanken Grotesk"; }
  .actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 20px; }
  .primary { border: 0; border-radius: 11px; background: #3f8f7e; color: #fff; padding: 9px 18px; font: 800 13px "Hanken Grotesk"; cursor: pointer; }
  .primary:disabled { opacity: 0.5; cursor: default; }
  .ghost { border: 1px solid #eae0d4; border-radius: 11px; background: #fffdfb; color: #6f6459; padding: 9px 16px; font: 700 13px "Hanken Grotesk"; cursor: pointer; }
</style>
