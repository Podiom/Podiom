<script lang="ts">
  import { onMount } from "svelte";
  import NotificationCenter from "./lib/NotificationCenter.svelte";
  import { notifications } from "./lib/notifications.svelte";
  import { startNativePush } from "./lib/push";
  import {
    formatHash,
    parseHash,
    type GoalFocus,
    type Route as DeepLinkRoute,
    type Target,
  } from "./lib/deeplink";
  import {
    applyUpdate,
    checkUpdate,
    createProfile,
    getHealth,
    getOnboardingState,
    getOnboardingToken,
    getUserProfile,
    hireAgent,
    listAgents,
    listProfiles,
    providerStatus,
  } from "./lib/api";
  import { auth } from "./lib/auth.svelte";
  import { avatars } from "./lib/avatars.svelte";
  import { deployment } from "./lib/base";
  import { carryChatTarget, type ChatTarget } from "./lib/chattarget";
  import { keyboard, watchKeyboard } from "./lib/keyboard.svelte";
  import { live } from "./lib/live.svelte";
  import { initChrome, isNative, onBackButton } from "./lib/native";
  import { reachability } from "./lib/reachability.svelte";
  import OfflineGate from "./pages/OfflineGate.svelte";
  import TokenGate from "./pages/TokenGate.svelte";
  import HAOnboarding from "./pages/HAOnboarding.svelte";
  import ProviderLogo from "./lib/ProviderLogo.svelte";
  import SidebarUsage from "./lib/SidebarUsage.svelte";
  import { DEFAULT_PROVIDER, PROVIDERS, providerMeta } from "./lib/providers";
  import type { Agent, Health, PermissionMode, ProfileInfo, Provider, ProviderAuthStatus, UpdateStatus } from "./lib/types";
  import Chat from "./pages/Chat.svelte";
  import Roadmap from "./pages/Roadmap.svelte";
  import Goals from "./pages/Goals.svelte";
  import Schedules from "./pages/Schedules.svelte";
  import Projects from "./pages/Projects.svelte";
  import Skills from "./pages/Skills.svelte";
  import Settings from "./pages/Settings.svelte";
  import Terminal from "./pages/Terminal.svelte";
  import type { PushState } from "./lib/live.svelte";
  import WorkspaceFileViewer from "./lib/WorkspaceFileViewer.svelte";

  // Route and the hash grammar live in lib/deeplink.ts, which is the one place that
  // knows what a URL looks like — notifications name a logical target and it decides
  // the route, so nothing here has to be kept in step by hand.
  type Route = DeepLinkRoute;
  type SettingsTab = "providers" | "general" | "agents" | "about-you" | "credentials" | "toolset" | "updates" | "notifications" | "logs";

  interface SettingsAccountTarget {
    provider: Provider;
    profile: string;
  }

  interface NavItem {
    key: Route;
    label: string;
    icon: string;
  }

  const NAV: NavItem[] = [
    {
      key: "chat",
      label: "Chat",
      icon: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>',
    },
    {
      key: "roadmap",
      label: "Roadmap",
      icon: '<rect x="3" y="3" width="6" height="18" rx="1.5"/><rect x="10.5" y="3" width="6" height="11" rx="1.5"/><rect x="18" y="3" width="3" height="7" rx="1.5"/>',
    },
    {
      key: "goals",
      label: "Goals",
      icon: '<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="4.5"/><circle cx="12" cy="12" r="0.6"/>',
    },
    {
      key: "projects",
      label: "Projects",
      icon: '<path d="M3 8a2 2 0 0 1 2-2h4l2 2.5h8a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><circle cx="8.5" cy="13" r="1.4"/>',
    },
    {
      key: "schedules",
      label: "Schedules",
      icon: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
    },
    {
      key: "skills",
      label: "Skills & MCP",
      icon: '<rect x="3" y="3" width="7" height="7" rx="1.6"/><rect x="14" y="3" width="7" height="7" rx="1.6"/><rect x="3" y="14" width="7" height="7" rx="1.6"/><rect x="14" y="14" width="7" height="7" rx="1.6"/>',
    },
  ];

  const TERMINAL_NAV: NavItem = {
    key: "terminal",
    label: "Terminal",
    icon: '<path d="m4 17 6-6-6-6"/><path d="M12 19h8"/>',
  };

  // Gear icon for the Settings entry pinned in the sidebar footer.
  const SETTINGS_ICON =
    '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>';

  const SETTINGS_NAV: NavItem = { key: "settings", label: "Settings", icon: SETTINGS_ICON };
  const MORE_ICON = '<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>';
  // Bell for the Notification Center, which is reachable from the sidebar footer and
  // from the mobile More sheet.
  const BELL_ICON =
    '<path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/>';
  const MOBILE_PRIMARY_ROUTES = new Set<Route>(["chat", "roadmap", "goals", "schedules"]);

  let route = $state<Route>("chat");
  let moreOpen = $state(false);
  const mode = deployment();
  let showHAOnboarding = $state(false);
  let haBootstrapState = $state<"idle" | "checking" | "onboarding" | "failed">(
    mode === "ha" && !auth.token ? "checking" : "idle",
  );
  let haBootstrapError = $state<string | null>(null);
  let haBootstrapInFlight = false;
  let health = $state<Health | null>(null);
  let update = $state<UpdateStatus | null>(null);
  let updateState = $state<"idle" | "checking" | "available" | "current" | "updating" | "restarting" | "failed">("idle");
  let updateError = $state<string | null>(null);
  let daemonStatus = $state<"connecting" | "live" | "offline">("connecting");
  let agents = $state<Agent[]>([]);
  // Keep the shared avatar registry in step with the canonical agents list, so
  // every <AgentAvatar> (which only knows an agent's name) learns when a picture
  // is uploaded, changed, or removed.
  $effect(() => {
    avatars.syncFromAgents(agents);
  });
  let chatTarget = $state<ChatTarget | null>(null);
  let goalTarget = $state<string | null>(null);
  // goalFocus carries the sub-resource a deep link named — an action item, a question,
  // an access request — so a notification lands on the exact thing it was about rather
  // than the goal that holds it.
  //
  // Schedules and Roadmap are board views with no per-item detail to open, so their
  // targets resolve to the page itself. The hash still records the item, which keeps
  // those links addressable and lets a future detail view use them without a change
  // to the grammar.
  let goalFocus = $state<GoalFocus | null>(null);
  let releaseNotesFocusToken = $state(0);
  let settingsFocusTab = $state<SettingsTab>("providers");
  let settingsFocusToken = $state(0);
  let settingsFocusAccount = $state<SettingsAccountTarget | null>(null);
  let providerAuthStatuses = $state<ProviderAuthStatus[]>([]);
  let providerAuthLoading = $state(true);
  let providerAuthError = $state<string | null>(null);

  // Hire modal.
  let hireOpen = $state(false);
  let hireName = $state("");
  let hireProvider = $state<Provider>(DEFAULT_PROVIDER);
  let hireProfile = $state("");
  let hirePermission = $state<PermissionMode>("approve");
  let hireError = $state<string | null>(null);
  let profiles = $state<ProfileInfo[]>([]);
  let profileCreateOpen = $state(false);
  let profileName = $state("");
  let profilePath = $state("");
  let profileSaving = $state(false);

  // Notification opt-in state for the Web Push toggle.
  let pushState = $state<PushState>("idle");

  // First-run "get to know me" invite: shown until a USER.md exists or the
  // user dismisses it (dismissal persists per browser).
  const PROFILE_INVITE_DISMISSED = "podiom.about-you-dismissed";
  let profileInvite = $state(false);

  async function refreshProfileInvite() {
    if (localStorage.getItem(PROFILE_INVITE_DISMISSED) === "1") {
      profileInvite = false;
      return;
    }
    try {
      const info = await getUserProfile();
      profileInvite = !info.exists;
    } catch {
      profileInvite = false;
    }
  }

  function dismissProfileInvite() {
    localStorage.setItem(PROFILE_INVITE_DISMISSED, "1");
    profileInvite = false;
  }

  // Boot only once the gateway token is present (HA10): before it, nothing but
  // the token screen renders and no API/WS traffic is attempted. Re-runs after
  // a rotation (token cleared → re-entered) to reopen the socket and refresh.
  let booted = $state(false);
  $effect(() => {
    if (mode !== "ha") return;
    if (auth.token) {
      showHAOnboarding = false;
      haBootstrapState = "idle";
      haBootstrapError = null;
      return;
    }
    if (showHAOnboarding) return;
    if (haBootstrapState === "idle") {
      haBootstrapState = "checking";
      return;
    }
    if (haBootstrapState === "checking" && !haBootstrapInFlight) {
      void bootstrapHA();
    }
  });

  $effect(() => {
    if (!auth.token) return;
    if (mode === "ha" && showHAOnboarding) return;
    if (booted) {
      live.connect(); // reopen after re-authentication
      return;
    }
    booted = true;
    void boot();
  });

  // The native apps can outlive the gateway they are a client of, so an offline
  // socket has to become a real screen rather than a stale page. The store reads
  // live.status as its trigger — an open socket is proof of reachability, so it
  // stays dormant until this fires with something else. Inert in a browser,
  // where the daemon served the page in the first place.
  $effect(() => {
    if (!isNative) return;
    reachability.observe(live.status);
  });

  async function bootstrapHA() {
    haBootstrapInFlight = true;
    haBootstrapError = null;
    try {
      const onboarding = await getOnboardingState();
      if (!onboarding.completed) {
        showHAOnboarding = true;
        haBootstrapState = "onboarding";
        return;
      }
      const result = await getOnboardingToken();
      route = "chat";
      booted = false;
      showHAOnboarding = false;
      auth.setToken(result.token);
      haBootstrapState = "idle";
    } catch (e) {
      haBootstrapError = e instanceof Error ? e.message : String(e);
      haBootstrapState = "failed";
    } finally {
      haBootstrapInFlight = false;
    }
  }

  async function boot() {
    // Open the app-wide socket once, above any page, so attention signalling
    // (toasts, red dots, nav badge) keeps working on every route. Wire toast /
    // push taps to open the relevant chat session.
    live.connect();
    // One navigator for every kind of tap — a toast, a Web Push notification, or the
    // Notification Center — because they all resolve to the same logical target.
    live.setNavigator(openTarget);
    // The Notification Center reads its history over REST and stays current from the
    // same socket, so it is live on every route rather than only while its panel is open.
    notifications.start();
    // Native push listeners. Attached on every platform's boot path (they no-op in the
    // browser) so a notification tapped while the app was terminated still routes once
    // the web layer is ready.
    startNativePush({
      onNavigate: openTarget,
      onForeground: () => void notifications.refresh(),
      // A button pressed on the notification itself. The same domain operation the
      // Notification Center performs, so a stale action is refused identically — and a
      // refusal or an unreachable daemon opens the app rather than looking like success.
      onAction: async (notificationID, actionID) => {
        const outcome = await notifications.act(notificationID, actionID);
        return outcome.status === "ok";
      },
    });
    void refreshProviderAuth();
    await refreshPushStatus();
    await refreshHealth();
    await refreshAgents();
    await refreshUpdate();
    await refreshProfileInvite();
  }

  async function refreshProviderAuth(refresh = false) {
    providerAuthLoading = true;
    providerAuthError = null;
    try {
      providerAuthStatuses = await providerStatus(refresh);
    } catch (e) {
      providerAuthError = e instanceof Error ? e.message : String(e);
    } finally {
      providerAuthLoading = false;
    }
  }

  async function refreshPushStatus() {
    try {
      pushState = await live.refreshPushStatus();
    } catch {
      pushState = "unsupported";
    }
  }

  async function enablePush() {
    pushState = "enabling";
    try {
      const result = await live.enablePush();
      pushState = result;
    } catch {
      pushState = "unsupported";
    }
  }

  // Keep the daemon indicator and update status fresh independently of the
  // chat socket: health is the source of truth for "live", and we poll for new
  // releases every 5 minutes.
  onMount(() => {
    initChrome();
    // Phones fold the nav away while the keyboard is up; see keyboard.svelte.ts.
    const offKeyboard = watchKeyboard();
    // The URL is the source of truth, so adopt whatever it already says — this is what
    // makes a deep link work on a cold start, including one opened from a notification
    // while the app was terminated.
    applyHash(window.location.hash);
    const onHashChange = () => applyHash(window.location.hash);
    window.addEventListener("hashchange", onHashChange);
    // Android's back gesture: dismiss whatever is on top first, then walk the history
    // the hash navigation builds, and only let the app exit from the default page.
    const offBack = onBackButton(() => {
      if (hireOpen) {
        hireOpen = false;
        return true;
      }
      if (moreOpen) {
        moreOpen = false;
        return true;
      }
      if (route !== "chat") {
        window.history.back();
        return true;
      }
      return false;
    });
    const healthTimer = window.setInterval(() => {
      if (auth.token) void refreshHealth();
    }, 10_000);
    const updateTimer = window.setInterval(() => {
      if (auth.token) void refreshUpdate(true);
    }, 5 * 60 * 1000);
    return () => {
      offBack();
      offKeyboard();
      window.removeEventListener("hashchange", onHashChange);
      window.clearInterval(healthTimer);
      window.clearInterval(updateTimer);
    };
  });

  async function refreshHealth() {
    try {
      health = await getHealth();
      daemonStatus = "live";
    } catch {
      daemonStatus = "offline";
    }
  }

  async function refreshAgents() {
    try {
      agents = await listAgents();
    } catch {
      // leave agents as-is
    }
  }

  async function refreshProfiles() {
    try {
      profiles = await listProfiles();
    } catch {
      profiles = [];
    }
  }

  async function refreshUpdate(silent = false) {
    // Don't let a background poll clobber an update that's mid-flight.
    if (silent && (updateState === "updating" || updateState === "restarting")) return;
    if (!silent) {
      updateState = "checking";
      updateError = null;
    }
    try {
      update = await checkUpdate();
      updateState = update.update_available ? "available" : "current";
    } catch (e) {
      update = null;
      updateState = "failed";
      updateError = e instanceof Error ? e.message : String(e);
    }
  }

  async function runUpdate() {
    if (!update) return;
    const warning = update.blocking_reason
      ? `${update.blocking_reason}\n\nForce update anyway? This restarts podiomd and may interrupt active turns.`
      : `Install ${update.latest_version}? This restarts podiomd and may interrupt active turns.`;
    if (!window.confirm(warning)) return;
    updateState = "updating";
    updateError = null;
    try {
      await applyUpdate(Boolean(update.blocking_reason));
      updateState = "restarting";
      await waitForRestart(update.latest_version);
      window.location.reload();
    } catch (e) {
      updateState = "failed";
      updateError = e instanceof Error ? e.message : String(e);
    }
  }

  async function waitForRestart(version: string) {
    for (let i = 0; i < 45; i++) {
      await new Promise((resolve) => setTimeout(resolve, 1000));
      try {
        const h = await getHealth();
        if (h.version === version) {
          health = h;
          return;
        }
      } catch {
        // daemon is probably between old and new process
      }
    }
  }

  function openChat(target: ChatTarget) {
    // The extra fields (agentName, seed) are transient composer state with no place
    // in a shareable URL, so they are held here while the hash carries the session.
    chatTarget = target;
    if (target.sessionId) {
      navigate({ kind: "chat", sessionId: target.sessionId });
      return;
    }
    navigate({ kind: "route", route: "chat" });
  }

  function openSettings(tab: SettingsTab = "providers", account: SettingsAccountTarget | null = null) {
    moreOpen = false;
    settingsFocusAccount = account;
    navigate({ kind: "settings", tab });
  }

  function openReleaseNotes() {
    releaseNotesFocusToken += 1;
    openSettings("updates");
  }

  function openRoute(next: Route) {
    moreOpen = false;
    if (next === "settings") {
      openSettings();
      return;
    }
    navigate({ kind: "route", route: next });
  }

  // navigate is the only way the app changes page.
  //
  // It assigns location.hash rather than setting `route` directly, so the URL is
  // always the source of truth: a deep link, a notification tap, the back gesture and
  // a nav click all take the same path through applyHash below. Assigning the hash
  // (rather than using an <a href>) also keeps this correct under a Home Assistant
  // ingress sub-path, where an injected <base href> would otherwise resolve a bare
  // fragment against the base instead of the current document.
  function navigate(target: Target) {
    const next = formatHash(target);
    if (window.location.hash === next) {
      // Same destination: no history entry, but still re-apply so a repeat tap on a
      // notification re-focuses the resource.
      applyHash(next);
      return;
    }
    window.location.hash = next;
  }

  // applyHash projects the URL onto the shell's state. An unrecognised hash falls
  // back to Chat rather than rendering nothing, so a stale or hand-edited link is
  // harmless.
  function applyHash(hash: string) {
    const target = parseHash(hash) ?? { kind: "route", route: "chat" };
    moreOpen = false;
    switch (target.kind) {
      case "route":
        route = target.route;
        break;
      case "chat":
        // openChat may have just parked transient state for this session (a roadmap
        // start hands over the task prompt), and the hash cannot carry it. Keep it
        // rather than overwriting it with what the URL alone knows.
        chatTarget = carryChatTarget(chatTarget, target.sessionId, target.permission);
        route = "chat";
        break;
      case "goal":
        goalTarget = target.goalId;
        goalFocus = target.focus ?? null;
        route = "goals";
        break;
      case "schedule":
        route = "schedules";
        break;
      case "task":
        route = "roadmap";
        break;
      case "settings":
        settingsFocusTab = (target.tab as SettingsTab) ?? settingsFocusTab;
        settingsFocusToken += 1;
        route = "settings";
        break;
    }
  }

  // openTarget is what a notification tap, a toast tap or the service worker calls.
  export function openTarget(target: Target) {
    navigate(target);
  }

  function handleWindowKeydown(event: KeyboardEvent) {
    if (moreOpen && event.key === "Escape") moreOpen = false;
  }

  function pushReminderLabel(): string {
    switch (pushState) {
      case "enabling":
        return "Notifications enabling…";
      case "denied":
        return "Notifications blocked";
      case "unsupported":
        return "Push unavailable";
      default:
        return "Set up notifications";
    }
  }

  function openHire() {
    hireName = "";
    hireProvider = DEFAULT_PROVIDER;
    hireProfile = "";
    hirePermission = "approve";
    hireError = null;
    profileCreateOpen = false;
    profileName = "";
    profilePath = "";
    hireOpen = true;
    void refreshProfiles();
  }

  async function submitHire() {
    hireError = null;
    try {
      const agent = await hireAgent({
        name: hireName.trim(),
        provider: hireProvider,
        profile: hireProfile,
        model: "",
        effort: "high",
        permission_mode: hirePermission,
      });
      agents = [agent, ...agents.filter((a) => a.Name !== agent.Name)];
      hireOpen = false;
      openSettings("agents");
    } catch (e) {
      hireError = e instanceof Error ? e.message : String(e);
    }
  }

  async function submitProfileFromHire() {
    profileSaving = true;
    hireError = null;
    try {
      const created = await createProfile({
        name: profileName.trim(),
        provider: hireProvider,
        config_dir: "",
        home_dir: "",
        [providerMeta(hireProvider).profileDir.bodyKey]: profilePath.trim(),
      });
      profiles = [created, ...profiles.filter((p) => p.Name !== created.Name)];
      hireProfile = created.Name;
      profileCreateOpen = false;
      profileName = "";
      profilePath = "";
      void refreshProviderAuth(true);
    } catch (e) {
      hireError = e instanceof Error ? e.message : String(e);
    } finally {
      profileSaving = false;
    }
  }

  const hireProfileOptions = $derived(profiles.filter((p) => p.Provider === hireProvider));
  const visibleNav = $derived(mode === "ha" ? [...NAV, TERMINAL_NAV] : NAV);
  const mobileMoreNav = $derived([
    ...NAV.filter((item) => item.key === "projects" || item.key === "skills"),
    SETTINGS_NAV,
    ...(mode === "ha" ? [TERMINAL_NAV] : []),
  ]);
  const mobileMoreActive = $derived(moreOpen || mobileMoreNav.some((item) => item.key === route));
  // Two tiers on purpose: a count for what actually needs the user, a plain dot for
  // "there is something new". The daemon reports unread and attention separately so
  // the number can keep meaning something — routine run and progress activity would
  // otherwise leave it permanently non-zero.
  const unreadOnly = $derived(notifications.attention === 0 && notifications.unread > 0);

  const daemonLabel = $derived(daemonStatus === "live" ? "podiomd live" : `podiomd ${daemonStatus}`);
  const daemonAddr = $derived(health ? `${health.version} · ${health.commit}` : "127.0.0.1:8787");
  // Only surface the update box when there's actually something to act on.
  const showUpdateBox = $derived(
    updateState === "available" ||
      updateState === "updating" ||
      updateState === "restarting" ||
      Boolean(update?.blocking_reason),
  );

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
</script>

