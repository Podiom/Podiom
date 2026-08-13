<script lang="ts">
  import {
    createProfile,
    deleteAgent,
    deleteAgentAvatar,
    getAgent,
    getMCP,
    getMemory,
    listAgentTools,
    listDreams,
    listProfiles,
    removeAgentTool,
    updateAgent,
    uploadAgentAvatar,
  } from "../lib/api";
  import {
    capabilityKey,
    effortOptions as capabilityEffortOptions,
    loadProviderCapabilities,
    modelOptions as capabilityModelOptions,
  } from "../lib/capabilities";
  import DreamJournal from "../lib/DreamJournal.svelte";
  import DreamOverlay from "../lib/DreamOverlay.svelte";
  import AgentMarkdown from "../lib/AgentMarkdown.svelte";
  import WorkspaceFileLinks from "../lib/WorkspaceFileLinks.svelte";
  import MemoryPanel from "../lib/MemoryPanel.svelte";
  import ProviderLogo from "../lib/ProviderLogo.svelte";
  import { DEFAULT_PROVIDER, PROVIDERS, isProvider, providerMeta } from "../lib/providers";
  import AgentAvatar from "../lib/AgentAvatar.svelte";
  import { avatars } from "../lib/avatars.svelte";
  import { modeChip, providerChip } from "../lib/theme";
  import type { Agent, Dream, MCPServer, MemoryInfo, ProfileInfo, ProviderCapabilities, WorkspaceTool } from "../lib/types";
  import ConfirmModal from "../lib/ConfirmModal.svelte";

  // A fallback chain row: a provider plus an optional profile. Encodes to a
  // single token (profile name when set, otherwise the bare provider).
  type FbRow = { provider: string; profile: string };

  interface ChatTarget {
    sessionId?: string;
    agentName?: string;
    seed?: string;
  }

  let {
    agents = [],
    onHire = () => {},
    onOpenChat = (_t: ChatTarget) => {},
    onChanged = () => {},
    embedded = false,
  }: {
    agents?: Agent[];
    onHire?: () => void;
    onOpenChat?: (t: ChatTarget) => void;
    onChanged?: () => void;
    embedded?: boolean;
  } = $props();

  let selected = $state<Agent | null>(null);

  // Memory / dreaming state for the detail view.
  let memoryInfo = $state<MemoryInfo | null>(null);
  let dreams = $state<Dream[]>([]);
  let detailSoul = $state("");
  let dreamOverlayOpen = $state(false);

  // Workspace tools (installed via approved cli_tool access requests).
  let workspaceTools = $state<WorkspaceTool[]>([]);
  let pendingToolRemove = $state<WorkspaceTool | null>(null);
  let toolRemoveBusy = $state(false);
  let toolRemoveError = $state<string | null>(null);

  async function loadWorkspaceTools(name: string) {
    try {
      const tools = await listAgentTools(name);
      if (selected?.Name === name) workspaceTools = tools;
    } catch {
      // Optional surface; the panel simply stays empty.
    }
  }

  async function confirmToolRemove() {
    if (!selected || !pendingToolRemove) return;
    toolRemoveBusy = true;
    toolRemoveError = null;
    try {
      await removeAgentTool(selected.Name, pendingToolRemove.tool);
      pendingToolRemove = null;
      await loadWorkspaceTools(selected.Name);
    } catch (e) {
      toolRemoveError = e instanceof Error ? e.message : "Remove failed.";
    } finally {
      toolRemoveBusy = false;
    }
  }

  async function openAgent(a: Agent) {
    selected = a;
    memoryInfo = null;
    dreams = [];
    detailSoul = "";
    workspaceTools = [];
    void loadMemory(a.Name);
    void loadWorkspaceTools(a.Name);
    try {
      const detail = await getAgent(a.Name);
      if (selected?.Name === a.Name) detailSoul = detail.Soul;
    } catch {
      // SOUL is optional; leave blank on error.
    }
  }

  async function loadMemory(name: string) {
    try {
      const [info, journal] = await Promise.all([getMemory(name), listDreams(name)]);
      if (selected?.Name === name) {
        memoryInfo = info;
        dreams = journal;
      }
    } catch {
      // Memory endpoints are optional; the panel simply won't render.
    }
  }

  function onDreamOverlayDone() {
    // Refresh memory + journal once the dream settles so the panel updates.
    if (selected) void loadMemory(selected.Name);
    onChanged();
  }

  // --- Profile picture (avatar) ---
  let avatarInput = $state<HTMLInputElement | null>(null);
  let avatarBusy = $state(false);
  let avatarError = $state<string | null>(null);

  // normalizeToSquarePng cover-crops the chosen image to a small square PNG in
  // the browser, so the daemon only ever stores a tiny normalized file and the
  // backend needs no image library.
  async function normalizeToSquarePng(file: File, size: number): Promise<Blob> {
    const bitmap = await createImageBitmap(file);
    try {
      const canvas = document.createElement("canvas");
      canvas.width = size;
      canvas.height = size;
      const ctx = canvas.getContext("2d");
      if (!ctx) throw new Error("canvas unavailable");
      const scale = Math.max(size / bitmap.width, size / bitmap.height);
      const w = bitmap.width * scale;
      const h = bitmap.height * scale;
      ctx.drawImage(bitmap, (size - w) / 2, (size - h) / 2, w, h);
      return await new Promise<Blob>((resolve, reject) =>
        canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("encode failed"))), "image/png"),
      );
    } finally {
      bitmap.close();
    }
  }

  async function onAvatarPick(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = ""; // allow re-picking the same file later
    if (!file || !selected) return;
    avatarBusy = true;
    avatarError = null;
    try {
      const png = await normalizeToSquarePng(file, 256);
      const { AvatarUpdatedAt } = await uploadAgentAvatar(selected.Name, png);
      avatars.setVersion(selected.Name, AvatarUpdatedAt);
      onChanged();
    } catch (err) {
      avatarError = err instanceof Error ? err.message : "Upload failed.";
    } finally {
      avatarBusy = false;
    }
  }

  async function removeAvatar() {
    if (!selected) return;
    avatarBusy = true;
    avatarError = null;
    try {
      const { AvatarUpdatedAt } = await deleteAgentAvatar(selected.Name);
      avatars.setVersion(selected.Name, AvatarUpdatedAt);
      onChanged();
    } catch (err) {
      avatarError = err instanceof Error ? err.message : "Remove failed.";
    } finally {
      avatarBusy = false;
    }
  }

  // Edit modal state.
  let editOpen = $state(false);
  let edName = $state("");
  let edProvider = $state<string>(DEFAULT_PROVIDER);
  let edModel = $state("");
  let edEffort = $state("high");
  let edProfile = $state("");
  let edPermission = $state("approve");
  let edSoul = $state("");
  let edFallback = $state<FbRow[]>([]);
  let edMCPServers = $state<string[]>([]);
  let mcpServers = $state<MCPServer[]>([]);
  let profiles = $state<ProfileInfo[]>([]);
  let saving = $state(false);
  let editError = $state<string | null>(null);
  let inlineProfileOpen = $state(false);
  let inlineProfileName = $state("");
  let inlineProfilePath = $state("");
  let inlineProfileSaving = $state(false);

  // Delete modal state.
  let deleteOpen = $state(false);
  let deleteName = $state("");
  let deleting = $state(false);
  let deleteError = $state<string | null>(null);

  let capabilitiesByKey = $state<Record<string, ProviderCapabilities>>({});
  let loadingCapabilities = new Set<string>();
  const editCapabilityKey = $derived(capabilityKey(edProvider, edProfile));
  const editCapabilities = $derived(capabilitiesByKey[editCapabilityKey] ?? null);
  const modelOptions = $derived(capabilityModelOptions(editCapabilities, edModel));
  const effortOptions = $derived(capabilityEffortOptions(editCapabilities, edModel, edEffort));
  const edProfileOptions = $derived(profiles.filter((p) => p.Provider === edProvider));

  $effect(() => {
    if (editOpen && isProvider(edProvider)) {
      void ensureCapabilities(edProvider, edProfile);
    }
  });

  async function ensureCapabilities(provider: string, profile = "") {
    if (!isProvider(provider)) return;
    const key = capabilityKey(provider, profile);
    if (capabilitiesByKey[key] || loadingCapabilities.has(key)) return;
    loadingCapabilities.add(key);
    try {
      const caps = await loadProviderCapabilities(provider, profile);
      capabilitiesByKey = { ...capabilitiesByKey, [key]: caps };
    } catch {
      // Preserve any custom current values if capability fetch is unavailable.
    } finally {
      loadingCapabilities.delete(key);
    }
  }

  function specs(a: Agent): string {
    return `${a.Model || a.Provider} · ${a.Effort || "medium"} · profile: ${a.Profile || "default"}`;
  }

  // Decode a stored fallback chain into editable rows. A profile name resolves
  // to its provider; a bare provider token (or legacy "default") becomes a
  // profile-less row pinned to that provider.
  function decodeFallback(tokens: string[], agentProvider: string): FbRow[] {
    return (tokens ?? []).map((tok) => {
      if (isProvider(tok)) return { provider: tok, profile: "" };
      if (tok === "default") return { provider: agentProvider, profile: "" };
      const p = profiles.find((pr) => pr.Name === tok);
      return { provider: p ? p.Provider : agentProvider, profile: tok };
    });
  }

  function encodeFallback(rows: FbRow[]): string[] {
    return rows.map((r) => r.profile || r.provider);
  }

  // Profiles selectable for a row's provider, plus the row's current value if
  // it isn't in that set (so stale/unknown profiles survive a round-trip).
  function profileOptions(provider: string, current: string): string[] {
    const names = profiles.filter((p) => p.Provider === provider).map((p) => p.Name);
    if (current && !names.includes(current)) names.push(current);
    return names;
  }

  function setRowProvider(i: number, provider: string) {
    const row = edFallback[i];
    // Drop the profile if it doesn't belong to the newly chosen provider.
    const valid = profiles.some((p) => p.Name === row.profile && p.Provider === provider);
    edFallback[i] = { provider, profile: valid ? row.profile : "" };
  }

  function setProvider(provider: string) {
    edProvider = provider;
    inlineProfileOpen = false;
    if (!profiles.some((p) => p.Name === edProfile && p.Provider === provider)) edProfile = "";
    edFallback = edFallback.map((row) => {
      const valid = profiles.some((p) => p.Name === row.profile && p.Provider === row.provider);
      return { provider: row.provider, profile: valid ? row.profile : "" };
    });
    void ensureCapabilities(provider, edProfile);
  }

  function addRow() {
    edFallback = [...edFallback, { provider: edProvider, profile: "" }];
  }

  function removeRow(i: number) {
    edFallback = edFallback.filter((_, idx) => idx !== i);
  }

  function moveRow(i: number, delta: number) {
    const j = i + delta;
    if (j < 0 || j >= edFallback.length) return;
    const next = [...edFallback];
    [next[i], next[j]] = [next[j], next[i]];
    edFallback = next;
  }

  async function openEdit(a: Agent) {
    editError = null;
    edName = a.Name;
    edProvider = a.Provider;
    edModel = a.Model;
    edEffort = a.Effort || "high";
    edProfile = a.Profile;
    edPermission = a.PermissionMode;
    edSoul = "";
    edFallback = decodeFallback(a.Fallback, a.Provider);
    edMCPServers = [...(a.MCPServers ?? [])];
    mcpServers = [];
    inlineProfileOpen = false;
    inlineProfileName = "";
    inlineProfilePath = "";
    editOpen = true;
    void ensureCapabilities(edProvider, edProfile);
    try {
      profiles = await listProfiles();
      // Re-decode now that provider info is available for profile tokens.
      edFallback = decodeFallback(a.Fallback, a.Provider);
      void ensureCapabilities(edProvider, edProfile);
    } catch {
      // Profiles are optional; dropdowns just stay empty.
    }
    try {
      const detail = await getAgent(a.Name);
      edSoul = detail.Soul;
      edMCPServers = [...(detail.MCPServers ?? [])];
    } catch {
      // SOUL is optional; leave blank on error.
    }
    try {
      const mcp = await getMCP();
      mcpServers = mcp.servers;
      edMCPServers = [...(mcp.assignments[a.Name] ?? edMCPServers)];
    } catch {
      // MCP catalogue is optional; editing core agent settings still works.
    }
  }

  async function save() {
    saving = true;
    editError = null;
    try {
      const detail = await updateAgent(edName, {
        provider: edProvider,
        model: edModel,
        effort: edEffort,
        profile: edProfile,
        permission_mode: edPermission,
        fallback: encodeFallback(edFallback),
        mcp_servers: edMCPServers,
        soul: edSoul,
      });
      // Reflect the saved engine fields in the detail view.
      selected = {
        Name: detail.Name,
        Provider: detail.Provider,
        Profile: detail.Profile,
        Model: detail.Model,
        Effort: detail.Effort,
        PermissionMode: detail.PermissionMode,
        Fallback: detail.Fallback,
        MCPServers: detail.MCPServers,
      };
      editOpen = false;
      onChanged();
    } catch (e) {
      editError = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  async function createInlineProfile() {
    inlineProfileSaving = true;
    editError = null;
    try {
      const created = await createProfile({
        name: inlineProfileName.trim(),
        provider: edProvider,
        config_dir: "",
        home_dir: "",
        [providerMeta(edProvider).profileDir.bodyKey]: inlineProfilePath.trim(),
      });
      profiles = [created, ...profiles.filter((p) => p.Name !== created.Name)];
      edProfile = created.Name;
      void ensureCapabilities(edProvider, edProfile);
      inlineProfileOpen = false;
      inlineProfileName = "";
      inlineProfilePath = "";
    } catch (e) {
      editError = e instanceof Error ? e.message : String(e);
    } finally {
      inlineProfileSaving = false;
    }
  }

  function openDelete(a: Agent) {
    deleteName = "";
    deleteError = null;
    deleteOpen = true;
    edName = a.Name;
  }

  async function confirmDelete() {
    if (!selected || deleteName.trim() !== selected.Name) return;
    deleting = true;
    deleteError = null;
    try {
      await deleteAgent(selected.Name, deleteName);
      deleteOpen = false;
      selected = null;
      onChanged();
    } catch (e) {
      deleteError = e instanceof Error ? e.message : String(e);
    } finally {
      deleting = false;
    }
  }

  function seg(on: boolean): string {
    return (
      "flex:1;padding:11px;border-radius:11px;cursor:pointer;font:600 13.5px 'Hanken Grotesk';" +
      (on
        ? "border:1px solid #BFE0D6;background:#E3F1EC;color:#2F6E60"
        : "border:1px solid #EAE0D4;background:#fff;color:#6F6459")
    );
  }

  function chip(on: boolean): string {
    return (
      "padding:6px 12px;border-radius:9px;cursor:pointer;font:600 12px 'JetBrains Mono',monospace;" +
      (on
        ? "border:1px solid #BFE0D6;background:#E3F1EC;color:#2F6E60"
        : "border:1px solid #EAE0D4;background:#fff;color:#6F6459")
    );
  }

  function toggleMCP(name: string) {
    if (edMCPServers.includes(name)) {
      edMCPServers = edMCPServers.filter((s) => s !== name);
    } else {
      edMCPServers = [...edMCPServers, name];
    }
  }

  function serverSummary(s: MCPServer): string {
    if (s.transport === "http") return s.url || "http";
    return [s.command, ...(s.args ?? [])].filter(Boolean).join(" ") || "stdio";
  }
</script>

{#if !selected}
  <div class="page" class:embedded>
    <header style="margin-bottom:22px">
      <div class="agents-head-row" style="display:flex;align-items:flex-end;gap:14px">
        <div>
          <h1 style="margin:0;font:800 24px 'Hanken Grotesk';letter-spacing:-.02em">Agents</h1>
          <p style="margin:3px 0 0;font:400 13px 'Hanken Grotesk';color:var(--muted-2)">Your roster of named colleagues. Each owns a workspace, a soul, and its own defaults.</p>
        </div>
        <span class="spacer"></span>
        <button class="head-cta" onclick={onHire}>+ Hire agent</button>
      </div>
    </header>

    <div class="roster">
      {#each agents as a}
        <button class="agent-card" onclick={() => openAgent(a)}>
          <AgentAvatar name={a.Name} size={56} radius={17} fontSize={23} />
          <div class="ac-body">
            <div class="ac-head">
              <span class="ac-name">{a.Name}</span>
              <span style={providerChip(a.Provider)}>{a.Provider}</span>
              <span style={modeChip(a.PermissionMode)}>{a.PermissionMode}</span>
            </div>
            <div class="ac-specs mono">{specs(a)}</div>
          </div>
        </button>
      {/each}
      <button class="agent-add" onclick={onHire}>
        <span style="font-size:26px;line-height:1">+</span> Hire a new agent
      </button>
    </div>
  </div>
{:else}
  {@const a = selected}
  <div class="page" class:embedded>
    <button class="back-btn" onclick={() => { selected = null; memoryInfo = null; dreams = []; }}>← All agents</button>
    <div class="ad-top">
      <div class="ad-avatar">
        <button
          type="button"
          class="ad-avatar-btn"
          onclick={() => avatarInput?.click()}
          disabled={avatarBusy}
          title="Upload a profile picture"
        >
          <AgentAvatar name={a.Name} size={80} radius={24} fontSize={32} />
          <span class="ad-avatar-edit" aria-hidden="true">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z" /><circle cx="12" cy="13" r="4" /></svg>
          </span>
        </button>
        {#if avatars.version(a.Name)}
          <button type="button" class="ad-avatar-remove" onclick={removeAvatar} disabled={avatarBusy}>Remove</button>
        {/if}
        <input bind:this={avatarInput} type="file" accept="image/*" hidden onchange={onAvatarPick} />
        {#if avatarError}<span class="ad-avatar-err">{avatarError}</span>{/if}
      </div>
      <div style="flex:1">
        <div class="ad-headrow">
          <span class="ad-name">{a.Name}</span>
          <span style={providerChip(a.Provider)}>{a.Provider}</span>
          <span style={modeChip(a.PermissionMode)}>{a.PermissionMode}</span>
        </div>
        <div class="ad-soul">Runs on {a.Provider} · {a.Model || "provider default"} · effort {a.Effort || "medium"}.</div>
        <div class="ad-actions">
          <button class="head-cta" onclick={() => onOpenChat({ agentName: a.Name })}>Start a chat</button>
          <button class="ad-edit" onclick={() => openEdit(a)}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" /></svg>
            Edit
          </button>
          <button class="ad-delete" onclick={() => openDelete(a)}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18" /><path d="M8 6V4h8v2" /><path d="M19 6l-1 14H6L5 6" /><path d="M10 11v5" /><path d="M14 11v5" /></svg>
            Delete
          </button>
        </div>
      </div>
    </div>

    <div class="ad-diptych">
      <div class="ad-memory">
        {#if memoryInfo}
          <MemoryPanel
            agentName={a.Name}
            memory={memoryInfo}
            {dreams}
            onChanged={() => loadMemory(a.Name)}
            onDreamNow={() => (dreamOverlayOpen = true)}
          />
        {:else}
          <div class="ad-panel ad-memory-loading">Loading memory…</div>
        {/if}
      </div>

      <div class="ad-side">
        <div class="ad-panel">
          <div class="ad-soul-head">
            <span class="ad-soul-title">Soul</span>
            <span class="ad-soul-chip">yours · static</span>
          </div>
          <div class="ad-soul-note">Who {a.Name} is. You author this; it never drifts.</div>
          {#if detailSoul.trim()}
            <div class="ad-soul-body"><AgentMarkdown content={detailSoul} /></div>
          {:else}
            <div class="ad-soul-empty">No soul written yet.</div>
          {/if}
        </div>

        <div class="ad-panel">
          <div class="label-mono" style="margin-bottom:14px">defaults</div>
          <div class="ad-spec"><span>Provider</span><span class="mono">{a.Provider}</span></div>
          <div class="ad-spec"><span>Model</span><span class="mono">{a.Model || "provider default"}</span></div>
          <div class="ad-spec"><span>Effort</span><span class="mono">{a.Effort || "medium"}</span></div>
          <div class="ad-spec"><span>Profile</span><span class="mono">{a.Profile || "default"}</span></div>
          <div class="ad-spec"><span>Fallback</span><span class="mono">{a.Fallback && a.Fallback.length ? a.Fallback.join(" → ") : "none"}</span></div>
          <div class="ad-spec"><span>MCP</span><span class="mono">{a.MCPServers && a.MCPServers.length ? a.MCPServers.join(", ") : "none"}</span></div>
          <div class="ad-spec"><span>Permission</span><span class="mono">{a.PermissionMode}</span></div>
          <div class="ad-spec"><span>Workspace</span><span class="mono">~/.podiom/agents/{a.Name}</span></div>
        </div>

        {#if workspaceTools.length > 0}
          <div class="ad-panel">
            <div class="label-mono" style="margin-bottom:6px">workspace tools</div>
            <div class="ad-tools-note">Installed by Podiom on approved goal requests; only {a.Name} sees them on PATH.</div>
            {#each workspaceTools as t (t.tool)}
              <div class="ad-tool">
                <div class="ad-tool-main">
                  <span class="ad-tool-name mono">{t.tool}</span>
                  <span class="ad-tool-meta mono">{t.installer}{t.version ? ` · ${t.version}` : ""}</span>
                  {#if t.broken}<span class="ad-tool-broken mono" title="The manifest lists this tool but its executable is missing on disk.">broken</span>{/if}
                </div>
                {#if t.version_output}
                  <div class="ad-tool-version mono">{t.version_output}</div>
                {/if}
                <button class="ad-tool-remove" title="Remove this tool" onclick={() => { toolRemoveError = null; pendingToolRemove = t; }}>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
                </button>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    </div>

    <DreamJournal agentName={a.Name} {dreams} />
  </div>

  {#if pendingToolRemove}
    <ConfirmModal
      title={`Remove ${pendingToolRemove.tool}?`}
      message={`This uninstalls ${pendingToolRemove.tool} from ${a.Name}'s workspace and removes it from the manifest. The agent can request it again if it still needs it.`}
      confirmLabel="Remove tool"
      busy={toolRemoveBusy}
      error={toolRemoveError}
      onConfirm={confirmToolRemove}
      onCancel={() => (pendingToolRemove = null)}
    />
  {/if}

  {#if dreamOverlayOpen}
    <DreamOverlay
      agentName={a.Name}
      onClose={() => (dreamOverlayOpen = false)}
      onDone={onDreamOverlayDone}
    />
  {/if}
{/if}

<!-- ===== Edit modal ===== -->
{#if editOpen}
  <div class="modal-backdrop" role="presentation" onclick={() => (editOpen = false)}>
    <div class="modal-card ed-modal" role="dialog" aria-modal="true" aria-label="Edit agent" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <div class="modal-head">
        <div class="modal-title">Edit {edName}</div>
        <div class="modal-sub">Tune how this colleague runs and rewrite their SOUL.md. Changes save to their durable defaults and workspace.</div>
      </div>
      <div class="modal-body">
        {#if editError}<div class="error-banner" style="margin-bottom:14px">{editError}</div>{/if}

        <div class="label-mono" style="margin-bottom:8px">provider</div>
        <div style="display:flex;gap:9px">
          {#each PROVIDERS as p (p.id)}
            <button class="provider-choice" style={seg(edProvider === p.id)} onclick={() => setProvider(p.id)}>
              <ProviderLogo provider={p.id} />{p.label}
            </button>
          {/each}
        </div>

        <div class="ed-row">
          <span class="ed-key">model</span>
          <div class="ed-chips">
            {#each modelOptions as m}
              <button style={chip(m === edModel)} onclick={() => (edModel = m)}>{m}</button>
            {/each}
            <input class="field-input mono" style="max-width:220px;padding:8px 10px;font-size:12px" bind:value={edModel} placeholder="custom model" />
          </div>
        </div>
        <div class="ed-row">
          <span class="ed-key">effort</span>
          <div class="ed-chips">
            {#each effortOptions as e}
              <button style={chip(e === edEffort)} onclick={() => (edEffort = e)}>{e}</button>
            {/each}
          </div>
        </div>
        <div class="ed-row">
          <span class="ed-key">profile</span>
          <div class="profile-pick">
            <div class="ed-chips">
              <button style={chip(edProfile === "")} onclick={() => (edProfile = "")}>default · global login</button>
              {#each edProfileOptions as p}
                <button style={chip(edProfile === p.Name)} onclick={() => (edProfile = p.Name)}>{p.Name}</button>
              {/each}
              <button class="chip-new" onclick={() => { inlineProfileOpen = !inlineProfileOpen; inlineProfileName = ""; inlineProfilePath = ""; }}>
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M12 5v14" /><path d="M5 12h14" /></svg>New
              </button>
            </div>
            {#if inlineProfileOpen}
              <div class="inline-profile">
                <div class="np-title">new profile · uses selected provider</div>
                <input class="field-input" bind:value={inlineProfileName} placeholder="profile name" />
                <input class="field-input mono" bind:value={inlineProfilePath} placeholder={providerMeta(edProvider).profileDir.placeholder} />
                <div class="np-actions">
                  <button class="np-create" disabled={inlineProfileSaving || !inlineProfileName.trim()} onclick={createInlineProfile}>
                    {inlineProfileSaving ? "Saving…" : "Create & select"}
                  </button>
                  <button class="np-cancel" onclick={() => (inlineProfileOpen = false)}>Cancel</button>
                </div>
              </div>
            {/if}
          </div>
        </div>

        <div class="label-mono" style="margin:18px 0 4px">fallback chain</div>
        <div class="fb-hint">Tried in order when the provider rate-limits. Pick a provider; add a profile if one exists.</div>
        {#each edFallback as row, i (i)}
          <div class="fb-row">
            <div class="fb-provs">
              {#each PROVIDERS as p (p.id)}
                <button class="provider-choice" style={chip(row.provider === p.id)} onclick={() => setRowProvider(i, p.id)}>
                  <ProviderLogo provider={p.id} size={13} />{p.id}
                </button>
              {/each}
            </div>
            <select class="fb-select" bind:value={row.profile}>
              <option value="">no profile</option>
              {#each profileOptions(row.provider, row.profile) as p}
                <option value={p}>{p}</option>
              {/each}
            </select>
            <button class="fb-move" title="Move up" disabled={i === 0} onclick={() => moveRow(i, -1)} aria-label="Move up">↑</button>
            <button class="fb-move" title="Move down" disabled={i === edFallback.length - 1} onclick={() => moveRow(i, 1)} aria-label="Move down">↓</button>
            <button class="fb-x" title="Remove" onclick={() => removeRow(i)} aria-label="Remove">×</button>
          </div>
        {/each}
        <button class="fb-add" onclick={addRow}>+ Add fallback</button>

        <div class="label-mono" style="margin:18px 0 8px">MCP servers</div>
        {#if mcpServers.length}
          <div class="mcp-picks">
            {#each mcpServers as server (server.name)}
              <button class="mcp-pick" class:on={edMCPServers.includes(server.name)} onclick={() => toggleMCP(server.name)}>
                <span class="mcp-name">{server.name}</span>
                <span class="mcp-meta">{server.transport} · {serverSummary(server)}</span>
              </button>
            {/each}
          </div>
        {:else}
          <div class="fb-hint">No MCP servers are in the catalogue yet.</div>
        {/if}

        <div class="ed-row">
          <span class="ed-key">permission</span>
          <div class="ed-chips">
            {#each ["approve", "auto", "yolo"] as p}
              <button style={chip(p === edPermission)} onclick={() => (edPermission = p)}>{p}</button>
            {/each}
          </div>
        </div>

        <div class="label-mono" style="margin:20px 0 8px">SOUL.md</div>
        <textarea class="field-area mono" rows="8" bind:value={edSoul} placeholder="# Name&#10;&#10;## Who you are…" style="font:400 12.5px/1.7 'JetBrains Mono',monospace;min-height:160px;white-space:pre"></textarea>
        <WorkspaceFileLinks content={edSoul} />

        <div style="display:flex;gap:9px;margin-top:22px">
          <button class="ed-cancel" onclick={() => (editOpen = false)}>Cancel</button>
          <button class="modal-cta" style="margin-top:0;flex:1" disabled={saving} onclick={save}>{saving ? "Saving…" : "Save changes"}</button>
        </div>
      </div>
    </div>
  </div>
{/if}

<!-- ===== Delete modal ===== -->
{#if deleteOpen && selected}
  <div class="modal-backdrop" role="presentation" onclick={() => (deleteOpen = false)}>
    <div class="modal-card delete-modal" role="dialog" aria-modal="true" aria-label="Delete agent" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <div class="modal-head">
        <div class="modal-title">Delete {selected.Name}</div>
        <div class="modal-sub">This archives sessions into <span class="mono">~/.podiom/agents/{selected.Name}/workspace/session-archive</span>, removes them from active history, and deletes the agent from Podiom and config.yaml. Agent files are preserved.</div>
      </div>
      <div class="modal-body">
        {#if deleteError}<div class="error-banner" style="margin-bottom:14px">{deleteError}</div>{/if}
        <div class="label-mono" style="margin-bottom:8px">type agent name</div>
        <input class="field-input mono" bind:value={deleteName} placeholder={selected.Name} autocomplete="off" />
        <div style="display:flex;gap:9px;margin-top:22px">
          <button class="ed-cancel" onclick={() => (deleteOpen = false)}>Cancel</button>
          <button class="delete-confirm" disabled={deleting || deleteName.trim() !== selected.Name} onclick={confirmDelete}>{deleting ? "Deleting..." : "Delete agent"}</button>
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
  /* Embedded inside the Settings tab: shed the full-height page chrome. */
  .page.embedded {
    flex: none;
    overflow: visible;
    padding: 0;
  }

  .roster {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 330px), 1fr));
    gap: 16px;
  }

  .agent-card {
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 20px;
    padding: 20px;
    cursor: pointer;
    box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04), 0 16px 40px -26px rgba(43, 37, 32, 0.22);
    display: flex;
    gap: 15px;
    text-align: left;
  }

  .mcp-picks {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 230px), 1fr));
    gap: 8px;
    margin-bottom: 10px;
  }

  .mcp-pick {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 10px 12px;
    border: 1px solid #eae0d4;
    border-radius: 11px;
    background: #fff;
    color: #4a4138;
    text-align: left;
    cursor: pointer;
  }

  .mcp-pick.on {
    border-color: #bfe0d6;
    background: #e3f1ec;
  }

  .mcp-name {
    font: 700 12.5px "JetBrains Mono", monospace;
    color: #241f1a;
  }

  .mcp-meta {
    font: 500 11px "Hanken Grotesk";
    color: #7a6f62;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .agent-card:hover {
    border-color: #d9cdbe;
  }

  .ac-body {
    flex: 1;
    min-width: 0;
  }

  .ac-head {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .ac-name {
    font: 800 18px "Hanken Grotesk";
  }

  .ac-specs {
    font: 400 11px "JetBrains Mono", monospace;
    color: var(--faint);
    margin-top: 9px;
  }

  .agent-add {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 7px;
    min-height: 140px;
    border: 1.5px dashed #decfbe;
    border-radius: 20px;
    color: #a8825e;
    font: 600 15px "Hanken Grotesk";
    cursor: pointer;
    background: rgba(255, 253, 251, 0.5);
  }

  .back-btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    border: none;
    background: none;
    cursor: pointer;
    font: 600 13px "Hanken Grotesk";
    color: #a8825e;
    margin-bottom: 18px;
  }

  .ad-top {
    display: flex;
    gap: 20px;
    align-items: flex-start;
    max-width: 880px;
  }

  .ad-avatar {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    flex: none;
  }

  .ad-avatar-btn {
    position: relative;
    padding: 0;
    border: none;
    background: none;
    cursor: pointer;
    border-radius: 24px;
    line-height: 0;
  }

  .ad-avatar-btn:disabled {
    cursor: default;
    opacity: 0.6;
  }

  .ad-avatar-edit {
    position: absolute;
    right: -4px;
    bottom: -4px;
    width: 28px;
    height: 28px;
    border-radius: 999px;
    background: #fff;
    border: 1px solid var(--field-line);
    color: var(--muted);
    display: flex;
    align-items: center;
    justify-content: center;
    box-shadow: 0 4px 10px -4px rgba(80, 40, 20, 0.4);
  }

  .ad-avatar-btn:hover .ad-avatar-edit {
    color: var(--teal-deep);
  }

  .ad-avatar-remove {
    border: none;
    background: none;
    color: var(--muted);
    font: 600 12px "Hanken Grotesk";
    cursor: pointer;
    text-decoration: underline;
  }

  .ad-avatar-remove:disabled {
    cursor: default;
    opacity: 0.6;
  }

  .ad-avatar-err {
    color: #a23e22;
    font: 500 11.5px "Hanken Grotesk";
    max-width: 90px;
    text-align: center;
  }

  .ad-headrow {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }

  .ad-name {
    font: 800 28px "Hanken Grotesk";
    letter-spacing: -0.02em;
  }

  .ad-soul {
    font: 400 15px/1.6 "Hanken Grotesk";
    color: var(--muted);
    margin-top: 8px;
    max-width: 560px;
    font-style: italic;
  }

  .ad-actions {
    display: flex;
    gap: 9px;
    margin-top: 16px;
  }

  .ad-edit {
    padding: 9px 16px;
    border: 1px solid var(--field-line);
    border-radius: 11px;
    background: #fff;
    color: var(--muted);
    font: 600 13.5px "Hanken Grotesk";
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }

  .ad-delete {
    padding: 9px 16px;
    border: 1px solid #e7c3b5;
    border-radius: 11px;
    background: #fff;
    color: #a23e22;
    font: 600 13.5px "Hanken Grotesk";
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }

  .ad-diptych {
    display: grid;
    grid-template-columns: 1.55fr 1fr;
    gap: 16px;
    margin-top: 26px;
    max-width: 1060px;
    align-items: start;
  }

  .ad-side {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .ad-memory-loading {
    color: var(--muted-2);
    font: 400 13px "Hanken Grotesk";
  }

  .ad-soul-head {
    display: flex;
    align-items: center;
    gap: 9px;
    margin-bottom: 4px;
    flex-wrap: wrap;
  }
  .ad-soul-title {
    font: 800 17px "Hanken Grotesk";
  }
  .ad-soul-chip {
    padding: 3px 9px;
    border-radius: 999px;
    background: #fbeae0;
    border: 1px solid #f2d6c5;
    font: 600 10px "JetBrains Mono", monospace;
    color: #b14e2a;
  }
  .ad-soul-note {
    font: 400 12.5px/1.5 "Hanken Grotesk";
    color: var(--muted-2);
    margin-bottom: 12px;
  }
  .ad-soul-body {
    font: 400 13px/1.6 "Hanken Grotesk";
    color: #5a5048;
    word-break: break-word;
  }
  .ad-soul-body :global(h1),
  .ad-soul-body :global(h2),
  .ad-soul-body :global(h3) {
    font-size: 14px;
    margin: 10px 0 4px;
  }
  .ad-soul-body :global(ul) {
    margin: 4px 0;
    padding-left: 18px;
  }
  .ad-soul-empty {
    font: 400 13px "Hanken Grotesk";
    color: var(--muted-2);
  }

  @media (max-width: 860px) {
    .ad-diptych {
      grid-template-columns: 1fr;
    }
  }

  .ad-panel {
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 18px;
    padding: 20px;
  }

  .ad-spec {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 9px 0;
    border-top: 1px solid #f1eae0;
    font: 400 13.5px "Hanken Grotesk";
    color: var(--muted);
  }

  .ad-spec span:last-child {
    font: 600 12.5px "JetBrains Mono", monospace;
    color: var(--ink);
  }

  /* edit modal */
  .ed-modal {
    width: 520px;
    max-width: 94vw;
  }

  .delete-modal {
    width: 480px;
    max-width: 94vw;
  }

  .ed-row {
    display: flex;
    align-items: center;
    gap: 9px;
    margin-top: 11px;
  }

  .ed-key {
    font: 500 11px "Hanken Grotesk";
    color: #9a8e80;
    width: 66px;
    flex: none;
  }

  .ed-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .fb-hint {
    font: 400 11.5px "Hanken Grotesk";
    color: #9a8e80;
    margin-bottom: 10px;
  }

  .fb-row {
    display: flex;
    align-items: center;
    gap: 7px;
    margin-bottom: 8px;
  }

  .fb-provs {
    display: flex;
    gap: 6px;
    flex: none;
  }

  .fb-select {
    flex: 1;
    min-width: 0;
    padding: 7px 10px;
    border: 1px solid var(--field-line);
    border-radius: 9px;
    background: #fff;
    color: var(--ink);
    font: 500 12px "JetBrains Mono", monospace;
    cursor: pointer;
  }

  .fb-move,
  .fb-x {
    flex: none;
    width: 30px;
    height: 30px;
    border: 1px solid var(--field-line);
    border-radius: 9px;
    background: #fff;
    color: var(--muted);
    font-size: 15px;
    line-height: 1;
    cursor: pointer;
  }

  .fb-x {
    border-color: #e7c3b5;
    color: #a23e22;
  }

  .fb-move:disabled {
    opacity: 0.35;
    cursor: not-allowed;
  }

  .fb-add {
    margin-top: 2px;
    padding: 8px 14px;
    border: 1.5px dashed #decfbe;
    border-radius: 10px;
    background: rgba(255, 253, 251, 0.5);
    color: #a8825e;
    font: 600 12.5px "Hanken Grotesk";
    cursor: pointer;
  }

  .profile-pick {
    flex: 1;
    min-width: 0;
  }

  .chip-new {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 6px 12px;
    border-radius: 9px;
    cursor: pointer;
    font: 600 12px "JetBrains Mono", monospace;
    border: 1px dashed #c9bdad;
    background: transparent;
    color: #8a7560;
  }

  .inline-profile {
    margin-top: 8px;
    padding: 12px;
    border: 1px solid var(--line-3);
    border-radius: 12px;
    background: var(--surface-3);
  }

  .inline-profile .field-input {
    margin-top: 8px;
  }

  .np-title {
    font: 600 10px "JetBrains Mono", monospace;
    letter-spacing: 0.1em;
    color: var(--faint);
    text-transform: uppercase;
  }

  .np-actions {
    display: flex;
    gap: 9px;
    margin-top: 12px;
  }

  .np-create {
    border: none;
    border-radius: 11px;
    padding: 9px 18px;
    background: var(--teal);
    color: #fff;
    font: 700 13px "Hanken Grotesk";
    cursor: pointer;
  }

  .np-create:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .np-cancel {
    border: 1px solid var(--field-line);
    border-radius: 11px;
    padding: 9px 16px;
    background: #fff;
    color: var(--muted-2);
    font: 600 13px "Hanken Grotesk";
    cursor: pointer;
  }

  .ed-cancel {
    flex: none;
    padding: 13px 20px;
    border: 1px solid var(--field-line);
    border-radius: 13px;
    background: #fff;
    color: var(--muted);
    font: 600 14px "Hanken Grotesk";
    cursor: pointer;
  }

  .delete-confirm {
    flex: 1;
    padding: 13px 20px;
    border: none;
    border-radius: 13px;
    background: var(--orange);
    color: #fff;
    font: 700 14px "Hanken Grotesk";
    cursor: pointer;
    box-shadow: 0 10px 22px -8px rgba(217, 102, 61, 0.7);
  }

  .delete-confirm:disabled {
    cursor: not-allowed;
    opacity: 0.45;
    box-shadow: none;
  }

  @media (max-width: 768px) {
    .agents-head-row {
      align-items: stretch !important;
      flex-direction: column;
      gap: 12px !important;
    }

    .agents-head-row .spacer {
      display: none;
    }

    .agents-head-row .head-cta {
      width: 100%;
    }

    .agent-card {
      padding: 16px;
      gap: 12px;
    }

    .agent-add {
      min-height: 104px;
    }

    .ad-top {
      flex-direction: column;
      gap: 14px;
    }

    .ad-name {
      font-size: 24px;
    }

    .ad-actions {
      flex-direction: column;
      align-items: stretch;
    }

    .ad-actions button {
      justify-content: center;
      width: 100%;
    }

    .ad-diptych {
      grid-template-columns: 1fr;
      margin-top: 20px;
    }

    .ad-spec {
      align-items: flex-start;
      flex-direction: column;
      gap: 4px;
    }

    .ad-spec span:last-child {
      max-width: 100%;
      overflow-wrap: anywhere;
    }

    .ed-row {
      align-items: stretch;
      flex-direction: column;
      gap: 7px;
    }

    .ed-key {
      width: auto;
    }
  }
  .ad-tools-note {
    font-size: 12px;
    line-height: 1.5;
    color: var(--muted-2);
    margin-bottom: 12px;
  }
  .ad-tool {
    position: relative;
    padding: 9px 34px 9px 0;
    border-top: 1px solid var(--line-3);
  }
  .ad-tool-main {
    display: flex;
    align-items: baseline;
    gap: 9px;
    flex-wrap: wrap;
  }
  .ad-tool-name {
    font-size: 13px;
    font-weight: 600;
    color: var(--ink);
  }
  .ad-tool-meta {
    font-size: 11px;
    color: var(--faint);
  }
  .ad-tool-broken {
    font-size: 10.5px;
    font-weight: 600;
    padding: 2px 8px;
    border-radius: 999px;
    background: #fbeae0;
    color: #b14e2a;
    border: 1px solid #f2d6c5;
  }
  .ad-tool-version {
    font-size: 11px;
    color: var(--faint);
    margin-top: 3px;
    overflow-wrap: anywhere;
  }
  .ad-tool-remove {
    position: absolute;
    right: 0;
    top: 10px;
    width: 26px;
    height: 26px;
    display: flex;
    align-items: center;
    justify-content: center;
    border: 1px solid var(--field-line);
    border-radius: 8px;
    background: #fff;
    color: var(--muted-2);
  }
  .ad-tool-remove:hover {
    color: #a23e22;
    border-color: #e7c3b5;
  }
</style>
