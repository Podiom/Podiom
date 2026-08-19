<script lang="ts">
  import { onMount, tick } from "svelte";
  import {
    createProfile,
    deleteProfile,
    getConfig,
    deleteNotificationDevice,
    getNotificationPreferences,
    listNotificationDevices,
    gitStatus,
    listProfiles,
    setGitIdentity,
    sendTestNotificationPush,
    setNotificationDeviceEnabled,
    setNotificationPreferences,
    updateConfig,
    updateProfile,
  } from "../lib/api";
  import ProviderSignIn from "../lib/ProviderSignIn.svelte";
  import ProviderLogo from "../lib/ProviderLogo.svelte";
  import * as connection from "../lib/connection";
  import { isNative } from "../lib/native";
  import { currentDeviceID, unregisterFromDaemon } from "../lib/push";
  import { DEFAULT_PROVIDER, PROVIDERS, isProvider, providerMeta } from "../lib/providers";
  import type { PushState } from "../lib/live.svelte";
  import type {
    Agent,
    GitStatus,
    GlobalConfig,
    Health,
    NotificationDevice,
    NotificationPreferenceGroup,
    NotificationTestResult,
    ProfileInfo,
    Provider,
    ProviderAuthStatus,
    UpdateStatus,
  } from "../lib/types";
  import AboutYou from "./AboutYou.svelte";
  import Credentials from "./Credentials.svelte";
  import Agents from "./Agents.svelte";
  import Logs from "./Logs.svelte";

  type UpdateState = "idle" | "checking" | "available" | "current" | "updating" | "restarting" | "failed";
  type SettingsTab = "providers" | "general" | "agents" | "about-you" | "credentials" | "updates" | "notifications" | "logs";

  // One row in a provider card's account list. The unnamed account (name "")
  // is the provider CLI's own login directory — Podiom did not create it, so it
  // can be signed into but not renamed or deleted.
  interface AccountRow {
    name: string;
    label: string;
    sub: string;
    canEdit: boolean;
  }

  interface ChatTarget {
    sessionId?: string;
    agentName?: string;
    seed?: string;
  }

  interface SettingsAccountTarget {
    provider: Provider;
    profile: string;
  }

  let {
    health,
    update,
    updateState,
    updateError,
    releaseNotesFocusToken,
    settingsFocusTab,
    settingsFocusToken,
    settingsFocusAccount,
    authStatuses,
    authLoading,
    authError,
    pushState,
    agents,
    onCheckUpdate,
    onRunUpdate,
    onEnablePush,
    onHireAgent,
    onOpenChat,
    onAgentsChanged,
    onRefreshAuth,
    onUserProfileSaved = () => {},
  }: {
    health: Health | null;
    update: UpdateStatus | null;
    updateState: UpdateState;
    updateError: string | null;
    releaseNotesFocusToken: number;
    settingsFocusTab: SettingsTab;
    settingsFocusToken: number;
    settingsFocusAccount: SettingsAccountTarget | null;
    authStatuses: ProviderAuthStatus[];
    authLoading: boolean;
    authError: string | null;
    pushState: PushState;
    agents: Agent[];
    onCheckUpdate: () => void;
    onRunUpdate: () => void;
    onEnablePush: () => void;
    onHireAgent: () => void;
    onOpenChat: (t: ChatTarget) => void;
    onAgentsChanged: () => void;
    onRefreshAuth: (refresh?: boolean) => Promise<void>;
    onUserProfileSaved?: () => void;
  } = $props();

  // ── Git ────────────────────────────────────────────────────────────────
  // Source control needs three things to be usable: the binary, a commit
  // identity, and credentials that can reach a remote. The card reports each
  // separately so a half-configured host says which half is missing.
  let git = $state<GitStatus | null>(null);
  let gitLoading = $state(true);
  let gitName = $state("");
  let gitEmail = $state("");
  let gitSaving = $state(false);
  let gitError = $state("");

  const gitIdentitySet = $derived(!!git?.user_name && !!git?.user_email);
  const gitBadge = $derived(
    !git || !git.found
      ? { label: "not installed", tone: "amber" }
      : git.ready
        ? { label: "ready", tone: "green" }
        : { label: "needs setup", tone: "amber" },
  );

  async function refreshGit() {
    gitLoading = true;
    try {
      git = await gitStatus();
      gitName = git.user_name ?? "";
      gitEmail = git.user_email ?? "";
    } catch (e) {
      gitError = e instanceof Error ? e.message : String(e);
    } finally {
      gitLoading = false;
    }
  }

  async function saveGitIdentity() {
    gitSaving = true;
    gitError = "";
    try {
      git = await setGitIdentity(gitName.trim(), gitEmail.trim());
    } catch (e) {
      gitError = e instanceof Error ? e.message : String(e);
    } finally {
      gitSaving = false;
    }
  }

  async function copySSHKey() {
    if (!git?.ssh_key) return;
    try {
      await navigator.clipboard.writeText(git.ssh_key);
    } catch {
      // Clipboard access can be denied; the key is on screen to copy by hand.
    }
  }

  let loading = $state(true);
  let error = $state<string | null>(null);
  let saving = $state(false);
  let saved = $state(false);
  let profiles = $state<ProfileInfo[]>([]);
  let profileError = $state<string | null>(null);
  // Which card shows profileError. A failed delete has no panel open, so the
  // message needs its own provider rather than borrowing the panel's.
  let profileErrorProvider = $state<Provider | null>(null);
  let profileSaving = $state(false);
  // Inline "add / edit account" panel state. npProvider names the card the
  // panel belongs to, so a create writes to that provider rather than to
  // whichever one happens to be the default.
  let npOpen = $state(false);
  let npProvider = $state<Provider | null>(null);
  let npEditing = $state<string | null>(null); // profile name being edited, else null
  let npName = $state("");
  let npDir = $state("");

  // Editable default engine + fallback.
  let provider = $state<Provider>(DEFAULT_PROVIDER);
  let profile = $state(""); // "" = default global login
  let fbTarget = $state<"none" | Provider>("none");
  let fbProfile = $state<string | null>(null);

  // Voice input (Whisper) key. Deliberately outside canonical()/save(): the
  // key is a secret with its own lifecycle, and omitting it from the main
  // patch is what keeps "typed nothing" distinct from "clear the key".
  let voiceKeySet = $state(false);
  let voiceKeyInput = $state("");
  let voiceKeySaving = $state(false);
  let voiceKeyError = $state<string | null>(null);
  let voiceKeySaved = $state(false);

  // How chat renders a finished thinking/working note. Also outside
  // canonical()/save(): it is a display preference, so it takes effect on click
  // rather than waiting behind the "Save defaults" button.
  let collapseReasoning = $state(false);
  let collapseSaving = $state(false);
  let collapseError = $state<string | null>(null);

  // Canonical JSON snapshot of the last-saved state, for dirty tracking.
  let baseline = $state("");
  let releaseNotesEl = $state<HTMLElement | null>(null);
  let tab = $state<SettingsTab>("providers");
  // Account row the chat sign-in CTA pointed at: scrolled to and ringed, but
  // never promoted to "used by new runs" — arriving from a failed turn should
  // not silently re-point every future run at that account.
  let focusedAccount = $state<string | null>(null);
  let handledSettingsFocusToken = 0;

  // Native only: which instance this app is connected to, and the way back to
  // the connection screen when the user wants a different one (R7).
  let connectedAddress = $state("");

  async function disconnect() {
    await connection.clear();
  }

  onMount(() => {
    void load();
    void refreshGit();
    void loadPreferences();
    void loadDevices();
    if (isNative) void connection.storedAddress().then((a) => (connectedAddress = a));
  });

  $effect(() => {
    const token = settingsFocusToken;
    const target = settingsFocusAccount;
    const ready = !loading;
    if (token <= 0) return;
    tab = settingsFocusTab;
    if (!target || settingsFocusTab !== "providers" || !ready || handledSettingsFocusToken === token) return;
    handledSettingsFocusToken = token;
    const targetProfile = target.profile && profiles.some((p) => p.Provider === target.provider && p.Name === target.profile)
      ? target.profile
      : "";
    void focusAccount(accountKey(target.provider, targetProfile));
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
    return p[providerMeta(p.Provider).profileDir.infoKey] ?? "";
  }

  // ── Provider accounts ──────────────────────────────────────────────────
  // A provider card lists its accounts: the provider CLI's own login directory
  // first, then every named profile pointed at that provider. Each account is a
  // separate login, so each carries its own sign-in state. The flow itself
  // lives in ProviderSignIn so Settings and the chat CTA behave identically;
  // this page only owns the per-account badges.
  function accountsFor(p: Provider): AccountRow[] {
    const rows: AccountRow[] = [
      {
        name: "",
        label: "default · global login",
        sub: `the ${providerMeta(p).label} CLI's own login directory`,
        canEdit: false,
      },
    ];
    for (const prof of profiles) {
      if (prof.Provider !== p) continue;
      rows.push({
        name: prof.Name,
        label: prof.Name,
        sub: pathForProfile(prof) || "provider default directory",
        canEdit: true,
      });
    }
    return rows;
  }

  function accountKey(p: Provider, name: string): string {
    return `${p}:${name}`;
  }

  // Card sub-line. An unprobed account counts as neither signed in nor out, so
  // the summary only ever claims what the daemon confirmed.
  function accountSummary(p: Provider): string {
    const rows = accountsFor(p);
    const label = rows.length === 1 ? "1 account" : `${rows.length} accounts`;
    if (authLoading && authStatuses.length === 0) return `${label} · checking sign-in…`;
    return `${label} · ${rows.filter((r) => authFor(p, r.name)?.logged_in).length} signed in`;
  }

  function authFor(p: Provider, name: string): ProviderAuthStatus | undefined {
    return authStatuses.find((s) => s.provider === p && s.profile === name);
  }

  // Three states, like the git card: an unprobed CLI is "unknown", not "signed
  // out" — claiming the latter would be a guess.
  function authBadge(p: Provider, name: string): { label: string; tone: string } {
    const st = authFor(p, name);
    if (!st) return { label: authLoading ? "checking" : "status unavailable", tone: "amber" };
    if (!st.found) return { label: "CLI missing", tone: "amber" };
    if (!st.login_checked) return { label: "sign-in unknown", tone: "amber" };
    return st.logged_in ? { label: "signed in", tone: "green" } : { label: "signed out", tone: "red" };
  }

  // Only accounts whose CLI is present and which the provider lets us drive can
  // be signed in from here.
  function canSignIn(p: Provider, name: string): boolean {
    const st = authFor(p, name);
    return !!st?.found && !!st.supports_login;
  }

  // Why an account's state is what it is, when that needs saying: a missing
  // CLI, or a provider whose sign-in Podiom cannot read.
  function authHint(p: Provider, name: string): string {
    const st = authFor(p, name);
    if (!st) return authLoading ? "" : "Podiom couldn't verify this account. Check again to retry.";
    if (!st.found) return st.install_hint || `Install ${providerMeta(p).label} before signing in.`;
    if (!st.login_checked) return st.login_hint || "Podiom cannot verify this provider's sign-in state automatically.";
    return "";
  }

  const npDirPh = $derived(providerMeta(npProvider ?? provider).profileDir.placeholder);

  // Clicking an account row points every new agent, task and session at it.
  function setDefaultAccount(p: Provider, name: string) {
    provider = p;
    profile = name;
    saved = false;
    focusedAccount = null;
  }

  function toggleAddAccount(p: Provider) {
    if (npOpen && !npEditing && npProvider === p) {
      npOpen = false;
      npProvider = null;
      return;
    }
    npOpen = true;
    npProvider = p;
    npEditing = null;
    npName = "";
    npDir = "";
    profileError = null;
    profileErrorProvider = null;
  }

  function startEditProfile(name: string) {
    const p = profiles.find((x) => x.Name === name);
    if (!p) return;
    npOpen = true;
    npProvider = p.Provider;
    npEditing = name;
    npName = p.Name;
    npDir = pathForProfile(p);
    profileError = null;
    profileErrorProvider = null;
  }

  function closeAccountPanel() {
    npOpen = false;
    npProvider = null;
    npEditing = null;
  }

  async function saveInlineProfile() {
    const name = npName.trim();
    const target = npProvider ?? provider;
    if (!name) return;
    profileSaving = true;
    profileError = null;
    profileErrorProvider = null;
    try {
      const body = {
        name,
        provider: target,
        config_dir: "",
        home_dir: "",
        [providerMeta(target).profileDir.bodyKey]: npDir.trim(),
      };
      if (npEditing) {
        await updateProfile(npEditing, body);
      } else {
        await createProfile(body);
      }
      profiles = await listProfiles();
      void onRefreshAuth(true); // a new/moved directory has its own sign-in state
      closeAccountPanel();
      npName = "";
      npDir = "";
    } catch (e) {
      profileError = e instanceof Error ? e.message : String(e);
      profileErrorProvider = target;
    } finally {
      profileSaving = false;
    }
  }

  async function removeProfile(name: string) {
    if (!window.confirm(`Delete profile ${name}?`)) return;
    const owner = profiles.find((x) => x.Name === name)?.Provider ?? provider;
    profileError = null;
    profileErrorProvider = null;
    try {
      await deleteProfile(name);
      profiles = await listProfiles();
      void onRefreshAuth(true);
      // The account is gone: anything still pointing at it has to let go.
      if (profile === name) {
        profile = "";
        saved = false;
      }
      if (fbProfile === name) {
        fbProfile = null;
        saved = false;
      }
      if (npEditing === name) closeAccountPanel();
    } catch (e) {
      profileError = e instanceof Error ? e.message : String(e);
      profileErrorProvider = owner;
    }
  }

  function applyConfig(cfg: GlobalConfig) {
    voiceKeySet = cfg.voice?.openai_api_key_set ?? false;
    collapseReasoning = cfg.collapse_reasoning ?? false;
    provider = cfg.provider;
    profile = cfg.profile ?? "";
    const fb = cfg.fallback ?? [];
    if (fb.length === 0) {
      fbTarget = "none";
      fbProfile = null;
    } else {
      const first = fb[0];
      if (isProvider(first)) {
        fbTarget = first;
        fbProfile = null;
      } else if (first === "default") {
        fbTarget = cfg.provider;
        fbProfile = null;
      } else {
        const prof = profiles.find((p) => p.Name === first);
        fbTarget = prof ? prof.Provider : DEFAULT_PROVIDER;
        fbProfile = first;
      }
    }
    baseline = canonical();
    saved = false;
  }

  const fbProfileOptions = $derived(
    fbTarget === "none" ? [] : profiles.filter((p) => p.Provider === fbTarget).map((p) => p.Name),
  );

  function buildFallback(): string[] {
    if (fbTarget === "none") return [];
    if (fbProfile && fbProfileOptions.includes(fbProfile)) return [fbProfile];
    return [fbTarget];
  }

  // One flat list of fallback destinations: no fallback, each provider on its
  // own login, then each named account. Mirrors what the config accepts.
  const fbChips = $derived.by(() => {
    const chips: { key: string; label: string; target: "none" | Provider; profile: string | null }[] = [
      { key: "none", label: "None", target: "none", profile: null },
    ];
    for (const p of PROVIDERS) {
      chips.push({ key: p.id, label: p.label, target: p.id, profile: null });
      for (const prof of profiles) {
        if (prof.Provider !== p.id) continue;
        chips.push({ key: `${p.id}:${prof.Name}`, label: `${p.label} · ${prof.Name}`, target: p.id, profile: prof.Name });
      }
    }
    return chips;
  });
  const fbSelectedKey = $derived(
    fbTarget === "none" ? "none" : fbProfile && fbProfileOptions.includes(fbProfile) ? `${fbTarget}:${fbProfile}` : fbTarget,
  );
  const fbDestLabel = $derived(fbChips.find((c) => c.key === fbSelectedKey)?.label ?? "None");
  const fbSummary = $derived(
    fbTarget === "none"
      ? "No fallback — rate-limited runs pause and retry automatically when the limit resets. Nothing is dropped."
      : `Rate-limited ${providerMeta(provider).label} runs re-route to ${fbDestLabel}.`,
  );

  function canonical(): string {
    return JSON.stringify({ provider, profile, fallback: buildFallback() });
  }

  const dirty = $derived(canonical() !== baseline);

  function setProvider(p: Provider) {
    if (p === provider) return;
    provider = p;
    saved = false;
    // The default account is tied to the provider; drop it if it no longer fits.
    if (profile && !profiles.some((x) => x.Name === profile && x.Provider === p)) profile = "";
    closeAccountPanel();
  }

  function setFallback(target: "none" | Provider, name: string | null) {
    fbTarget = target;
    fbProfile = name;
    saved = false;
  }

  async function save() {
    saving = true;
    error = null;
    try {
      const cfg = await updateConfig({ provider, profile, fallback: buildFallback() });
      applyConfig(cfg);
      saved = true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      saved = false;
    } finally {
      saving = false;
    }
  }

  // Saves or clears only the voice key — the daemon leaves every other global
  // setting untouched when the patch omits it.
  async function patchVoiceKey(key: string) {
    voiceKeySaving = true;
    voiceKeyError = null;
    voiceKeySaved = false;
    try {
      const cfg = await updateConfig({ voice: { openai_api_key: key } });
      voiceKeySet = cfg.voice?.openai_api_key_set ?? false;
      voiceKeyInput = "";
      voiceKeySaved = true;
    } catch (e) {
      voiceKeyError = e instanceof Error ? e.message : String(e);
    } finally {
      voiceKeySaving = false;
    }
  }

  async function setCollapseReasoning(value: boolean) {
    if (value === collapseReasoning || collapseSaving) return;
    collapseSaving = true;
    collapseError = null;
    try {
      applyConfig(await updateConfig({ collapse_reasoning: value }));
    } catch (e) {
      collapseError = e instanceof Error ? e.message : String(e);
    } finally {
      collapseSaving = false;
    }
  }

  async function focusReleaseNotes() {
    await tick();
    releaseNotesEl?.scrollIntoView({ behavior: "smooth", block: "start" });
    releaseNotesEl?.focus({ preventScroll: true });
  }

  // Arriving from the chat sign-in CTA: show the account, don't touch it.
  async function focusAccount(key: string) {
    focusedAccount = key;
    await tick();
    const el = document.querySelector<HTMLElement>(`[data-account="${CSS.escape(key)}"]`);
    el?.scrollIntoView({ behavior: "smooth", block: "center" });
    el?.focus({ preventScroll: true });
  }

  // ---- Version & updates: presentation of App.svelte's update state ----
  const canCheck = $derived(updateState === "idle" || updateState === "current" || updateState === "failed");
  const updateBadge = $derived(updateBadgeFor(updateState));
  const releaseNotes = $derived(update?.release_notes ?? "");
  const pushBadge = $derived(pushBadgeFor(pushState));

  // ---- registered devices ----------------------------------------------------
  //
  // One installation can have several. Muting one is registration state — whether that
  // phone receives anything — and is deliberately separate from the preferences below,
  // which decide which events matter at all.
  let devices = $state<NotificationDevice[]>([]);
  let devicesBusy = $state<string | null>(null);

  async function loadDevices() {
    try {
      devices = (await listNotificationDevices()).devices;
    } catch {
      // A daemon without native push configured has none. Not worth an error.
      devices = [];
    }
  }

  async function toggleDevice(id: string, enabled: boolean) {
    devicesBusy = id;
    try {
      const updated = await setNotificationDeviceEnabled(id, enabled);
      devices = devices.map((d) => (d.id === updated.id ? updated : d));
    } catch (e) {
      prefsError = e instanceof Error ? e.message : String(e);
    } finally {
      devicesBusy = null;
    }
  }

  async function forgetDevice(id: string) {
    devicesBusy = id;
    try {
      // Removing the device this app is running on also drops its push token and the
      // stored device id, so it stops receiving and does not silently re-register.
      if (isNative && id === (await currentDeviceID())) await unregisterFromDaemon();
      else await deleteNotificationDevice(id);
      devices = devices.filter((d) => d.id !== id);
    } catch (e) {
      prefsError = e instanceof Error ? e.message : String(e);
    } finally {
      devicesBusy = null;
    }
  }

  // ---- test push ---------------------------------------------------------------
  //
  // Deliberately reports per device rather than as one success/failure. The relay
  // answers HTTP 200 even when every device is rejected, so "the request worked" and
  // "the phone buzzed" are different questions and only the second one matters here.
  let testBusy = $state(false);
  let testResults = $state<NotificationTestResult[] | null>(null);
  let testError = $state<string | null>(null);

  async function sendTestPush() {
    testBusy = true;
    testError = null;
    testResults = null;
    try {
      testResults = (await sendTestNotificationPush()).results;
      // A verdict can retire a device, so the list is refreshed rather than left
      // showing a registration the relay has just told us is gone.
      await loadDevices();
    } catch (e) {
      testError = e instanceof Error ? e.message : String(e);
    } finally {
      testBusy = false;
    }
  }

  // testResultCopy turns the relay's vocabulary into something readable, keeping the
  // raw reason available for the cases the UI has no wording for.
  function testResultCopy(result: NotificationTestResult): string {
    if (result.status === "accepted") return "sent to this device";
    if (result.error?.includes("unregistered") || result.error?.includes("unknown_device")) {
      return "needs re-registering";
    }
    return result.error || "delivery failed";
  }

  // ---- notification preferences ------------------------------------------------
  //
  // The daemon owns the model. It is fetched rather than assembled here so the labels,
  // grouping and defaults live in one place, and so a notification type added later
  // shows up without a frontend change.
  let prefGroups = $state<NotificationPreferenceGroup[]>([]);
  let prefsSaving = $state(false);
  let prefsError = $state<string | null>(null);

  async function loadPreferences() {
    prefsError = null;
    try {
      prefGroups = (await getNotificationPreferences()).groups;
    } catch (e) {
      prefsError = e instanceof Error ? e.message : String(e);
    }
  }

  // togglePreference writes one switch and adopts the response.
  //
  // The response is the whole updated model, so there is no follow-up read and no local
  // guess about what the server decided — one switch writes a preference for every
  // delivery channel, which is not something to reimplement here.
  async function togglePreference(type: string, enabled: boolean) {
    prefsSaving = true;
    prefsError = null;
    try {
      prefGroups = (await setNotificationPreferences([{ type, enabled }])).groups;
    } catch (e) {
      prefsError = e instanceof Error ? e.message : String(e);
      // Re-read so the checkboxes reflect the server rather than the failed attempt.
      await loadPreferences();
    } finally {
      prefsSaving = false;
    }
  }
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
  // The wording differs between the app and the browser because the thing being granted
  // does — an OS-level permission for this device, or a site permission for this browser
  // — and so does where the user goes to change their mind about it.
  //
  // This describes whether Podiom can reach you at all. Which events are worth reaching
  // you about is the separate card below; keeping them apart is what lets a user tell
  // which of the two is stopping a notification.
  function pushCopyFor(state: PushState): { title: string; body: string } {
    switch (state) {
      case "enabled":
        return isNative
          ? { title: "Notifications are on", body: "This device is registered with Podiom and will receive notifications wherever you are." }
          : { title: "Notifications are on", body: "Podiom will keep this browser registered while notification permission remains approved." };
      case "enabling":
        return { title: "Enabling notifications", body: "Waiting for registration to finish." };
      case "denied":
        return isNative
          ? { title: "Notifications are blocked", body: "Notifications are turned off for Podiom in your device settings. Allow them there to use them here." }
          : { title: "Notifications are blocked", body: "Your browser has blocked notification permission for Podiom. Change the site permission in the browser to use them." };
      case "unsupported":
        return { title: "Notifications are unavailable", body: "This browser or daemon is missing Web Push support." };
      default:
        return {
          title: "Notifications are not enabled",
          body: "Turn them on to hear about agent questions, permission requests, goal and schedule activity, action items, and failures — without watching the dashboard.",
        };
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
      <button class:active={tab === "providers"} onclick={() => (tab = "providers")}>Providers</button>
      <button class:active={tab === "general"} onclick={() => (tab = "general")}>General</button>
      <button class:active={tab === "agents"} onclick={() => (tab = "agents")}>Agents</button>
      <button class:active={tab === "about-you"} onclick={() => (tab = "about-you")}>About you</button>
      <button class:active={tab === "credentials"} onclick={() => (tab = "credentials")}>Credentials</button>
      <button class:active={tab === "updates"} onclick={() => (tab = "updates")}>Version &amp; Updates</button>
      <button class:active={tab === "notifications"} onclick={() => (tab = "notifications")}>Notifications</button>
      <button class:active={tab === "logs"} onclick={() => (tab = "logs")}>Logs</button>
    </div>

    {#if tab === "providers"}
    <!-- ===== PROVIDERS ===== -->
    {#if loading}
      <div class="empty-note">Loading providers…</div>
    {:else}
      {#each PROVIDERS as p (p.id)}
        {@const isDefault = provider === p.id}
        <section class="prov-card" class:is-default={isDefault}>
          <div class="prov-head">
            <span class="prov-logo" style="background:{p.accent.bg};border-color:{p.accent.bd}">
              <ProviderLogo provider={p.id} size={23} />
            </span>
            <div class="grow">
              <div class="prov-name">
                {p.label}
                {#if isDefault}<span class="prov-pill">DEFAULT ENGINE</span>{/if}
              </div>
              <div class="prov-sub">{accountSummary(p.id)}</div>
            </div>
            {#if !isDefault}
              <button class="prov-default-btn" onclick={() => setProvider(p.id)}>Make default</button>
            {/if}
          </div>

          <div class="accounts">
            {#each accountsFor(p.id) as acct (acct.name)}
              {@const key = accountKey(p.id, acct.name)}
              {@const badge = authBadge(p.id, acct.name)}
              {@const hint = authHint(p.id, acct.name)}
              {@const inUse = isDefault && profile === acct.name}
              <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
              <div class="acct" class:focused={focusedAccount === key} data-account={key} tabindex="-1">
                <div class="acct-row">
                  <button
                    class="acct-pick"
                    class:on={inUse}
                    title="Use this account for new agents, tasks and sessions"
                    onclick={() => setDefaultAccount(p.id, acct.name)}>
                    <span class="auth-dot {badge.tone}" title={badge.label}></span>
                    <span class="acct-id">
                      <span class="acct-label">
                        {acct.label}
                        {#if inUse}<span class="acct-inuse">used by new runs</span>{/if}
                      </span>
                      <span class="acct-sub mono">{acct.sub}</span>
                    </span>
                  </button>

                  {#if acct.canEdit}
                    <button class="acct-tool" title="Edit account" aria-label="Edit account {acct.label}" onclick={() => startEditProfile(acct.name)}>
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/></svg>
                    </button>
                    <button class="acct-tool del" title="Delete account" aria-label="Delete account {acct.label}" onclick={() => removeProfile(acct.name)}>×</button>
                  {/if}

                  <span class="acct-badge {badge.tone}">{badge.label}</span>

                  {#if canSignIn(p.id, acct.name)}
                    <div class="acct-auth" class:signed-in={authFor(p.id, acct.name)?.logged_in}>
                      <ProviderSignIn
                        provider={p.id}
                        profile={acct.name}
                        startLabel={authFor(p.id, acct.name)?.logged_in ? "Sign in again" : "Sign in"}
                        onSignedIn={() => void onRefreshAuth(true)} />
                    </div>
                  {:else}
                    <button class="acct-recheck" disabled={authLoading} onclick={() => void onRefreshAuth(true)}>
                      {authLoading ? "Checking…" : "Check again"}
                    </button>
                  {/if}
                </div>

                {#if hint}
                  <div class="acct-note">{hint}</div>
                {/if}

                {#if npOpen && npEditing === acct.name}
                  <div class="np-panel inline">
                    <div class="np-title">edit account · {providerMeta(p.id).label}</div>
                    <input class="np-input" bind:value={npName} disabled placeholder="account name" />
                    <input class="np-input mono" bind:value={npDir} placeholder={npDirPh} />
                    <div class="np-actions">
                      <button class="np-create" disabled={profileSaving || !npName.trim()} onclick={saveInlineProfile}>
                        {profileSaving ? "Saving…" : "Save changes"}
                      </button>
                      <button class="np-cancel" onclick={closeAccountPanel}>Cancel</button>
                    </div>
                  </div>
                {/if}
              </div>
            {/each}
          </div>

          <div class="prov-foot">
            <button class="acct-add" onclick={() => toggleAddAccount(p.id)}>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M12 5v14"/><path d="M5 12h14"/></svg>Add account
            </button>
            {#if npOpen && !npEditing && npProvider === p.id}
              <div class="np-panel">
                <div class="np-title">new account · {providerMeta(p.id).label}</div>
                <input class="np-input" bind:value={npName} placeholder="account name — e.g. work" />
                <input class="np-input mono" bind:value={npDir} placeholder={npDirPh} />
                <div class="np-actions">
                  <button class="np-create" disabled={profileSaving || !npName.trim()} onclick={saveInlineProfile}>
                    {profileSaving ? "Saving…" : "Create account"}
                  </button>
                  <button class="np-cancel" onclick={closeAccountPanel}>Cancel</button>
                </div>
              </div>
            {/if}
            <div class="hint">
              Each account is its own login with its own rate limit, so Podiom can switch accounts when one runs out.
              The default account is whatever the {providerMeta(p.id).label} CLI is already signed into.
            </div>
            {#if profileError && profileErrorProvider === p.id}
              <div class="error-banner" style="margin-top:10px">{profileError}</div>
            {/if}
          </div>

          {#if isDefault}
            <!-- Fallback is one chain on the global config, and it only applies
                 to runs that start on the default engine — so it belongs here. -->
            <div class="fb-strip">
              <div class="fb-route">
                <span class="fb-tag">fallback</span>
                <span class="fb-badge"><ProviderLogo provider={p.id} size={13} />{p.label}</span>
                <svg class="fb-arrow" width="44" height="12" viewBox="0 0 44 12" fill="none" aria-hidden="true">
                  <path d="M2 6h32" stroke="#c9a24e" stroke-width="2" stroke-linecap="round" stroke-dasharray="5 4"/>
                  <path d="M35 1.5 42 6l-7 4.5" stroke="#c9a24e" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                </svg>
                <span class="fb-badge dest" class:muted={fbTarget === "none"}>{fbDestLabel}</span>
                <span class="fb-when">when rate-limited · 429</span>
              </div>
              <div class="fb-chips">
                {#each fbChips as c (c.key)}
                  <button class="chip" class:on={fbSelectedKey === c.key} onclick={() => setFallback(c.target, c.profile)}>{c.label}</button>
                {/each}
              </div>
              <div class="fb-summary">{fbSummary}</div>
            </div>
          {/if}
        </section>
      {/each}

      {#if authError}
        <div class="error-banner" style="margin-top:16px">{authError}</div>
      {/if}

      <!-- ===== SAVE ===== -->
      <div class="save-row">
        <button class="btn-save" disabled={saving} onclick={save}>{saving ? "Saving…" : "Save defaults"}</button>
        {#if saved && !dirty}
          <span class="save-ok"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>Saved to config.yaml</span>
        {:else}
          <span class="save-hint">Changes apply to new runs immediately.</span>
        {/if}
      </div>
    {/if}

    {:else if tab === "general"}
    <!-- ===== CHAT DISPLAY ===== -->
    <section class="card">
      <div class="card-head">
        <div class="card-icon violet">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
        </div>
        <div class="grow">
          <div class="card-title">Chat display</div>
          <div class="card-sub">How an agent's thinking and working notes read once its answer has arrived.</div>
        </div>
      </div>

      <div class="rows">
        <div class="row top">
          <span class="row-key">notes</span>
          <div class="chips-col">
            <div class="chips">
              <button class="chip" class:on={!collapseReasoning} disabled={collapseSaving} onclick={() => setCollapseReasoning(false)}>always expanded</button>
              <button class="chip" class:on={collapseReasoning} disabled={collapseSaving} onclick={() => setCollapseReasoning(true)}>collapse when done</button>
            </div>
            <div class="hint">
              Notes always stream in full while a turn runs. Collapsing folds each finished one down to a single clickable
              line so long turns stay readable.
            </div>
            {#if collapseError}
              <div class="field-error">{collapseError}</div>
            {/if}
          </div>
        </div>
      </div>
    </section>

    {#if isNative}
      <!-- ===== CONNECTION (native only) ===== -->
      <section class="card">
        <div class="card-head">
          <div class="card-icon violet">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.55a11 11 0 0 1 14 0"/><path d="M8.5 16.11a6 6 0 0 1 7 0"/><line x1="12" y1="20" x2="12" y2="20"/></svg>
          </div>
          <div class="grow">
            <div class="card-title">Podiom instance</div>
            <div class="card-sub">The daemon this app talks to. Disconnecting forgets the address and the gateway token, and returns you to the connection screen.</div>
          </div>
        </div>

        <div class="rows">
          <div class="row">
            <span class="row-key">address</span>
            <span class="mono">{connectedAddress || "not configured"}</span>
          </div>
          <div class="row">
            <span class="row-key">&nbsp;</span>
            <button class="chip" onclick={disconnect}>Disconnect</button>
          </div>
        </div>
      </section>
    {/if}

    {:else if tab === "agents"}
    <!-- ===== AGENTS ===== -->
    <Agents embedded {agents} onHire={onHireAgent} {onOpenChat} onChanged={onAgentsChanged} />

    {:else if tab === "about-you"}
    <!-- ===== ABOUT YOU (USER.md) ===== -->
    <AboutYou {agents} onSaved={onUserProfileSaved} />

    {:else if tab === "credentials"}
    <!-- ===== GIT ===== -->
    <section class="card">
      <div class="card-head">
        <div class="card-icon violet">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M6 15V9a9 9 0 0 1 9-9"/></svg>
        </div>
        <div class="grow">
          <div class="card-title">Git</div>
          <div class="card-sub">Source control for projects that use it. Podiom uses your own git credentials — it never creates any.</div>
        </div>
        <span class="badge {gitBadge.tone}">{gitLoading ? "checking…" : gitBadge.label}</span>
      </div>

      <div class="git-steps">
        <div class="git-step" class:done={!!git?.found}>
          <span class="git-step-mark">{git?.found ? "✓" : "1"}</span>
          <div class="grow">
            <div class="git-step-title">git installed</div>
            <div class="git-step-sub">
              {#if git?.found}
                <span class="mono">{git.version || git.path}</span>
              {:else}
                {git?.hint || "Install git, then re-check."}
              {/if}
            </div>
          </div>
        </div>

        <div class="git-step" class:done={gitIdentitySet}>
          <span class="git-step-mark">{gitIdentitySet ? "✓" : "2"}</span>
          <div class="grow">
            <div class="git-step-title">commit identity</div>
            <div class="git-step-sub">The name and email your agents' commits are attributed to.</div>
            <div class="git-identity">
              <input class="timeout-input" style="flex:1;min-width:150px" bind:value={gitName} placeholder="Your Name" disabled={!git?.found} />
              <input class="timeout-input" style="flex:1;min-width:180px" bind:value={gitEmail} placeholder="you@example.com" disabled={!git?.found} />
              <button
                class="btn-save sm"
                disabled={!git?.found || gitSaving || !gitName.trim() || !gitEmail.trim()}
                onclick={saveGitIdentity}
              >{gitSaving ? "Saving…" : "Save"}</button>
            </div>
          </div>
        </div>

        <div class="git-step" class:done={!!git?.ssh_key}>
          <span class="git-step-mark">{git?.ssh_key ? "✓" : "3"}</span>
          <div class="grow">
            <div class="git-step-title">credentials</div>
            {#if git?.ssh_key}
              <div class="git-step-sub">
                An SSH key is present. If a repository still refuses you, add this public key to your git host.
              </div>
              <div class="git-key">
                <code class="mono">{git.ssh_key}</code>
                <button class="btn-save sm" onclick={copySSHKey}>Copy</button>
              </div>
            {:else}
              <div class="git-step-sub">
                No SSH key found. Create one with
                <span class="mono">ssh-keygen -t ed25519 -C "{gitEmail || "you@example.com"}"</span>
                and add the public half to your git host — or configure a credential helper. Podiom uses whatever you set up.
              </div>
            {/if}
          </div>
        </div>
      </div>

      {#if gitError}
        <div class="voice-key-error">{gitError}</div>
      {/if}
      <div class="hint">
        Projects choose whether they use git on the Projects page. A project with source control gets a real working
        copy the agent operates on with plain <span class="mono">git</span>.
        <button class="link-btn" onclick={refreshGit}>Re-check</button>
      </div>
    </section>

    <!-- ===== VOICE INPUT ===== -->
    <section class="card">
      <div class="card-head">
        <div class="card-icon teal">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5 11a7 7 0 0 0 14 0"/><line x1="12" y1="18" x2="12" y2="21"/></svg>
        </div>
        <div class="grow">
          <div class="card-title">Voice input</div>
          <div class="card-sub">Speak prompts in chat, tasks, and goals — transcribed server-side with the OpenAI Whisper API.</div>
        </div>
        {#if voiceKeySet}
          <span class="voice-key-badge on">key set</span>
        {:else}
          <span class="voice-key-badge">no key</span>
        {/if}
      </div>

      <div class="rows">
        <div class="row">
          <span class="row-key">openai key</span>
          <div class="voice-key-controls">
            <input
              class="timeout-input mono"
              style="max-width:260px;flex:1"
              type="password"
              autocomplete="off"
              bind:value={voiceKeyInput}
              placeholder={voiceKeySet ? "replace key…" : "sk-…"}
            />
            <button class="btn-save sm" disabled={voiceKeySaving || !voiceKeyInput.trim()} onclick={() => patchVoiceKey(voiceKeyInput.trim())}>
              {voiceKeySaving ? "Saving…" : "Save key"}
            </button>
            {#if voiceKeySet}
              <button class="voice-key-clear" disabled={voiceKeySaving} onclick={() => patchVoiceKey("")}>Clear</button>
            {/if}
          </div>
        </div>
        <div class="hint">
          Stored as <span class="mono">voice.openai_api_key</span> in config.yaml. The key stays on this machine — audio is uploaded to
          OpenAI for transcription only. Setting <span class="mono">PODIOM_OPENAI_API_KEY</span> or <span class="mono">OPENAI_API_KEY</span>
          in the daemon's environment overrides it.
        </div>
        {#if voiceKeyError}
          <div class="voice-key-error">{voiceKeyError}</div>
        {:else if voiceKeySaved}
          <div class="voice-key-ok">Saved to config.yaml.</div>
        {/if}
      </div>
    </section>

    <!-- ===== AGENT-GRANTED SECRETS ===== -->
    <Credentials {onOpenChat} />

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
          <div class="card-title">{isNative ? "Push notifications" : "Browser notifications"}</div>
          <div class="card-sub">
            {isNative
              ? "Whether this device can receive Podiom notifications."
              : "Whether this browser can receive Podiom notifications."}
          </div>
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

    {#if devices.length > 0}
      <section class="card">
        <div class="card-head">
          <div class="card-icon teal">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="2" width="14" height="20" rx="2"/><path d="M12 18h.01"/></svg>
          </div>
          <div class="grow">
            <div class="card-title">Devices</div>
            <div class="card-sub">
              Phones and tablets registered for push. Muting one stops notifications
              reaching it without changing what the others get.
            </div>
          </div>
        </div>
        <div class="pref-group">
          {#each devices as device (device.id)}
            <div class="pref-row">
              <input
                type="checkbox"
                checked={device.enabled}
                disabled={devicesBusy === device.id}
                onchange={() => toggleDevice(device.id, !device.enabled)} />
              <span class="pref-label">{device.label || device.platform}</span>
              <span class="pref-type mono">
                {device.status === "invalid" ? "needs re-registering" : device.platform}
              </span>
              <button
                class="link"
                disabled={devicesBusy === device.id}
                onclick={() => forgetDevice(device.id)}>Forget</button>
            </div>
          {/each}
        </div>

        <!-- A real push, on demand. The relay answers 200 even when every device is
             rejected, so the verdict is reported per device rather than as one
             "sent" — that difference is the only thing that makes this useful. -->
        <div class="notification-panel">
          <div class="grow">
            <div class="route-tag">test</div>
            <div class="notification-title">Send a test notification</div>
            <div class="notification-copy">
              Pushes a real notification to every device above and reports what the relay
              said about each one. Ignores the preferences below, so a muted event type
              does not change the answer.
            </div>
          </div>
          {#if testBusy}
            <span class="spinner-note amber"><span class="spinner amber"></span>Sending…</span>
          {:else}
            <button class="btn-save" onclick={sendTestPush}>Send test</button>
          {/if}
        </div>

        {#if testError}
          <div class="error-banner" style="margin-top:10px">{testError}</div>
        {:else if testResults}
          <div class="pref-group">
            {#if testResults.length === 0}
              <div class="pref-row">
                <span class="pref-label">No device was eligible</span>
                <span class="pref-type mono">all muted or awaiting re-registration</span>
              </div>
            {:else}
              {#each testResults as result (result.device_id)}
                <div class="pref-row">
                  <span class="pref-label">{result.label || result.platform || result.device_id}</span>
                  <span class="pref-type mono">{testResultCopy(result)}</span>
                </div>
              {/each}
            {/if}
          </div>
        {/if}
      </section>
    {/if}

    <!-- Which Podiom events notify you.
         Deliberately separate from the delivery status above: that answers "can this
         device receive anything", these answer "which events are worth telling me
         about". Conflating the two would make muting a device look like changing what
         Podiom considers important.

         The whole model — groups, headings, labels and defaults — comes from the daemon,
         so a notification type added in a later release appears here with no change to
         this file. -->
    <section class="card">
      <div class="card-head">
        <div class="card-icon teal">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 6h16"/><path d="M4 12h16"/><path d="M4 18h10"/></svg>
        </div>
        <div class="grow">
          <div class="card-title">What notifies you</div>
          <div class="card-sub">
            Events that need you are on by default. Routine activity is off — turn on what
            you want to follow.
          </div>
        </div>
        {#if prefsSaving}
          <span class="spinner-note amber"><span class="spinner amber"></span>Saving…</span>
        {/if}
      </div>

      {#if prefsError}
        <div class="notification-panel"><div class="grow"><div class="notification-copy">{prefsError}</div></div></div>
      {:else if prefGroups.length === 0}
        <div class="notification-panel"><div class="grow"><div class="notification-copy">Loading…</div></div></div>
      {:else}
        {#each prefGroups as group (group.category)}
          <div class="pref-group">
            <div class="route-tag">{group.title}</div>
            {#each group.rows as row (row.type)}
              <label class="pref-row">
                <input
                  type="checkbox"
                  checked={row.enabled}
                  disabled={prefsSaving}
                  onchange={() => togglePreference(row.type, !row.enabled)} />
                <span class="pref-label">{row.label}</span>
                <span class="pref-type mono">{row.type}</span>
              </label>
            {/each}
          </div>
        {/each}
      {/if}
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

  .field-error {
    font: 600 12px "Hanken Grotesk";
    color: #9a4e2f;
  }

  /* ── provider cards ─────────────────────────────────────────────────── */
  .prov-card {
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 20px;
    margin-bottom: 18px;
    overflow: hidden;
    box-shadow:
      0 1px 2px rgba(43, 37, 32, 0.04),
      0 16px 40px -28px rgba(43, 37, 32, 0.22);
  }

  .prov-head {
    display: flex;
    align-items: center;
    gap: 13px;
    padding: 20px 22px 16px;
    background: linear-gradient(180deg, var(--surface-2), var(--surface));
    border-bottom: 1px solid var(--line);
  }

  .prov-card.is-default .prov-head {
    background: linear-gradient(180deg, #eef6f2, var(--surface));
  }

  .prov-logo {
    width: 42px;
    height: 42px;
    border-radius: 13px;
    display: grid;
    place-items: center;
    flex: none;
    background: var(--surface-2);
    border: 1px solid var(--line);
  }

  .prov-name {
    display: flex;
    align-items: center;
    gap: 9px;
    font: 800 19px "Hanken Grotesk";
    letter-spacing: -0.02em;
    color: var(--ink);
  }

  .prov-pill {
    padding: 4px 11px;
    border-radius: 999px;
    font: 700 9.5px "JetBrains Mono", monospace;
    letter-spacing: 0.07em;
    background: linear-gradient(150deg, #47997f, var(--teal-deep));
    color: var(--surface);
  }

  .prov-sub {
    font: 400 12.5px "Hanken Grotesk";
    color: var(--faint);
    margin-top: 2px;
  }

  .prov-default-btn {
    flex: none;
    border: 1px solid var(--field-line);
    border-radius: 11px;
    padding: 8px 14px;
    background: rgba(255, 255, 255, 0.85);
    color: var(--muted);
    font: 600 12.5px "Hanken Grotesk";
    cursor: pointer;
  }

  .prov-default-btn:hover {
    border-color: #bfe0d6;
    background: #e3f1ec;
    color: var(--teal-deep);
  }

  /* ── account rows ───────────────────────────────────────────────────── */
  .accounts {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 16px 22px 0;
  }

  .acct {
    border: 1px solid var(--line-3);
    border-radius: 14px;
    background: var(--surface-3);
    outline: none;
  }

  .acct:hover {
    border-color: #e2d6c6;
  }

  .acct.focused {
    border-color: #9ecdc0;
    box-shadow: 0 0 0 3px rgba(63, 143, 126, 0.14);
  }

  .acct-row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
    padding: 12px 15px;
  }

  /* The row's own button: activating it makes this the account new runs use. */
  .acct-pick {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 12px;
    border: none;
    background: transparent;
    padding: 0;
    text-align: left;
    cursor: pointer;
  }

  .acct-id {
    min-width: 0;
    display: block;
  }

  .acct-label {
    display: flex;
    align-items: center;
    gap: 8px;
    font: 700 13.5px "Hanken Grotesk";
    color: var(--ink);
  }

  .acct-pick.on .acct-label {
    color: var(--teal-ink);
  }

  .acct-inuse {
    font: 600 9.5px "JetBrains Mono", monospace;
    letter-spacing: 0.05em;
    color: #8a7560;
    background: #f1eadf;
    border: 1px solid #e6dbcb;
    border-radius: 999px;
    padding: 2px 8px;
  }

  .acct-sub {
    display: block;
    font: 500 11px "JetBrains Mono", monospace;
    color: var(--faint);
    margin-top: 3px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .acct-tool {
    flex: none;
    display: inline-flex;
    align-items: center;
    border: none;
    background: transparent;
    padding: 2px;
    color: #a0937f;
    cursor: pointer;
  }

  .acct-tool:hover {
    color: var(--teal-deep);
  }

  .acct-tool.del {
    color: #b79e86;
    font: 700 15px/1 "Hanken Grotesk";
  }

  .acct-tool.del:hover {
    color: #c2705e;
  }

  .acct-badge {
    flex: none;
    padding: 5px 11px;
    border-radius: 999px;
    font: 600 11px "Hanken Grotesk";
    background: #f1eadf;
    border: 1px solid #e6dbcb;
    color: var(--muted);
  }

  .acct-badge.green {
    background: #eaf1ed;
    border-color: #cfe3d8;
    color: #3f7a5f;
  }

  .acct-badge.amber {
    background: #fcf8ee;
    border-color: #eadbb8;
    color: #9a6c17;
  }

  .acct-badge.red {
    background: #fceee8;
    border-color: #f2d6c8;
    color: #b14e2a;
  }

  .acct-auth {
    flex: none;
  }

  /* Re-authenticating a working account is rare — keep the button quiet so the
     rows that actually need attention are the ones that read as urgent. */
  .acct-auth.signed-in :global(.signin-btn) {
    border: 1px solid #b9d4c1;
    background: #fff;
    color: #35674f;
  }

  /* Once a login is running the component grows a help line, inputs and a
     cancel button — too much for the row, so it drops to its own strip. */
  .acct-auth:has(:global(.signin-help)) {
    flex: 1 0 100%;
    border-top: 1px dashed var(--line);
    padding-top: 12px;
    animation: popIn 0.18s ease;
  }

  .acct-recheck {
    flex: none;
    padding: 7px 12px;
    border: 1px solid var(--field-line);
    border-radius: 10px;
    background: #fff;
    color: var(--muted-2);
    font: 650 12px "Hanken Grotesk";
    cursor: pointer;
  }

  .acct-recheck:disabled {
    opacity: 0.55;
    cursor: default;
  }

  .acct-note {
    border-top: 1px dashed var(--line);
    padding: 11px 15px;
    font: 500 12.5px/1.5 "Hanken Grotesk";
    color: var(--muted-2);
  }

  .prov-foot {
    padding: 12px 22px 18px;
  }

  .acct-add {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 7px 13px;
    border-radius: 10px;
    cursor: pointer;
    font: 600 12px "JetBrains Mono", monospace;
    border: 1px dashed #c9bdad;
    background: transparent;
    color: #8a7560;
  }

  .acct-add:hover {
    border-color: var(--teal);
    background: #eff6f1;
    color: var(--teal-deep);
  }

  .prov-foot .hint {
    margin-top: 11px;
    max-width: 560px;
  }

  /* ── fallback strip (default engine only) ───────────────────────────── */
  .fb-strip {
    padding: 14px 22px 17px;
    background: linear-gradient(180deg, #fcf7ec, #f9f1e0);
    border-top: 1px solid #f0e4cc;
  }

  .fb-route {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }

  .fb-tag {
    font: 600 9.5px "JetBrains Mono", monospace;
    letter-spacing: 0.08em;
    color: #b29b72;
    text-transform: uppercase;
    flex: none;
  }

  .fb-badge {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 5px 11px;
    border-radius: 999px;
    background: #fff;
    border: 1px solid #efe3cb;
    font: 700 11.5px "Hanken Grotesk";
    color: var(--ink);
  }

  .fb-badge.muted {
    background: transparent;
    color: var(--muted-2);
    font-weight: 600;
  }

  .fb-arrow {
    flex: none;
  }

  .fb-when {
    font: 600 9.5px "JetBrains Mono", monospace;
    letter-spacing: 0.08em;
    color: #c4af87;
    text-transform: uppercase;
    margin-left: auto;
  }

  .fb-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
    margin-top: 10px;
  }

  .fb-summary {
    font: 400 11.5px/1.5 "Hanken Grotesk";
    color: var(--muted-2);
    margin-top: 9px;
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

  .auth-dot {
    width: 7px;
    height: 7px;
    border-radius: 999px;
    flex: none;
    background: var(--muted-2);
  }

  .auth-dot.green {
    background: #3f7a5f;
  }

  .auth-dot.amber {
    background: #c08a22;
  }

  .auth-dot.red {
    background: #b4553f;
  }

  .route-tag {
    font: 600 9.5px "JetBrains Mono", monospace;
    letter-spacing: 0.12em;
    color: var(--faint);
    text-transform: uppercase;
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

  /* voice input */
  .btn-save.sm {
    padding: 8px 14px;
    border-radius: 10px;
    font-size: 13px;
  }

  .btn-save:disabled {
    opacity: 0.55;
  }

  .voice-key-badge {
    padding: 5px 11px;
    border-radius: 999px;
    font: 600 10.5px "JetBrains Mono", monospace;
    white-space: nowrap;
    border: 1px solid var(--line-2);
    color: var(--faint);
    align-self: flex-start;
  }

  .voice-key-badge.on {
    border-color: rgba(63, 143, 126, 0.5);
    color: #3f7a5f;
    background: rgba(63, 143, 126, 0.08);
  }

  .voice-key-controls {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    flex-wrap: wrap;
  }

  .voice-key-clear {
    border: 1px solid var(--line-2);
    border-radius: 10px;
    padding: 8px 13px;
    background: #fff;
    color: #8a5a4e;
    font: 600 13px "Hanken Grotesk";
    cursor: pointer;
  }

  .voice-key-error {
    font: 500 12.5px "Hanken Grotesk";
    color: #c0392b;
  }

  .voice-key-ok {
    font: 600 12.5px "Hanken Grotesk";
    color: #3f7a5f;
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

  .pref-group {
    padding: 0.75rem 1rem 0.5rem;
    border-top: 1px solid var(--line-soft, #f0ece5);
  }

  .pref-row {
    display: flex;
    align-items: center;
    gap: 0.55rem;
    padding: 0.3rem 0;
    cursor: pointer;
  }

  .pref-label {
    font-size: 0.84rem;
  }

  /* The machine-readable type is shown alongside the label: it is what the API and the
     docs use, so seeing it here makes a preference traceable to what produces it. */
  .pref-type {
    margin-left: auto;
    font-size: 0.68rem;
    color: var(--ink-soft, #6b625a);
    opacity: 0.6;
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

  .git-steps {
    display: flex;
    flex-direction: column;
    gap: 14px;
    margin-top: 4px;
  }
  .git-step {
    display: flex;
    gap: 11px;
    align-items: flex-start;
  }
  .git-step-mark {
    flex: none;
    width: 22px;
    height: 22px;
    border-radius: 999px;
    display: grid;
    place-items: center;
    font: 600 11px "JetBrains Mono", monospace;
    background: #EFEBE4;
    border: 1px solid #DED8CE;
    color: #7A7268;
  }
  .git-step.done .git-step-mark {
    background: #EAF1ED;
    border-color: #CFE3D8;
    color: #3F7A5F;
  }
  .git-step-title {
    font: 600 12.5px/1.4 "Inter", system-ui, sans-serif;
    color: #2C2A27;
  }
  .git-step-sub {
    font: 400 12px/1.6 "Inter", system-ui, sans-serif;
    color: #7A7268;
    margin-top: 2px;
  }
  .git-identity {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 8px;
  }
  .git-key {
    display: flex;
    gap: 8px;
    align-items: center;
    margin-top: 8px;
  }
  .git-key code {
    flex: 1;
    min-width: 0;
    overflow-x: auto;
    white-space: nowrap;
    background: #F6F3EE;
    border: 1px solid #E4DED4;
    border-radius: 7px;
    padding: 7px 9px;
    font-size: 11px;
    color: #5A544C;
  }
  .link-btn {
    background: none;
    border: 0;
    padding: 0;
    margin-left: 4px;
    font: inherit;
    color: #3F7A5F;
    text-decoration: underline;
    cursor: pointer;
  }
</style>