<svelte:window onkeydown={handleWindowKeydown} />

{#if mode === "ha" && !auth.token && !showHAOnboarding}
  <main class="ha-bootstrap">
    <div class="bootstrap-mark">
      <svg width="34" height="34" viewBox="0 0 48 48" aria-hidden="true">
        <path d="M8 31 Q18 6 30 16" fill="none" stroke="#2F6E60" stroke-width="3.4" stroke-linecap="round" />
        <circle cx="30" cy="16" r="4.6" fill="#2F6E60" />
        <circle cx="36" cy="23" r="2.9" fill="#2F6E60" opacity=".72" />
        <circle cx="41" cy="29" r="1.7" fill="#2F6E60" opacity=".45" />
      </svg>
    </div>
    <div class="bootstrap-title">Opening Podiom</div>
    <div class="bootstrap-status mono">
      {#if haBootstrapState === "failed"}
        {haBootstrapError ?? "could not check Home Assistant setup"}
      {:else}
        checking Home Assistant setup...
      {/if}
    </div>
    {#if haBootstrapState === "failed"}
      <button class="bootstrap-retry" onclick={() => { haBootstrapError = null; haBootstrapState = "checking"; }}>Retry</button>
    {/if}
  </main>
{:else if mode === "ha" && showHAOnboarding}
  <HAOnboarding onUnlocked={() => { showHAOnboarding = false; route = "chat"; booted = false; }} />
{:else if !auth.token}
  <TokenGate />
{:else}
<div class="app-root" class:kbd-open={keyboard.open}>
  <!-- ============ SIDEBAR ============ -->
  <aside class="sidebar">
    <div class="brand">
      <div class="brand-logo">
        <svg width="20" height="20" viewBox="0 0 48 48" aria-hidden="true">
          <path d="M8 31 Q18 6 30 16" fill="none" stroke="#fff" stroke-width="3.8" stroke-linecap="round" />
          <circle cx="30" cy="16" r="4.8" fill="#fff" />
          <circle cx="36" cy="23" r="2.9" fill="#fff" opacity=".72" />
          <circle cx="41" cy="29" r="1.7" fill="#fff" opacity=".45" />
        </svg>
      </div>
      <div>
        <div class="brand-name">Podiom</div>
        <div class="brand-tag mono">conductor</div>
      </div>
    </div>

    <nav class="nav-links" aria-label="Primary navigation">
      {#each visibleNav as item}
        <button
          class="nav-link"
          class:mobile-overflow={!MOBILE_PRIMARY_ROUTES.has(item.key)}
          class:active={route === item.key}
          aria-label={item.key === "schedules" && live.scheduleAttention.size > 0
            ? "Schedules, a schedule needs your attention"
            : undefined}
          onclick={() => openRoute(item.key)}>
          <svg
            width="18"
            height="18"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round">{@html item.icon}</svg
          >
          {item.label}
          {#if item.key === "chat" && live.attention.size > 0}
            <span class="nav-badge" title="A session needs your attention">{live.attention.size}</span>
          {/if}
          {#if item.key === "goals" && live.goalAttention.size > 0}
            <span class="nav-badge" title="A goal needs your attention">{live.goalAttention.size}</span>
          {/if}
          {#if item.key === "schedules" && live.scheduleAttention.size > 0}
            <span class="nav-attention-dot" title="A schedule needs your attention" aria-hidden="true"></span>
          {/if}
        </button>
      {/each}
      <button
        class="nav-link mobile-more-toggle"
        class:active={mobileMoreActive}
        aria-haspopup="dialog"
        aria-expanded={moreOpen}
        aria-controls="mobile-more-navigation"
        aria-label={notifications.unread > 0 ? "More, unread notifications" : undefined}
        onclick={() => (moreOpen = !moreOpen)}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">{@html MORE_ICON}</svg>
        More
        <!-- The bell lives in the sidebar footer, which the phone layout hides, so this
             is the only place unread notifications can show up on a phone. Unlike the
             bell it lights for anything unread: the count is inside the sheet. -->
        {#if notifications.unread > 0}
          <span class="more-dot" aria-hidden="true"></span>
        {/if}
      </button>
    </nav>

    <div class="nav-foot">
      <button class="nav-link settings-link" class:active={route === "settings"} onclick={() => openSettings()}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{@html SETTINGS_ICON}</svg>
        Settings
      </button>
      <SidebarUsage
        snapshots={live.usage}
        authStatuses={providerAuthStatuses}
        onOpenSignIn={(provider, profile) => openSettings("providers", { provider, profile })} />
      <div class="daemon">
        <span class="daemon-dot" class:live={daemonStatus === "live"}></span>
        <div class="daemon-text mono">
          {daemonLabel}<br /><span class="daemon-addr">{daemonAddr}</span>
        </div>
      </div>
      {#if showUpdateBox}
        <div class="update-box">
          <div class="update-line mono">
            {#if updateState === "updating"}
              updating
            {:else if updateState === "restarting"}
              restarting
            {:else}
              update {update?.latest_version}
            {/if}
          </div>
          {#if update?.blocking_reason}
            <div class="update-note">{update.blocking_reason}</div>
          {:else if updateError}
            <div class="update-note">{updateError}</div>
          {/if}
          <div class="update-actions">
            <button class="update-btn primary" disabled={updateState === "updating" || updateState === "restarting"} onclick={runUpdate}>Update</button>
            {#if updateState === "available" && update}
              <button class="update-btn" onclick={openReleaseNotes}>Release notes</button>
            {/if}
          </div>
        </div>
      {/if}
      {#if profileInvite}
        <div class="profile-invite">
          <div class="profile-invite-title">Help your agents get to know you</div>
          <div class="profile-invite-note">A 2-minute interview teaches every agent who you are and how you like to work.</div>
          <div class="profile-invite-actions">
            <button class="update-btn primary" onclick={() => openSettings("about-you")}>Get started</button>
            <button class="update-btn" onclick={dismissProfileInvite}>Dismiss</button>
          </div>
        </div>
      {/if}
      {#if pushState !== "enabled"}
        <button
          class="push-reminder"
          class:warn={pushState === "denied" || pushState === "unsupported"}
          title="Open notification settings"
          onclick={() => openSettings("notifications")}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9" /><path d="M13.73 21a2 2 0 0 1-3.46 0" />
          </svg>
          <span>{pushReminderLabel()}</span>
        </button>
      {/if}
      <button
        class="bell-btn"
        class:has-unread={notifications.unread > 0}
        title="Notifications"
        aria-label={notifications.attention > 0
          ? `Notifications, ${notifications.attention} needing attention`
          : unreadOnly
            ? `Notifications, ${notifications.unread} unread`
            : "Notifications"}
        onclick={() => notifications.toggle()}>
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{@html BELL_ICON}</svg>
        <span>Notifications</span>
        {#if notifications.attention > 0}
          <span class="bell-count mono">{notifications.attention > 99 ? "99+" : notifications.attention}</span>
        {:else if unreadOnly}
          <span class="bell-dot" aria-hidden="true"></span>
        {/if}
      </button>
      <button class="hire-btn" onclick={openHire}><span class="hire-plus">+</span> Hire agent</button>
    </div>
  </aside>

  {#if moreOpen}
    <div class="mobile-more-layer">
      <button class="mobile-more-backdrop" aria-label="Close more navigation" onclick={() => (moreOpen = false)}></button>
      <dialog id="mobile-more-navigation" class="mobile-more-sheet" open aria-modal="true" aria-label="More navigation">
        <div class="mobile-more-head">
          <span>More</span>
          <button class="mobile-more-close" aria-label="Close more navigation" onclick={() => (moreOpen = false)}>×</button>
        </div>
        <nav class="mobile-more-links" aria-label="More navigation">
          <!-- Not part of mobileMoreNav: that list routes, and the Notification Center
               is a panel rather than a destination. -->
          <button
            class="mobile-more-link"
            onclick={() => {
              moreOpen = false;
              notifications.toggle();
            }}>
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{@html BELL_ICON}</svg>
            Notifications
            {#if notifications.attention > 0}
              <span class="bell-count mono">{notifications.attention > 99 ? "99+" : notifications.attention}</span>
            {:else if notifications.unread > 0}
              <span class="bell-dot" aria-hidden="true"></span>
            {/if}
          </button>
          {#each mobileMoreNav as item}
            <button class="mobile-more-link" class:active={route === item.key} onclick={() => openRoute(item.key)}>
              <svg
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round">{@html item.icon}</svg
              >
              {item.label}
            </button>
          {/each}
        </nav>
      </dialog>
    </div>
  {/if}

  <!-- ============ MAIN ============ -->
  <!-- flush-top hands the status-bar inset to the route instead of applying it
       here, for routes whose own top element paints a different background and
       should reach the top of the screen (Chat's agent header). -->
  <div class="main" class:flush-top={route === "chat"}>
    {#if route === "chat"}
      <Chat {agents} target={chatTarget} onConsumeTarget={() => (chatTarget = null)} onOpenGoal={(id) => navigate({ kind: "goal", goalId: id })} />
    {:else if route === "roadmap"}
      <Roadmap {agents} onOpenChat={openChat} onOpenGoal={(id) => navigate({ kind: "goal", goalId: id })} />
    {:else if route === "goals"}
      <Goals
        {agents}
        target={goalTarget}
        focus={goalFocus}
        onConsumeTarget={() => {
          goalTarget = null;
          goalFocus = null;
        }}
        onOpenChat={openChat} />
    {:else if route === "projects"}
      <Projects {agents} onOpenChat={openChat} />
    {:else if route === "schedules"}
      <Schedules {agents} onOpenChat={openChat} onOpenGoal={(id) => navigate({ kind: "goal", goalId: id })} />
    {:else if route === "skills"}
      <Skills />
    {:else if route === "terminal" && mode === "ha"}
      <Terminal />
    {:else if route === "settings"}
      <Settings
        {health}
        {update}
        {updateState}
        {updateError}
        {releaseNotesFocusToken}
        {settingsFocusTab}
        {settingsFocusToken}
        {settingsFocusAccount}
        authStatuses={providerAuthStatuses}
        authLoading={providerAuthLoading}
        authError={providerAuthError}
        {pushState}
        {agents}
        onCheckUpdate={() => refreshUpdate()}
        onRunUpdate={runUpdate}
        onEnablePush={enablePush}
        onHireAgent={openHire}
        onOpenChat={openChat}
        onAgentsChanged={refreshAgents}
        onRefreshAuth={refreshProviderAuth}
        onUserProfileSaved={() => (profileInvite = false)} />
    {/if}
  </div>

  <!-- ============ HIRE MODAL ============ -->
  {#if hireOpen}
    <div class="modal-backdrop" role="presentation" onclick={() => (hireOpen = false)}>
      <div class="modal-card hire-modal" role="dialog" aria-modal="true" aria-label="Hire agent" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
        <div class="modal-head">
          <div class="modal-title">Hire an agent</div>
          <div class="modal-sub">Give your new colleague a name and a backend. They'll get a workspace, a SOUL.md, and a seat on the bench.</div>
        </div>
        <div class="modal-body">
          {#if hireError}<div class="error-banner" style="margin-bottom:14px">{hireError}</div>{/if}
          <div class="label-mono" style="margin-bottom:8px">name</div>
          <input class="field-input" bind:value={hireName} placeholder="e.g. atlas" />

          <div class="label-mono" style="margin:18px 0 8px">backend</div>
          <div style="display:flex;gap:9px">
            {#each PROVIDERS as p (p.id)}
              <button class="provider-choice" style={seg(hireProvider === p.id)} onclick={() => { hireProvider = p.id; hireProfile = ""; profileCreateOpen = false; }}>
                <ProviderLogo provider={p.id} />{p.label}
              </button>
            {/each}
          </div>

          <div class="label-mono" style="margin:18px 0 8px">profile</div>
          <div class="prof-chips">
            <button style={chip(hireProfile === "")} onclick={() => (hireProfile = "")}>default · global login</button>
            {#each hireProfileOptions as p}
              <button style={chip(hireProfile === p.Name)} onclick={() => (hireProfile = p.Name)}>{p.Name}</button>
            {/each}
            <button class="chip-new" type="button" onclick={() => { profileCreateOpen = !profileCreateOpen; profileName = ""; profilePath = ""; }}>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M12 5v14" /><path d="M5 12h14" /></svg>New
            </button>
          </div>

          {#if profileCreateOpen}
            <div class="inline-create">
              <div class="np-title">new profile · uses selected provider</div>
              <input class="field-input" bind:value={profileName} placeholder="profile name" />
              <input class="field-input mono" bind:value={profilePath} placeholder={providerMeta(hireProvider).profileDir.placeholder} />
              <div class="np-actions">
                <button class="np-create" disabled={profileSaving || !profileName.trim()} onclick={submitProfileFromHire}>
                  {profileSaving ? "Saving…" : "Create & select"}
                </button>
                <button class="np-cancel" type="button" onclick={() => (profileCreateOpen = false)}>Cancel</button>
              </div>
            </div>
          {/if}

          <div class="label-mono" style="margin:18px 0 8px">permission mode</div>
          <div style="display:flex;gap:9px">
            <button style={seg(hirePermission === "approve")} onclick={() => (hirePermission = "approve")}>approve · safe</button>
            <button style={seg(hirePermission === "auto")} onclick={() => (hirePermission = "auto")}>auto · edits</button>
            <button style={seg(hirePermission === "yolo")} onclick={() => (hirePermission = "yolo")}>yolo · full access</button>
          </div>

          <button class="modal-cta" disabled={!hireName.trim()} onclick={submitHire}>Create agent</button>
        </div>
      </div>
    </div>
  {/if}

  <!-- ============ TOASTS (global, top-right) ============ -->
  <div class="toasts" aria-live="polite">
    {#each live.toasts as t (t.id)}
      <button
        class="toast"
        class:permission={t.urgent}
        onclick={() => {
          navigate(t.target);
          // Tapping through counts as having seen it, so the Center and the badge agree
          // with what the user just looked at.
          if (t.notificationId) void notifications.markRead(t.notificationId);
          live.dismissToast(t.id);
        }}>
        <span class="toast-dot"></span>
        <span class="toast-body">
          <span class="toast-title">{t.title}</span>
          <span class="toast-sub">{t.body}</span>
        </span>
        <span
          class="toast-x"
          role="button"
          tabindex="0"
          aria-label="Dismiss"
          onclick={(e) => {
            e.stopPropagation();
            live.dismissToast(t.id);
          }}
          onkeydown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.stopPropagation();
              live.dismissToast(t.id);
            }
          }}>✕</span>
      </button>
    {/each}
  </div>
  <WorkspaceFileViewer />
</div>
<!-- Outside the toast stack on purpose: .toasts is pointer-events: none so toasts do
     not swallow clicks meant for the app behind them, and the panel would inherit
     that and render perfectly while ignoring every click. -->
<NotificationCenter onNavigate={openTarget} />
{#if reachability.visible}
  <OfflineGate />
{/if}
{/if}

<style>
  .ha-bootstrap {
    min-height: 100vh;
    background: linear-gradient(180deg, #f8f3ea 0%, #efe5d6 100%);
    color: #2b2520;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 14px;
    padding: 32px;
    text-align: center;
  }

  .bootstrap-mark {
    width: 62px;
    height: 62px;
    border-radius: 18px;
    background: #e3f1ec;
    display: flex;
    align-items: center;
    justify-content: center;
    box-shadow: 0 18px 34px -22px rgba(47, 110, 96, 0.75);
  }

  .bootstrap-title {
    margin-top: 4px;
    color: #2b2520;
    font: 800 28px/1.1 "Hanken Grotesk", system-ui, sans-serif;
  }

  .bootstrap-status {
    max-width: min(440px, 100%);
    color: #8a7d6a;
    font-size: 12px;
    overflow-wrap: anywhere;
  }

  .bootstrap-retry {
    margin-top: 6px;
    border: 1px solid #bfe0d6;
    border-radius: 10px;
    background: #3f8f7e;
    color: #fffaf2;
    cursor: pointer;
    padding: 10px 18px;
    font: 700 14px "Hanken Grotesk", system-ui, sans-serif;
  }

  .bootstrap-retry:hover {
    background: #357b6c;
  }

  .sidebar {
    width: 236px;
    flex: none;
    background: var(--surface);
    border-right: 1px solid var(--line);
    display: flex;
    flex-direction: column;
    padding: 20px 16px;
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 4px 8px 18px;
  }

  .brand-logo {
    width: 34px;
    height: 34px;
    border-radius: 11px;
    background: linear-gradient(150deg, #46a08c, #2f6e60);
    display: flex;
    align-items: center;
    justify-content: center;
    box-shadow: 0 6px 14px -6px rgba(47, 110, 96, 0.6);
  }

  .brand-name {
    font: 800 18px "Hanken Grotesk";
    letter-spacing: -0.02em;
    line-height: 1;
  }

  .brand-tag {
    font-size: 10px;
    font-weight: 500;
    color: var(--faint);
    letter-spacing: 0.08em;
  }

  .nav-links {
    display: flex;
    flex-direction: column;
    gap: 3px;
  }

  .mobile-more-toggle,
  .mobile-more-layer {
    display: none;
  }

  .nav-link {
    display: flex;
    align-items: center;
    gap: 11px;
    border: none;
    cursor: pointer;
    text-align: left;
    padding: 10px 12px;
    border-radius: 12px;
    font: 600 14px "Hanken Grotesk";
    background: transparent;
    color: var(--muted);
  }

  .nav-link:hover {
    background: #f6efe6;
  }

  .nav-link.active {
    background: #e3f1ec;
    color: var(--teal-deep);
  }

  .nav-badge {
    margin-left: auto;
    min-width: 18px;
    height: 18px;
    padding: 0 5px;
    border-radius: 999px;
    background: #d64528;
    color: #fff;
    font: 700 11px "JetBrains Mono", monospace;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    box-shadow: 0 0 0 2px rgba(214, 69, 40, 0.2);
  }

  .nav-attention-dot {
    margin-left: auto;
    width: 8px;
    height: 8px;
    flex: none;
    border-radius: 50%;
    background: #d64528;
    box-shadow: 0 0 0 3px rgba(214, 69, 40, 0.18);
  }

  .push-reminder {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    border: 1px solid var(--line-3);
    background: var(--surface-3);
    cursor: pointer;
    padding: 9px 10px;
    border-radius: 12px;
    font: 600 12px "Hanken Grotesk";
    color: var(--muted);
  }

  .push-reminder:hover {
    background: #f6efe6;
  }

  .push-reminder.warn {
    border-color: #ecd9ae;
    background: #fbf1dd;
    color: #9a6e1e;
  }

  /* Global toast stack, top-right, above modals (z 40). The inset clears the
     status bar / notch in the native apps and is 0 in a browser. */
  .toasts {
    position: fixed;
    top: calc(18px + env(safe-area-inset-top));
    right: 18px;
    z-index: 60;
    display: flex;
    flex-direction: column;
    gap: 10px;
    max-width: min(360px, 92vw);
    pointer-events: none;
  }

  .toast {
    pointer-events: auto;
    display: flex;
    align-items: flex-start;
    gap: 11px;
    width: 100%;
    text-align: left;
    cursor: pointer;
    border: 1px solid var(--line);
    background: var(--surface);
    border-radius: 13px;
    padding: 13px 14px;
    box-shadow: 0 14px 34px -14px rgba(43, 37, 32, 0.4);
    animation: popIn 0.18s ease;
  }

  .toast-dot {
    flex: none;
    margin-top: 4px;
    width: 9px;
    height: 9px;
    border-radius: 999px;
    background: var(--gold);
    box-shadow: 0 0 0 3px rgba(154, 110, 30, 0.16);
  }

  .toast.permission .toast-dot {
    background: #d64528;
    box-shadow: 0 0 0 3px rgba(214, 69, 40, 0.16);
  }

  .toast-body {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .toast-title {
    font: 700 13.5px "Hanken Grotesk";
    color: var(--ink);
  }

  .toast-sub {
    font: 400 12px/1.4 "Hanken Grotesk";
    color: var(--muted);
  }

  .toast-x {
    margin-left: auto;
    flex: none;
    color: var(--faint);
    font-size: 12px;
    line-height: 1;
    padding: 2px 4px;
    border-radius: 6px;
  }

  .toast-x:hover {
    background: #f2ebe1;
    color: var(--muted);
  }

  .nav-foot {
    margin-top: auto;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .daemon {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 12px;
    border-radius: 12px;
    background: var(--surface-3);
    border: 1px solid var(--line-3);
  }

  .daemon-dot {
    width: 8px;
    height: 8px;
    border-radius: 99px;
    flex: none;
    background: #c0492a;
    box-shadow: 0 0 0 3px rgba(192, 73, 42, 0.18);
  }

  .daemon-dot.live {
    background: #4f9e78;
    box-shadow: 0 0 0 3px rgba(79, 158, 120, 0.18);
  }

  .daemon-text {
    font-size: 11px;
    font-weight: 500;
    color: var(--muted);
    line-height: 1.3;
  }

  .daemon-addr {
    color: var(--faint);
  }

  .update-box {
    padding: 10px 12px;
    border-radius: 12px;
    background: var(--surface-3);
    border: 1px solid var(--line-3);
  }

  .update-line {
    font-size: 10.5px;
    color: var(--muted);
  }

  .update-note {
    margin-top: 5px;
    color: var(--faint);
    font: 400 11px/1.35 "Hanken Grotesk";
  }

  .update-actions {
    display: flex;
    gap: 7px;
    margin-top: 8px;
  }

  .update-btn {
    flex: 1;
    border: 1px solid var(--line-3);
    background: #fff;
    border-radius: 9px;
    padding: 7px 8px;
    cursor: pointer;
    font: 700 11px "Hanken Grotesk";
    color: var(--muted);
  }

  .update-btn.primary {
    background: var(--teal);
    border-color: var(--teal);
    color: #fff;
  }

  .profile-invite {
    padding: 10px 12px;
    border-radius: 12px;
    background: var(--surface-3);
    border: 1px solid var(--line-3);
  }

  .profile-invite-title {
    font: 700 12px "Hanken Grotesk";
    color: var(--muted);
  }

  .profile-invite-note {
    margin-top: 5px;
    color: var(--faint);
    font: 400 11px/1.35 "Hanken Grotesk";
  }

  .profile-invite-actions {
    display: flex;
    gap: 7px;
    margin-top: 8px;
  }

  .bell-btn {
    position: relative;
    display: flex;
    align-items: center;
    gap: 0.45rem;
    width: 100%;
    border: 1px solid var(--line, #e7e2da);
    background: var(--surface, #fff);
    border-radius: 9px;
    padding: 0.45rem 0.6rem;
    font-size: 0.8rem;
    color: var(--ink-soft, #6b625a);
    cursor: pointer;
  }

  .bell-btn:hover {
    border-color: var(--teal-deep, #2f7f6f);
    color: var(--ink, #2b2724);
  }

  .bell-btn.has-unread {
    color: var(--ink, #2b2724);
    border-color: color-mix(in srgb, var(--teal-deep, #2f7f6f) 45%, var(--line, #e7e2da));
  }

  .bell-count {
    margin-left: auto;
    min-width: 1.15rem;
    text-align: center;
    font-size: 0.68rem;
    padding: 0.05rem 0.3rem;
    border-radius: 999px;
    background: var(--teal-deep, #2f7f6f);
    color: #fff;
  }

  /* Shown instead of the count when there is something unread but nothing urgent.
     Same 7px teal mark the panel puts against an unread row, so the bell and the list
     say the same thing the same way. */
  .bell-dot {
    margin-left: auto;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--teal-deep, #2f7f6f);
  }

  .hire-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    border: 1.5px dashed #decfbe;
    background: rgba(250, 246, 240, 0.6);
    cursor: pointer;
    padding: 10px;
    border-radius: 12px;
    font: 600 13px "Hanken Grotesk";
    color: #a8825e;
  }

  .hire-plus {
    font-size: 16px;
    line-height: 1;
  }

  .main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }

  /* Hire modal */
  .hire-modal {
    width: 460px;
    max-width: 92vw;
  }

  .modal-head {
    padding: 26px 26px 0;
  }

  .modal-title {
    font: 800 22px "Hanken Grotesk";
    letter-spacing: -0.01em;
  }

  .modal-sub {
    font: 400 13.5px/1.5 "Hanken Grotesk";
    color: var(--muted-2);
    margin-top: 5px;
  }

  .modal-body {
    padding: 22px 26px 26px;
  }

  .modal-cta {
    width: 100%;
    margin-top: 24px;
    border: none;
    border-radius: 13px;
    padding: 13px;
    background: var(--teal);
    color: #fff;
    font: 700 15px "Hanken Grotesk";
    cursor: pointer;
    box-shadow: 0 10px 22px -8px rgba(63, 143, 126, 0.7);
  }

  .prof-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    align-items: center;
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

  .inline-create {
    margin-top: 10px;
    padding: 12px;
    border: 1px solid var(--line-3);
    border-radius: 12px;
    background: var(--surface-3);
  }

  .inline-create .field-input {
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

  @media (max-width: 768px) {
    /* The keyboard takes roughly half the screen, and the nav is not what the
       user is reaching for mid-sentence. Hand its 72px back to the page for as
       long as they are typing — on every route, not just Chat. */
    .app-root.kbd-open .sidebar {
      display: none;
    }

    .app-root.kbd-open .main,
    .app-root.kbd-open .main.flush-top {
      padding-bottom: 0;
    }

    .sidebar {
      position: fixed;
      left: 0;
      right: 0;
      bottom: 0;
      z-index: 50;
      width: auto;
      height: 72px;
      padding: 8px 10px calc(8px + env(safe-area-inset-bottom));
      border-right: none;
      border-top: 1px solid var(--line);
      box-shadow: 0 -14px 34px -28px rgba(43, 37, 32, 0.42);
    }

    .brand,
    .nav-foot {
      display: none;
    }

    .nav-links {
      flex: 1;
      flex-direction: row;
      gap: 4px;
      min-width: 0;
    }

    .nav-link.mobile-overflow {
      display: none;
    }

    .mobile-more-toggle {
      display: flex;
    }

    .nav-link {
      flex: 1;
      min-width: 0;
      flex-direction: column;
      justify-content: center;
      gap: 4px;
      padding: 7px 4px;
      border-radius: 11px;
      text-align: center;
      font-size: 11px;
      line-height: 1.1;
    }

    .nav-link svg {
      width: 18px;
      height: 18px;
    }

    .nav-links .nav-link {
      position: relative;
    }

    .nav-badge {
      position: absolute;
      top: 2px;
      left: calc(50% + 5px);
      min-width: 16px;
      height: 16px;
      padding: 0 4px;
      font-size: 9px;
    }

    .nav-attention-dot {
      position: absolute;
      top: 4px;
      left: calc(50% + 7px);
      margin-left: 0;
      box-shadow: 0 0 0 2px var(--surface);
    }

    /* Anchored like .nav-badge, since it marks the same corner of the same button. */
    .more-dot {
      position: absolute;
      top: 4px;
      left: calc(50% + 7px);
      width: 8px;
      height: 8px;
      border-radius: 50%;
      background: var(--teal-deep);
      box-shadow: 0 0 0 2px var(--surface);
    }

    .mobile-more-layer {
      display: block;
      position: fixed;
      inset: 0 0 calc(72px + env(safe-area-inset-bottom)) 0;
      z-index: 49;
    }

    .mobile-more-backdrop {
      position: absolute;
      inset: 0;
      width: 100%;
      border: none;
      padding: 0;
      background: rgba(43, 37, 32, 0.24);
      backdrop-filter: blur(1px);
    }

    .mobile-more-sheet {
      position: absolute;
      right: 12px;
      bottom: 12px;
      left: 12px;
      z-index: 1;
      width: min(420px, calc(100% - 24px));
      margin: 0 auto;
      padding: 14px;
      border: 1px solid var(--line);
      border-radius: 20px;
      background: var(--surface);
      box-shadow: 0 24px 60px -22px rgba(43, 37, 32, 0.58);
      animation: popIn 0.18s ease;
    }

    .mobile-more-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 2px 10px 6px;
      color: var(--ink);
      font: 700 16px "Hanken Grotesk";
    }

    .mobile-more-close {
      display: grid;
      width: 32px;
      height: 32px;
      place-items: center;
      border: none;
      border-radius: 10px;
      background: var(--surface-3);
      color: var(--muted);
      font-size: 20px;
      line-height: 1;
    }

    .mobile-more-links {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 8px;
    }

    .mobile-more-link {
      display: flex;
      min-width: 0;
      min-height: 54px;
      align-items: center;
      gap: 10px;
      border: 1px solid var(--line-3);
      border-radius: 13px;
      padding: 10px 12px;
      background: var(--surface-3);
      color: var(--muted);
      text-align: left;
      font: 600 13px "Hanken Grotesk";
    }

    .mobile-more-link svg {
      flex: none;
    }

    .mobile-more-link.active {
      border-color: #bfe0d6;
      background: #e3f1ec;
      color: var(--teal-deep);
    }

    .main {
      width: 100%;
      min-height: 0;
      /* Clear the status bar / notch here rather than per page: the WebView is
         drawn edge to edge in the native apps, and only five of the routes use
         the shared .page wrapper — Chat, Roadmap and Settings lay themselves
         out and would otherwise start under the clock. Zero in a browser. */
      padding-top: env(safe-area-inset-top);
      padding-bottom: calc(72px + env(safe-area-inset-bottom));
    }

    /* The route paints the strip itself — see the class comment in the markup. */
    .main.flush-top {
      padding-top: 0;
      /* The fixed mobile nav is already 72px tall including its safe-area
         padding (global border-box sizing). Reserving the inset again leaves
         an empty strip between Chat's composer and the nav. */
      padding-bottom: 72px;
    }
  }
</style>
