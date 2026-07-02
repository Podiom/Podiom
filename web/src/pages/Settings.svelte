<script lang="ts">
  import { onMount, tick } from "svelte";
  import { createProfile, deleteProfile, getConfig, listProfiles, updateConfig, updateProfile } from "../lib/api";
  import {
    capabilityKey,
    effortOptions as capabilityEffortOptions,
    loadProviderCapabilities,
    modelOptions as capabilityModelOptions,
  } from "../lib/capabilities";
  import ProviderLogo from "../lib/ProviderLogo.svelte";
  import type { PushState } from "../lib/live.svelte";
  import type { GlobalConfig, Health, PermissionMode, ProfileInfo, Provider, ProviderCapabilities, UpdateStatus } from "../lib/types";
  import Logs from "./Logs.svelte";

  type UpdateState = "idle" | "checking" | "available" | "current" | "updating" | "restarting" | "failed";
  type SettingsTab = "global" | "updates" | "notifications" | "logs";

  let {
    health,
    update,
    updateState,
    updateError,
    releaseNotesFocusToken,
    settingsFocusTab,
    settingsFocusToken,
    pushState,
    onCheckUpdate,
    onRunUpdate,
    onEnablePush,
  }: {
    health: Health | null;
    update: UpdateStatus | null;
    updateState: UpdateState;
    updateError: string | null;
    releaseNotesFocusToken: number;
    settingsFocusTab: SettingsTab;
    settingsFocusToken: number;
    pushState: PushState;
    onCheckUpdate: () => void;
    onRunUpdate: () => void;
    onEnablePush: () => void;
  } = $props();

  const PERMISSIONS: PermissionMode[] = ["approve", "yolo"];
  const TIMEOUT_PRESETS = ["30s", "1m", "3m", "5m", "10m"];

  let loading = $state(true);
  let error = $state<string | null>(null);
  let saving = $state(false);
  let saved = $state(false);
  let profiles = $state<ProfileInfo[]>([]);
  // Inline "new / edit profile" panel state (chip-driven, per design).
  let profileError = $state<string | null>(null);
  let profileSaving = $state(false);
  let npOpen = $state(false);
  let npEditing = $state<string | null>(null); // profile name being edited, else null
  let npName = $state("");
  let npDir = $state("");

  // Editable default config + fallback.
  let provider = $state<Provider>("claude");
  let profile = $state(""); // "" = default global login
  let model = $state(""); // "" = provider default
  let effort = $state("medium");
  let permission = $state<PermissionMode>("approve");
  let permissionTimeout = $state("3m");
  let fbTarget = $state<"none" | Provider>("none");
  let fbProfile = $state<string | null>(null);

  // Canonical JSON snapshot of the last-saved state, for dirty tracking.
  let baseline = $state("");
  let releaseNotesEl = $state<HTMLElement | null>(null);
  let tab = $state<SettingsTab>("global");
  let capabilitiesByKey = $state<Record<string, ProviderCapabilities>>({});
  let loadingCapabilities = new Set<string>();

  onMount(load);

  $effect(() => {
    void ensureCapabilities(provider, profile);
  });

  async function ensureCapabilities(p: Provider, prof = "") {
    const key = capabilityKey(p, prof);
    if (capabilitiesByKey[key] || loadingCapabilities.has(key)) return;
    loadingCapabilities.add(key);
    try {
      const caps = await loadProviderCapabilities(p, prof);
      capabilitiesByKey = { ...capabilitiesByKey, [key]: caps };
    } catch {
      // Preserve current custom settings if the endpoint cannot be reached.
    } finally {
      loadingCapabilities.delete(key);
    }
  }

  $effect(() => {
    if (settingsFocusToken > 0) {
      tab = settingsFocusTab;
    }
  });

  $effect(() => {
    if (releaseNotesFocusToken > 0) {
      tab = "updates";
      void focusReleaseNotes();
    }
  });

  async function load() {
    loading = true;
    error = null;
    try {
      const [cfg, profs] = await Promise.all([getConfig(), listProfiles()]);
      profiles = profs;
      applyConfig(cfg);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  function pathForProfile(p: ProfileInfo): string {
    return p.Provider === "codex" ? (p.HomeDir ?? "") : (p.ConfigDir ?? "");
  }

  // Profiles selectable for the current default provider (chips).
  const profileChips = $derived(profiles.filter((p) => p.Provider === provider).map((p) => p.Name));
  const npDirPh = $derived(
    provider === "codex" ? "CODEX_HOME — optional" : "CLAUDE_CONFIG_DIR — optional",
  );

  function setProfile(name: string) {
    profile = name;
    saved = false;
    void ensureCapabilities(provider, profile);
  }

  function toggleNewProfile() {
    if (npOpen) {
      npOpen = false;
      npEditing = null;
    } else {
      npOpen = true;
      npEditing = null;
      npName = "";
      npDir = "";
      profileError = null;
    }
  }

  function startEditProfile(name: string) {
    const p = profiles.find((x) => x.Name === name);
    if (!p) return;
    npOpen = true;
    npEditing = name;
    npName = p.Name;
    npDir = pathForProfile(p);
    profileError = null;
  }

  async function saveInlineProfile() {
    const name = npName.trim();
    if (!name) return;
    profileSaving = true;
    profileError = null;
    try {
      const body = {
        name,
        provider,
        config_dir: provider === "claude" ? npDir.trim() : "",
        home_dir: provider === "codex" ? npDir.trim() : "",
      };
      if (npEditing) {
        await updateProfile(npEditing, body);
      } else {
        await createProfile(body);
      }
      profiles = await listProfiles();
      if (!npEditing) setProfile(name); // "Create & select"
      void ensureCapabilities(provider, name);
      npOpen = false;
      npEditing = null;
      npName = "";
      npDir = "";
    } catch (e) {
      profileError = e instanceof Error ? e.message : String(e);
    } finally {
      profileSaving = false;
    }
  }

  async function removeProfile(name: string) {
    if (!window.confirm(`Delete profile ${name}?`)) return;
    profileError = null;
    try {
      await deleteProfile(name);
      profiles = await listProfiles();
      if (profile === name) setProfile("");
      if (npEditing === name) {
        npOpen = false;
        npEditing = null;
      }
    } catch (e) {
      profileError = e instanceof Error ? e.message : String(e);
    }
  }

  function applyConfig(cfg: GlobalConfig) {
    provider = cfg.provider;
    profile = cfg.profile ?? "";
    model = cfg.model;
    effort = cfg.effort || "medium";
    permission = cfg.permission_mode;
    permissionTimeout = cfg.permission_timeout || "3m";
    const fb = cfg.fallback ?? [];
    if (fb.length === 0) {
      fbTarget = "none";
      fbProfile = null;
    } else {
      const first = fb[0];
      if (first === "claude" || first === "codex") {
        fbTarget = first;
        fbProfile = null;
      } else if (first === "default") {
        fbTarget = cfg.provider;
        fbProfile = null;
      } else {
        const prof = profiles.find((p) => p.Name === first);
        fbTarget = prof ? prof.Provider : "claude";
        fbProfile = first;
      }
    }
    baseline = canonical();
    saved = false;
  }

  const settingsCapabilityKey = $derived(capabilityKey(provider, profile));
  const settingsCapabilities = $derived(capabilitiesByKey[settingsCapabilityKey] ?? null);
  const modelChips = $derived(capabilityModelOptions(settingsCapabilities, model));
  const effortChips = $derived(capabilityEffortOptions(settingsCapabilities, model, effort));
  const fbProfileOptions = $derived(
    fbTarget === "none" ? [] : profiles.filter((p) => p.Provider === fbTarget).map((p) => p.Name),
  );
  const fbHasProfiles = $derived(fbProfileOptions.length > 0);

  function buildFallback(): string[] {
    if (fbTarget === "none") return [];
    if (fbProfile && fbProfileOptions.includes(fbProfile)) return [fbProfile];
    return [fbTarget];
  }

  function canonical(): string {
    return JSON.stringify({ provider, profile, model, effort, permission, permission_timeout: permissionTimeout, fallback: buildFallback() });
  }

  const dirty = $derived(canonical() !== baseline);
  const timeoutInvalid = $derived(!validDuration(permissionTimeout));

  function setProvider(p: Provider) {
    if (p === provider) return;
    provider = p;
    saved = false;
    // The default profile is tied to the provider; drop it if it no longer fits.
    if (profile && !profiles.some((x) => x.Name === profile && x.Provider === p)) profile = "";
    void ensureCapabilities(p, profile);
    npOpen = false;
    npEditing = null;
  }
  function setModel(m: string) {
    model = m;
    saved = false;
  }
  function setEffort(e: string) {
    effort = e;
    saved = false;
  }
  function setPermission(m: PermissionMode) {
    permission = m;
    saved = false;
  }
  function setPermissionTimeout(value: string) {
    permissionTimeout = value;
    saved = false;
  }
  function setFbTarget(t: "none" | Provider) {
    fbTarget = t;
    saved = false;
    if (t === "none") {
      fbProfile = null;
    } else {
      const opts = profiles.filter((p) => p.Provider === t).map((p) => p.Name);
      if (fbProfile && !opts.includes(fbProfile)) fbProfile = null;
    }
  }
  function setFbProfile(name: string) {
    fbProfile = name;
    saved = false;
  }

  async function save() {
    if (timeoutInvalid) {
      error = "approval wait must be a positive duration like 30s, 3m, or 1h";
      saved = false;
      return;
    }
    const timeout = permissionTimeout.trim();
    saving = true;
    error = null;
    try {
      const cfg = await updateConfig({
        provider,
        profile,
        model,
        effort,
        permission_mode: permission,
        permission_timeout: timeout,
        fallback: buildFallback(),
      });
      applyConfig(cfg);
      saved = true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      saved = false;
    } finally {
      saving = false;
    }
  }

  function validDuration(raw: string): boolean {
    const value = raw.trim();
    if (!value) return false;
    const parts = value.match(/\d+(?:\.\d+)?(?:ns|us|ms|s|m|h)/g);
    if (!parts || parts.join("") !== value) return false;
    return parts.some((part) => Number.parseFloat(part) > 0);
  }

  async function focusReleaseNotes() {
    await tick();
    releaseNotesEl?.scrollIntoView({ behavior: "smooth", block: "start" });
    releaseNotesEl?.focus({ preventScroll: true });
  }

  // ---- Version & updates: presentation of App.svelte's update state ----
  const primaryLabel = $derived(model ? `${provider} · ${model}` : `${provider} · provider default`);
  const fbRouteLabel = $derived(
    fbTarget === "none"
      ? "pause · retry on reset"
      : fbProfile && fbProfileOptions.includes(fbProfile)
        ? `${fbTarget} · ${fbProfile}`
        : fbTarget,
  );
  const canCheck = $derived(updateState === "idle" || updateState === "current" || updateState === "failed");
  const updateBadge = $derived(updateBadgeFor(updateState));
  const releaseNotes = $derived(update?.release_notes ?? "");
  const pushBadge = $derived(pushBadgeFor(pushState));
  const pushStatusCopy = $derived(pushCopyFor(pushState));
  function updateBadgeFor(state: UpdateState): { label: string; tone: "neutral" | "green" | "amber" } {
    switch (state) {
      case "checking":
        return { label: "checking…", tone: "neutral" };
      case "current":
        return { label: "up to date", tone: "green" };
      case "available":
        return { label: `${update?.latest_version ?? ""} available`, tone: "amber" };
      case "updating":
      case "restarting":
        return { label: "updating…", tone: "amber" };
      case "failed":
        return { label: "check failed", tone: "amber" };
      default:
        return { label: "not checked", tone: "neutral" };
    }
  }
  function pushBadgeFor(state: PushState): { label: string; tone: "neutral" | "green" | "amber" } {
    switch (state) {
      case "enabled":
        return { label: "on", tone: "green" };
      case "enabling":
        return { label: "enabling…", tone: "amber" };
      case "denied":
        return { label: "blocked", tone: "amber" };
      case "unsupported":
        return { label: "unavailable", tone: "neutral" };
      default:
        return { label: "not enabled", tone: "neutral" };
    }
  }
  function pushCopyFor(state: PushState): { title: string; body: string } {
    switch (state) {
      case "enabled":
        return { title: "Notifications are on", body: "Podiom will keep this browser registered while notification permission remains approved." };
      case "enabling":
        return { title: "Enabling notifications", body: "Waiting for the browser and daemon to finish registration." };
      case "denied":
        return { title: "Notifications are blocked", body: "Your browser has blocked notification permission for Podiom. Change the site permission in the browser to use them." };
      case "unsupported":
        return { title: "Notifications are unavailable", body: "This browser or daemon is missing Web Push support." };
      default:
        return { title: "Notifications are not enabled", body: "Enable notifications to get alerted when an agent needs your approval or answer." };
    }
  }
</script>

<div class="page settings">
  <div class="settings-inner">
    <header class="settings-head">
      <h1>Settings</h1>
      <p>
        The engine every new agent, task and session inherits unless overridden — and where Podiom turns when a provider
        can't take the work.
      </p>
    </header>

    {#if error}
      <div class="error-banner" style="margin-bottom:16px">{error}</div>
    {/if}

    <div class="tabs">
      <button class:active={tab === "global"} onclick={() => (tab = "global")}>Global config</button>
      <button class:active={tab === "updates"} onclick={() => (tab = "updates")}>Version &amp; Updates</button>
      <button class:active={tab === "notifications"} onclick={() => (tab = "notifications")}>Notifications</button>
      <button class:active={tab === "logs"} onclick={() => (tab = "logs")}>Logs</button>
    </div>

    {#if tab === "global"}
    <!-- ===== DEFAULT CONFIGURATION ===== -->
    <section class="card">
      <div class="card-head">
        <div class="card-icon teal">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 6h10"/><path d="M20 6h-2"/><circle cx="16" cy="6" r="2"/><path d="M4 12h2"/><path d="M12 12h8"/><circle cx="9" cy="12" r="2"/><path d="M4 18h10"/><path d="M20 18h-2"/><circle cx="16" cy="18" r="2"/></svg>
        </div>
        <div>
          <div class="card-title">Default configuration</div>
          <div class="card-sub">Applied to every new colleague and ad-hoc run.</div>
        </div>
      </div>

      {#if loading}
        <div class="empty-note">Loading defaults…</div>
      {:else}
        <div class="rows">
          <div class="row">
            <span class="row-key">provider</span>
            <div class="seg-group">
              <button class="seg provider-choice" class:on={provider === "claude"} onclick={() => setProvider("claude")}>
                <ProviderLogo provider="claude" />Claude
              </button>
              <button class="seg provider-choice" class:on={provider === "codex"} onclick={() => setProvider("codex")}>
                <ProviderLogo provider="codex" />Codex
              </button>
            </div>
          </div>
          <div class="row top">
            <span class="row-key">profile</span>
            <div class="chips-col">
              <div class="chips">
                <button class="chip" class:on={profile === ""} onclick={() => setProfile("")}>default · global login</button>
                {#each profileChips as name}
                  <button class="chip prof-chip" class:on={profile === name} onclick={() => setProfile(name)}>
                    <span class="prof-label">{name}</span>
                    <span class="prof-tools">
                      <span
                        class="prof-edit"
                        role="button"
                        tabindex="0"
                        title="Edit profile"
                        onclick={(e) => { e.stopPropagation(); startEditProfile(name); }}
                        onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); e.stopPropagation(); startEditProfile(name); } }}>
                        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/></svg>
                      </span>
                      <span
                        class="prof-del"
                        role="button"
                        tabindex="0"
                        title="Delete profile"
                        onclick={(e) => { e.stopPropagation(); removeProfile(name); }}
                        onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); e.stopPropagation(); removeProfile(name); } }}>×</span>
                    </span>
                  </button>
                {/each}
                <button class="chip-new" onclick={toggleNewProfile}>
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M12 5v14"/><path d="M5 12h14"/></svg>New
                </button>
              </div>
              <div class="hint">
                Connect multiple Claude or Codex accounts. Each profile has its own login and rate limit, so Podiom can
                switch accounts when one runs out. No profile means your normal login is used.
              </div>
              {#if npOpen}
                <div class="np-panel">
                  <div class="np-title">{npEditing ? `edit profile · ${provider}` : "new profile · uses selected provider"}</div>
                  <input class="np-input" bind:value={npName} disabled={Boolean(npEditing)} placeholder="profile name" />
                  <input class="np-input mono" bind:value={npDir} placeholder={npDirPh} />
                  <div class="np-actions">
                    <button class="np-create" disabled={profileSaving || !npName.trim()} onclick={saveInlineProfile}>
                      {profileSaving ? "Saving…" : npEditing ? "Save changes" : "Create & select"}
                    </button>
                    <button class="np-cancel" onclick={toggleNewProfile}>Cancel</button>
                  </div>
                </div>
              {/if}
              {#if profileError}
                <div class="error-banner" style="margin-top:10px">{profileError}</div>
              {/if}
            </div>
          </div>
          <div class="row top">
            <span class="row-key">model</span>
            <div class="chips">
              <button class="chip" class:on={model === ""} onclick={() => setModel("")}>✦ default</button>
              {#each modelChips as m}
                <button class="chip" class:on={model === m} onclick={() => setModel(m)}>{m}</button>
              {/each}
              <input class="field-input mono" style="max-width:220px;padding:8px 10px;font-size:12px" bind:value={model} placeholder="custom model" oninput={() => (saved = false)} />
            </div>
          </div>
          <div class="row top">
            <span class="row-key">effort</span>
            <div class="chips">
              {#each effortChips as e}
                <button class="chip" class:on={effort === e} onclick={() => setEffort(e)}>{e}</button>
              {/each}
            </div>
          </div>
          <div class="row top">
            <span class="row-key">permission</span>
            <div class="chips">
              {#each PERMISSIONS as m}
                <button class="chip" class:on={permission === m} onclick={() => setPermission(m)}>{m}</button>
              {/each}
            </div>
          </div>
          <div class="row top">
            <span class="row-key">approval wait</span>
            <div class="chips-col">
              <div class="timeout-control">
                <div class="chips">
                  {#each TIMEOUT_PRESETS as value}
                    <button class="chip" class:on={permissionTimeout === value} onclick={() => setPermissionTimeout(value)}>{value}</button>
                  {/each}
                </div>
                <input
                  class="timeout-input mono"
                  class:invalid={timeoutInvalid}
                  bind:value={permissionTimeout}
                  placeholder="custom"
                  aria-label="Approval prompt wait time" />
              </div>
              <div class="hint">How long approval prompts wait before auto-denying. Use values like 45s, 3m, or 1h.</div>
              {#if timeoutInvalid}
                <div class="field-error">Enter a positive duration like 30s, 3m, or 1h.</div>
              {/if}
            </div>
          </div>
        </div>
      {/if}
    </section>

    <!-- ===== FALLBACK PROVIDER ===== -->
    <section class="card">
      <div class="card-head">
        <div class="card-icon gold">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 1 0 9-9 9 9 0 0 0-6.36 2.64L3 8"/><path d="M3 4v4h4"/></svg>
        </div>
        <div>
          <div class="card-title">Fallback provider</div>
          <div class="card-sub">Where runs re-route when your provider is rate-limited or unreachable.</div>
        </div>
      </div>

      <div class="route">
        <div class="route-end">
          <div class="route-tag">primary</div>
          <div class="route-val">{primaryLabel}</div>
        </div>
        <div class="route-arrow">
          <span class="route-cond">429 · limited</span>
          <svg width="36" height="14" viewBox="0 0 36 14" fill="none" stroke="#c9a24e" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M2 7h30"/><path d="M27 2l6 5-6 5"/></svg>
        </div>
        <div class="route-end right">
          <div class="route-tag">fallback</div>
          <div class="route-val" class:muted={fbTarget === "none"}>{fbRouteLabel}</div>
        </div>
      </div>

      <div class="rows">
        <div class="row">
          <span class="row-key">route to</span>
          <div class="seg-group">
            <button class="seg" class:on={fbTarget === "none"} onclick={() => setFbTarget("none")}>None</button>
            <button class="seg provider-choice" class:on={fbTarget === "claude"} onclick={() => setFbTarget("claude")}>
              <ProviderLogo provider="claude" />Claude
            </button>
            <button class="seg provider-choice" class:on={fbTarget === "codex"} onclick={() => setFbTarget("codex")}>
              <ProviderLogo provider="codex" />Codex
            </button>
          </div>
        </div>
        {#if fbTarget !== "none" && fbHasProfiles}
          <div class="row top">
            <span class="row-key">profile</span>
            <div class="chips-col">
              <div class="chips">
                {#each fbProfileOptions as name}
                  <button class="chip" class:on={fbProfile === name} onclick={() => setFbProfile(name)}>{name}</button>
                {/each}
              </div>
              <div class="hint">Which account on the fallback provider to run under.</div>
            </div>
          </div>
        {/if}
        {#if fbTarget === "none"}
          <div class="fb-disabled"><span class="dot-amber">●</span>No fallback — rate-limited runs pause and retry automatically when the limit resets. Nothing is dropped.</div>
        {/if}
      </div>
    </section>

    <!-- ===== SAVE ===== -->
    <div class="save-row">
      <button class="btn-save" disabled={saving || loading || timeoutInvalid} onclick={save}>{saving ? "Saving…" : "Save defaults"}</button>
      {#if saved && !dirty}
        <span class="save-ok"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>Saved to config.yaml</span>
      {:else}
        <span class="save-hint">Changes apply to new runs immediately.</span>
      {/if}
    </div>

    {:else if tab === "updates"}
    <!-- ===== VERSION & UPDATES ===== -->
    <section class="card">
      <div class="card-head">
        <div class="card-icon teal">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-3-6.7"/><path d="M21 3v5h-5"/></svg>
        </div>
        <div class="grow">
          <div class="card-title">Version &amp; updates</div>
          <div class="card-sub">Releases from github.com/podiom/podiom.</div>
        </div>
        <span class="badge {updateBadge.tone}">{updateBadge.label}</span>
      </div>

      <div class="ver-row">
        <div class="grow">
          <div class="route-tag">installed</div>
          <div class="ver-num">{health ? health.version : "—"}</div>
        </div>
        {#if updateState === "checking"}
          <span class="spinner-note"><span class="spinner"></span>Contacting GitHub…</span>
        {:else if updateState === "updating" || updateState === "restarting"}
          <span class="spinner-note amber"><span class="spinner amber"></span>Installing {update?.latest_version ?? ""}…</span>
        {:else if canCheck}
          <button class="btn-ghost" onclick={onCheckUpdate}>Check for updates</button>
        {/if}
      </div>

      {#if updateState === "current"}
        <div class="ver-note"><span class="ok">✓</span>You're running the latest release.</div>
      {/if}
      {#if updateState === "failed" && updateError}
        <div class="ver-note"><span class="dot-amber">●</span>{updateError}</div>
      {/if}
      {#if updateState === "available" && update}
        <div class="new-version">
          <div>
            <div class="route-tag">new version</div>
            <div class="ver-num">{update.latest_version}</div>
          </div>
        </div>
        <div
          class="release-notes"
          bind:this={releaseNotesEl}
          tabindex="-1"
          aria-label={`Release notes for ${update.latest_version}`}>
          <div class="release-notes-title">Release notes</div>
          {#if releaseNotes.trim()}
            <pre>{releaseNotes}</pre>
          {:else}
            <div class="empty-note">No release notes were published for this release.</div>
          {/if}
        </div>
        <div class="upd-actions">
          <button class="btn-save" onclick={onRunUpdate}>Update to {update.latest_version}</button>
          {#if update.blocking_reason}
            <span class="save-hint">{update.blocking_reason}</span>
          {/if}
          {#if update.release_url}
            <a class="rel-link" href={update.release_url} target="_blank" rel="noreferrer">Release notes ↗</a>
          {/if}
        </div>
      {/if}
    </section>

    {:else if tab === "notifications"}
    <!-- ===== NOTIFICATIONS ===== -->
    <section class="card">
      <div class="card-head">
        <div class="card-icon teal">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg>
        </div>
        <div class="grow">
          <div class="card-title">Notifications</div>
          <div class="card-sub">Alerts when an agent needs your approval or answer.</div>
        </div>
        <span class="badge {pushBadge.tone}">{pushBadge.label}</span>
      </div>

      <div class="notification-panel">
        <div class="grow">
          <div class="route-tag">status</div>
          <div class="notification-title">{pushStatusCopy.title}</div>
          <div class="notification-copy">{pushStatusCopy.body}</div>
        </div>
        {#if pushState === "idle"}
          <button class="btn-save" onclick={onEnablePush}>Enable notifications</button>
        {:else if pushState === "enabling"}
          <span class="spinner-note amber"><span class="spinner amber"></span>Registering…</span>
        {/if}
      </div>
    </section>

    {:else}
    <!-- ===== LOGS ===== -->
    <section class="card">
      <div class="card-head">
        <div class="card-icon violet">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 17l6-6-6-6"/><path d="M12 19h8"/></svg>
        </div>
        <div>
          <div class="card-title">Logs</div>
          <div class="card-sub">Live tail of the daemon log.</div>
        </div>
      </div>
      <div class="logs-wrap">
        <Logs embedded />
      </div>
    </section>
    {/if}
  </div>
</div>

<style>
  .settings-inner {
    max-width: 740px;
    margin: 0 auto;
  }

  .settings-head {
    margin-bottom: 24px;
  }

  .settings-head h1 {
    margin: 0;
    font: 800 24px "Hanken Grotesk";
    letter-spacing: -0.02em;
  }

  .settings-head p {
    margin: 6px 0 0;
    font: 400 13.5px/1.5 "Hanken Grotesk";
    color: var(--muted-2);
    max-width: 560px;
  }

  .tabs {
    display: inline-flex;
    gap: 4px;
    padding: 4px;
    margin: 0 0 18px;
    border-radius: 13px;
    background: #efe7dc;
    border: 1px solid #e6dbcc;
    flex-wrap: wrap;
  }

  .tabs button {
    border: 0;
    background: transparent;
    color: #8a7f73;
    cursor: pointer;
    border-radius: 9px;
    padding: 7px 12px;
    font: 700 12.5px "Hanken Grotesk";
  }

  .tabs button.active {
    background: #fffdfb;
    color: #2b2520;
    box-shadow: 0 1px 3px rgba(43, 37, 32, 0.12);
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 20px;
    padding: 24px 26px;
    margin-bottom: 18px;
  }

  .card-head {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .card-icon {
    width: 38px;
    height: 38px;
    border-radius: 12px;
    display: flex;
    align-items: center;
    justify-content: center;
    flex: none;
  }

  .card-icon.teal {
    background: #eaf2ee;
    color: #2f6e60;
  }

  .card-icon.gold {
    background: #fbf1dd;
    color: #9a6e1e;
  }

  .card-icon.violet {
    background: #f0edf9;
    color: #5847b8;
  }

  .card-title {
    font: 800 17px "Hanken Grotesk";
    letter-spacing: -0.01em;
  }

  .card-sub {
    font: 400 12.5px "Hanken Grotesk";
    color: var(--muted-2);
  }

  .grow {
    flex: 1;
    min-width: 0;
  }

  .rows {
    display: flex;
    flex-direction: column;
    gap: 15px;
    margin-top: 22px;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 14px;
  }

  .row.top {
    align-items: flex-start;
  }

  .row-key {
    font: 600 10.5px "JetBrains Mono", monospace;
    letter-spacing: 0.07em;
    color: var(--faint);
    text-transform: uppercase;
    width: 84px;
    flex: none;
    padding-top: 7px;
  }

  .row:not(.top) .row-key {
    padding-top: 0;
  }

  .seg-group {
    flex: 1;
    display: flex;
    gap: 9px;
  }

  .seg {
    flex: 1;
    padding: 11px;
    border-radius: 11px;
    font: 600 13.5px "Hanken Grotesk";
    border: 1px solid var(--field-line);
    background: #fff;
    color: var(--muted);
  }

  .seg.on {
    border-color: #bfe0d6;
    background: #e3f1ec;
    color: var(--teal-deep);
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
  }

  .chips-col {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .chip {
    padding: 6px 12px;
    border-radius: 9px;
    font: 600 12px "JetBrains Mono", monospace;
    border: 1px solid var(--field-line);
    background: #fff;
    color: var(--muted);
  }

  .chip.on {
    border-color: #bfe0d6;
    background: #e3f1ec;
    color: var(--teal-deep);
  }

  .hint {
    font: 400 11.5px/1.45 "Hanken Grotesk";
    color: var(--muted-2);
  }

  .timeout-control {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }

  .timeout-input {
    width: 104px;
    border: 1px solid var(--field-line);
    border-radius: 11px;
    padding: 8px 10px;
    background: #fff;
    color: var(--ink);
    outline: none;
  }

  .timeout-input:focus {
    border-color: #9ecdc0;
    box-shadow: 0 0 0 3px rgba(63, 143, 126, 0.12);
  }

  .timeout-input.invalid {
    border-color: #d99a78;
    box-shadow: 0 0 0 3px rgba(217, 154, 120, 0.13);
  }

  .field-error {
    font: 600 12px "Hanken Grotesk";
    color: #9a4e2f;
  }

  /* profile chips: select the default profile + manage inline */
  .prof-chip {
    display: inline-flex;
    align-items: center;
    gap: 0;
  }

  .prof-tools {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    margin-left: 7px;
    padding-left: 8px;
    border-left: 1px solid rgba(90, 80, 72, 0.16);
  }

  .prof-edit {
    display: inline-flex;
    align-items: center;
    color: #a0937f;
    cursor: pointer;
  }

  .prof-edit:hover {
    color: var(--teal-deep);
  }

  .prof-del {
    color: #b79e86;
    font-weight: 700;
    line-height: 1;
    cursor: pointer;
  }

  .prof-del:hover {
    color: #c2705e;
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

  .np-panel {
    margin-top: 11px;
    background: var(--surface-3);
    border: 1px solid var(--line-3);
    border-radius: 14px;
    padding: 14px;
    max-width: 380px;
  }

  .np-title {
    font: 600 10px "JetBrains Mono", monospace;
    letter-spacing: 0.1em;
    color: var(--faint);
    text-transform: uppercase;
    margin-bottom: 10px;
  }

  .np-input {
    width: 100%;
    border: 1px solid var(--field-line);
    border-radius: 11px;
    padding: 10px 13px;
    font: 600 14px "Hanken Grotesk";
    color: var(--ink);
    outline: none;
    background: #fff;
  }

  .np-input.mono {
    font: 500 12px "JetBrains Mono", monospace;
    color: var(--muted);
    margin-top: 9px;
  }

  .np-input:disabled {
    background: #f4efe8;
    color: var(--muted-2);
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

  /* fallback route summary */
  .route {
    display: flex;
    align-items: center;
    gap: 12px;
    background: var(--surface-3);
    border: 1px solid var(--line-3);
    border-radius: 14px;
    padding: 15px 18px;
    margin-top: 20px;
  }

  .route-end {
    flex: 1;
    min-width: 0;
  }

  .route-end.right {
    text-align: right;
  }

  .route-tag {
    font: 600 9.5px "JetBrains Mono", monospace;
    letter-spacing: 0.12em;
    color: var(--faint);
    text-transform: uppercase;
  }

  .route-val {
    font: 700 14.5px "Hanken Grotesk";
    color: var(--ink);
    margin-top: 3px;
  }

  .route-val.muted {
    color: var(--muted-2);
  }

  .route-arrow {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    flex: none;
    padding: 0 6px;
  }

  .route-cond {
    font: 600 9.5px "JetBrains Mono", monospace;
    color: #b07a22;
    letter-spacing: 0.03em;
  }

  .fb-disabled {
    font: 400 12.5px/1.5 "Hanken Grotesk";
    color: var(--muted-2);
    display: flex;
    align-items: flex-start;
    gap: 8px;
  }

  .dot-amber {
    color: #c9a24e;
    flex: none;
  }

  /* save row */
  .save-row {
    display: flex;
    align-items: center;
    gap: 15px;
    margin: 4px 0 18px;
  }

  .btn-save {
    border: none;
    border-radius: 13px;
    padding: 13px 26px;
    background: var(--teal);
    color: #fff;
    font: 700 14.5px "Hanken Grotesk";
    box-shadow: 0 10px 22px -8px rgba(63, 143, 126, 0.7);
  }

  .save-ok {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font: 600 13px "Hanken Grotesk";
    color: #3f7a5f;
  }

  .save-hint {
    font: 400 12.5px "Hanken Grotesk";
    color: var(--faint);
  }

  /* version & updates */
  .badge {
    padding: 5px 11px;
    border-radius: 999px;
    font: 600 10.5px "JetBrains Mono", monospace;
    white-space: nowrap;
    border: 1px solid var(--line-2);
    flex: none;
  }

  .badge.neutral {
    background: #f1eadf;
    color: var(--muted-2);
    border-color: #e6dbcb;
  }

  .badge.green {
    background: #eaf1ed;
    color: #3f7a5f;
    border-color: #cfe3d8;
  }

  .badge.amber {
    background: #fbf1dd;
    color: #9a6e1e;
    border-color: #ecd9ae;
  }

  .ver-row {
    display: flex;
    align-items: center;
    gap: 14px;
    background: var(--surface-3);
    border: 1px solid var(--line-3);
    border-radius: 14px;
    padding: 15px 18px;
    margin-top: 20px;
  }

  .ver-num {
    font: 800 18px "Hanken Grotesk";
    color: var(--ink);
    margin-top: 2px;
  }

  .new-version {
    display: flex;
    align-items: center;
    gap: 14px;
    background: #fbf1dd;
    border: 1px solid #ecd9ae;
    border-radius: 14px;
    padding: 15px 18px;
    margin-top: 14px;
  }

  .release-notes {
    margin-top: 12px;
    border: 1px solid var(--line-3);
    border-radius: 14px;
    background: #fff;
    padding: 15px 18px;
  }

  .release-notes:focus {
    outline: 2px solid #bfe0d6;
    outline-offset: 3px;
  }

  .release-notes-title {
    font: 700 13.5px "Hanken Grotesk";
    color: var(--ink);
    margin-bottom: 9px;
  }

  .release-notes pre {
    margin: 0;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font: 500 12px/1.55 "JetBrains Mono", monospace;
    color: var(--muted);
  }

  .btn-ghost {
    border: 1px solid #cfe3d8;
    border-radius: 12px;
    padding: 10px 18px;
    background: #fff;
    color: #2f6e60;
    font: 700 13px "Hanken Grotesk";
    flex: none;
  }

  .spinner-note {
    display: inline-flex;
    align-items: center;
    gap: 9px;
    font: 600 13px "Hanken Grotesk";
    color: var(--muted-2);
    flex: none;
  }

  .spinner-note.amber {
    color: #9a6e1e;
  }

  .spinner {
    width: 15px;
    height: 15px;
    border: 2px solid #d8cdbd;
    border-top-color: #2f6e60;
    border-radius: 99px;
    display: inline-block;
    animation: pdSpin 0.7s linear infinite;
  }

  .spinner.amber {
    border-color: #ecd9ae;
    border-top-color: #9a6e1e;
  }

  @keyframes pdSpin {
    to {
      transform: rotate(360deg);
    }
  }

  .ver-note {
    font: 400 12.5px/1.5 "Hanken Grotesk";
    color: var(--muted-2);
    margin-top: 14px;
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .ver-note .ok {
    color: #3f7a5f;
  }

  .upd-actions {
    display: flex;
    align-items: center;
    gap: 14px;
    flex-wrap: wrap;
    margin-top: 16px;
  }

  .rel-link {
    font: 600 12.5px "Hanken Grotesk";
    color: #2f6e60;
    text-decoration: none;
  }

  .rel-link:hover {
    text-decoration: underline;
  }

  .notification-panel {
    display: flex;
    align-items: center;
    gap: 18px;
    background: var(--surface-3);
    border: 1px solid var(--line-3);
    border-radius: 14px;
    padding: 16px 18px;
    margin-top: 20px;
  }

  .notification-title {
    font: 800 17px "Hanken Grotesk";
    color: var(--ink);
    margin-top: 4px;
  }

  .notification-copy {
    font: 400 12.5px/1.5 "Hanken Grotesk";
    color: var(--muted-2);
    margin-top: 4px;
    max-width: 470px;
  }

  .logs-wrap {
    margin-top: 18px;
  }

  @media (max-width: 768px) {
    .row,
    .row.top {
      flex-direction: column;
      align-items: stretch;
      gap: 8px;
    }

    .row-key {
      width: auto;
      padding-top: 0;
    }

    .tabs {
      display: flex;
    }

    .notification-panel {
      flex-direction: column;
      align-items: stretch;
    }
  }
</style>
