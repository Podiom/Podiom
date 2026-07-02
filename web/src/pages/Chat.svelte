<script lang="ts">
  import { onMount, tick } from "svelte";
  import { deleteSession, getSession, listProfiles, listProjects } from "../lib/api";
  import { live } from "../lib/live.svelte";
  import { renderMarkdown } from "../lib/markdown";
  import ConfirmModal from "../lib/ConfirmModal.svelte";
  import UsageChip from "../lib/UsageChip.svelte";
  import {
    agentGradient,
    avatarStyle,
    initial,
    modeChip,
    originLabel,
    originStyle,
    providerChip,
  } from "../lib/theme";
  import type {
    Agent,
    ClientMessage,
    Message,
    PermissionMode,
    PermissionRequest,
    Provider,
    ProfileInfo,
    Project,
    ServerMessage,
    Session,
    SessionOrigin,
    TurnState,
    UserInputQuestion,
    UserInputRequest,
  } from "../lib/types";

  interface ChatTarget {
    sessionId?: string;
    agentName?: string;
    seed?: string;
  }

  type ApprovalRisk = "safe" | "caution" | "danger";
  type ApprovalStatus = "pending" | "approved" | "denied" | "expired" | "cleared";

  interface ApprovalRecord {
    request: PermissionRequest;
    status: ApprovalStatus;
    risk: ApprovalRisk;
    note?: string;
    at: number;
  }

  let {
    agents = [],
    target = null,
    onConsumeTarget = () => {},
  }: {
    agents?: Agent[];
    target?: ChatTarget | null;
    onConsumeTarget?: () => void;
  } = $props();

  // Connection + session-list state is owned by the shared live store so it
  // survives navigating away from the chat page (attention signalling must keep
  // working everywhere). This page reads it reactively and drives its own
  // per-session view state locally.
  const status = $derived(live.status);
  const sessions = $derived(live.sessions);
  const activeTurns = $derived(live.activeTurns);
  let activeSession = $state<Session | null>(null);
  let projectName = $state<string>("");

  // Session delete confirmation.
  let pendingDelete = $state<Session | null>(null);
  let deleteBusy = $state(false);
  let deleteError = $state<string | null>(null);
  let messages = $state<Message[]>([]);
  let pendingAssistant = $state("");
  let pendingPermission = $state<PermissionRequest | null>(null);
  let approvalHistoryBySession = $state<Record<string, ApprovalRecord[]>>({});
  let approvalDockOpen = $state(false);
  let expandedApprovalText = $state<Record<string, boolean>>({});
  let denyingPermissionID = $state<string | null>(null);
  let denyText = $state("");
  let pendingUserInput = $state<UserInputRequest | null>(null);
  let userInputAnswers = $state<Record<string, string[]>>({});
  let permissionRemaining = $state(0);
  let messageText = $state("");
  let selectedAgent = $state("");
  let originFilter = $state<SessionOrigin | "all">("all");
  let agentFilter = $state("all");
  let projectFilter = $state("all");
  let projects = $state<Project[]>([]);
  let profiles = $state<ProfileInfo[]>([]);
  let draftProvider = $state<Provider | "">("");
  let draftProfile = $state("");
  let draftModel = $state("");
  let draftEffort = $state("");
  let draftPermissionMode = $state<PermissionMode | "">("");
  let draftProjectID = $state("");
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let sending = $state(false);
  let unsubscribe: (() => void) | undefined;
  let countdown: number | undefined;
  let pendingSeed: string | null = null;
  let msgsEl: HTMLDivElement | null = null;
  // stick = auto-follow the transcript to the bottom. Turns off when the user
  // scrolls up to read history, back on when they return near the bottom.
  let stick = true;
  const LAST_SESSION_KEY = "podium:last-chat-session";

  // Layout / UI state.
  let sessOpen = $state(true);
  let ctxOpen = $state(false);
  let isPhone = $state(false);
  let openDropdown = $state<string | null>(null);
  let newSessionOpen = $state(false);

  const SLASH_CMDS = [
    { cmd: "/model", desc: "set the model for this session" },
    { cmd: "/effort", desc: "low · medium · high · xhigh · max" },
    { cmd: "/profile", desc: "switch auth context — replays history" },
    { cmd: "/permission", desc: "approve or yolo for this session" },
    { cmd: "/name", desc: "rename the session" },
    { cmd: "/help", desc: "list every command" },
  ];

  const EFFORTS = ["low", "medium", "high", "xhigh", "max"];

  const APPROVAL_TONES: Record<
    ApprovalRisk,
    { label: string; text: string; bg: string; border: string; dot: string; ring: string; tint: string }
  > = {
    safe: {
      label: "safe",
      text: "#2F6E60",
      bg: "#EAF3EF",
      border: "#CFE6DA",
      dot: "#3F8F7E",
      ring: "rgba(63,143,126,.16)",
      tint: "#EAF3EF",
    },
    caution: {
      label: "caution",
      text: "#9A6E1E",
      bg: "#FBF1DD",
      border: "#F0DCA9",
      dot: "#C99A3A",
      ring: "rgba(201,154,58,.24)",
      tint: "#FBF3E1",
    },
    danger: {
      label: "danger",
      text: "#B14E2A",
      bg: "#F7E7DE",
      border: "#EFCDBD",
      dot: "#C0532E",
      ring: "rgba(192,83,46,.26)",
      tint: "#FBECE4",
    },
  };

  const DENY_CHIPS = ["Wrong approach", "Do it another way", "Not now - I'll handle it"];

  const activeAgent = $derived(
    agents.find((a) => a.Name === activeSession?.AgentName || a.Name === selectedAgent),
  );
  const activeGrad = $derived(agentGradient(activeAgent?.Name ?? selectedAgent ?? "?"));
  const activeMono = $derived(initial(activeAgent?.Name ?? selectedAgent ?? "?"));
  const filteredSessions = $derived(
    sessions.filter((s) => {
      if (originFilter !== "all" && s.Origin !== originFilter) return false;
      if (agentFilter !== "all" && s.AgentName !== agentFilter) return false;
      if (projectFilter !== "all" && s.ProjectID !== projectFilter) return false;
      return true;
    }),
  );
  function projectLabel(id: string): string {
    return projects.find((p) => p.id === id)?.name ?? id;
  }
  const sessionTitle = $derived(
    activeSession ? activeSession.Name || activeSession.AgentName : selectedAgent || "New session",
  );
  const draftOrAgentProvider = $derived(draftProvider || activeAgent?.Provider || "claude");
  const selectedProvider = $derived(activeSession?.Provider ?? draftOrAgentProvider);
  const selectedProfile = $derived(
    activeSession?.Profile ?? (draftProvider ? draftProfile : activeAgent?.Profile ?? ""),
  );
  const inheritedModel = $derived(selectedProvider === activeAgent?.Provider ? activeAgent?.Model || "" : "");
  const modelOptions = $derived(modelsFor(selectedProvider));
  const curModel = $derived(
    activeSession ? activeSession.Model || inheritedModel || "—" : draftModel || inheritedModel || "—",
  );
  const curEffort = $derived(
    activeSession ? activeSession.Effort || activeAgent?.Effort || "medium" : draftEffort || activeAgent?.Effort || "medium",
  );
  const curMode = $derived(
    activeSession
      ? activeSession.PermissionMode || activeAgent?.PermissionMode || "approve"
      : draftPermissionMode || activeAgent?.PermissionMode || "approve",
  );
  const curProjectID = $derived(activeSession ? activeSession.ProjectID : draftProjectID);
  const linkedProjectName = $derived(curProjectID ? projectName || projectLabel(curProjectID) : "");

  // Account / usage context follows the active session (or, pre-session, the
  // selected agent). The usage snapshot key is the profile name, falling back to
  // the provider name for implicit-default profiles — matching the tracker keys.
  const usageProvider = $derived(selectedProvider);
  const usageProfile = $derived(selectedProfile);
  const usageKey = $derived(usageProfile || usageProvider);
  const usageSnapshot = $derived(live.usageByProfile.get(usageKey));
  const accountLabel = $derived(usageProfile ? `${usageProvider} · ${usageProfile}` : usageProvider);
  const providerProfiles = $derived(profiles.filter((p) => p.Provider === usageProvider));
  const showSlash = $derived(messageText.startsWith("/"));
  const activeTurn = $derived(activeSession ? activeTurns[activeSession.ID] : undefined);
  const approvalHistory = $derived(activeSession ? approvalHistoryBySession[activeSession.ID] ?? [] : []);
  const currentApproval = $derived(
    pendingPermission
      ? approvalHistory.find((r) => r.request.id === pendingPermission?.id) ?? approvalRecord(pendingPermission, "pending")
      : null,
  );
  const approvalTone = $derived(currentApproval ? APPROVAL_TONES[currentApproval.risk] : null);
  const approvingPaused = $derived(!!pendingPermission && !!approvalTone);

  function sessionSub(s: Session): string {
    return `${s.AgentName} · ${s.Provider}${s.Model ? " " + s.Model : ""}`;
  }

  function modelsFor(provider: Provider): string[] {
    return provider === "codex" ? ["gpt-5.1", "gpt-5.1-mini", "o4"] : ["sonnet", "opus", "haiku"];
  }

  onMount(() => {
    const mq = window.matchMedia("(max-width: 768px)");
    const syncPhone = () => {
      isPhone = mq.matches;
      if (mq.matches) {
        sessOpen = false;
        ctxOpen = false;
      }
    };
    syncPhone();
    mq.addEventListener("change", syncPhone);
    live.connect();
    unsubscribe = live.subscribe(handleServerMessage);
    listProjects().then((p) => (projects = p)).catch(() => {});
    listProfiles().then((p) => (profiles = p)).catch(() => {});
    restoreLastSession();
    countdown = window.setInterval(updatePermissionRemaining, 1000);
    return () => {
      mq.removeEventListener("change", syncPhone);
      if (countdown) window.clearInterval(countdown);
      unsubscribe?.();
    };
  });

  // When the shared socket (re)connects, re-attach to the open session and flush
  // any pending seed so a freshly opened chat still starts its turn.
  $effect(() => {
    if (live.status === "live") {
      attachActiveSession();
      flushSeed();
    }
  });

  $effect(() => {
    const t = target;
    if (!t) return;
    onConsumeTarget();
    void openTarget(t);
  });

  async function openTarget(t: ChatTarget) {
    if (t.sessionId) {
      const session = sessions.find((s) => s.ID === t.sessionId) ?? ({ ID: t.sessionId } as Session);
      await loadHistory(session);
    } else if (t.agentName) {
      selectedAgent = t.agentName;
      newSession();
    }
    if (t.seed) {
      pendingSeed = t.seed;
      flushSeed();
    }
  }

  function flushSeed() {
    if (pendingSeed && status === "live") {
      const seed = pendingSeed;
      pendingSeed = null;
      sendTurn(seed);
    }
  }

  function send(msg: ClientMessage): boolean {
    if (live.status !== "live") {
      error = "WebSocket is offline — reconnecting…";
      return false;
    }
    live.send(msg);
    return true;
  }

  // handleServerMessage is registered with the live store and handles only this
  // page's view concerns; the store owns sessions/activeTurns/attention itself.
  function handleServerMessage(msg: ServerMessage) {
    switch (msg.type) {
      case "state":
        if (!selectedAgent && agents.length > 0) selectedAgent = agents[0].Name;
        if (activeSession) {
          const replacement = live.sessions.find((s) => s.ID === activeSession?.ID);
          if (replacement) activeSession = replacement;
          sending = !!live.activeTurns[activeSession.ID];
        }
        break;
      case "session":
        if (msg.session) {
          activeSession = msg.session;
          selectedAgent = msg.session.AgentName;
          rememberSession(msg.session.ID);
          if (!msg.session.ProjectID) projectName = "";
          resetDraftSettings();
        }
        break;
      case "turn_state":
        applyTurnState(msg.turn_state);
        break;
      case "history":
        messages = msg.history ?? [];
        pendingAssistant = "";
        pendingPermission = null;
        resetApprovalForm();
        pendingUserInput = null;
        break;
      case "message":
        if (!messageForActiveSession(msg)) break;
        // The agent has produced output, so it is no longer blocked on the user;
        // drop any stale approve/answer modal (see clearPendingRequests).
        clearPendingRequests();
        if (msg.message && !messages.some((e) => sameMessage(e, msg.message))) {
          messages = [...messages, msg.message];
          if (msg.message.Role === "assistant") pendingAssistant = "";
        }
        break;
      case "delta":
        if (!messageForActiveSession(msg)) break;
        clearPendingRequests();
        pendingAssistant += msg.delta ?? "";
        break;
      case "assistant":
        if (!messageForActiveSession(msg)) break;
        clearPendingRequests();
        if (!pendingAssistant) pendingAssistant = msg.delta ?? "";
        break;
      case "permission_request":
        if (!messageForActiveSession(msg)) break;
        if (msg.request) {
          const sessionID = sessionIDForMessage(msg);
          upsertApprovalRecord(sessionID, msg.request);
          pendingPermission = approvalResolved(sessionID, msg.request.id) ? null : msg.request;
        } else {
          pendingPermission = null;
        }
        resetApprovalForm();
        updatePermissionRemaining();
        if (pendingPermission) forceScrollToBottom();
        break;
      case "user_input_request":
        if (!messageForActiveSession(msg)) break;
        pendingUserInput = msg.input ?? null;
        userInputAnswers = initialUserInputAnswers(pendingUserInput);
        if (pendingUserInput) forceScrollToBottom();
        break;
      case "notice":
        notice = msg.notice ?? null;
        sending = false;
        break;
      case "done":
        if (messageForActiveSession(msg)) {
          markApprovalRecord(activeSession?.ID, pendingPermission?.id, "cleared");
          pendingPermission = null;
          resetApprovalForm();
          if (pendingUserInput?.provider !== "claude") pendingUserInput = null;
          sending = false;
        }
        window.setTimeout(() => live.send({ type: "list" }), 1200);
        break;
      case "error":
        if (messageForActiveSession(msg)) {
          if (msg.error === "permission request not found") {
            // A stale approval (the request already timed out / the daemon moved
            // on) — clear the dead modal and show a gentle notice, not an error.
            markApprovalRecord(activeSession?.ID, pendingPermission?.id, "expired");
            pendingPermission = null;
            resetApprovalForm();
            permissionRemaining = 0;
            notice = "The approval request expired.";
            sending = false;
          } else if (msg.error === "user input request not found") {
            // Benign: Claude questions are answered by a follow-up turn, so the
            // broker has no pending entry by the time the decision lands. Nothing
            // failed (the follow-up turn is running) — swallow it silently.
          } else {
            error = msg.error ?? "Unknown server error";
            pendingAssistant = "";
            sending = false;
          }
        }
        break;
    }
  }

  // clearPendingRequests dismisses stale approve/answer modals when the agent
  // resumes producing output (delta/assistant/message) — at that point it is no
  // longer blocked, so acting on the modal would hit a dead request. Claude's
  // question modal is intentionally preserved (its answer is a follow-up turn),
  // matching the "done" handler's semantics.
  function clearPendingRequests() {
    markApprovalRecord(activeSession?.ID, pendingPermission?.id, "cleared");
    pendingPermission = null;
    permissionRemaining = 0;
    resetApprovalForm();
    if (pendingUserInput?.provider !== "claude") pendingUserInput = null;
  }

  function rememberSession(id: string) {
    localStorage.setItem(LAST_SESSION_KEY, id);
  }

  function restoreLastSession() {
    const id = localStorage.getItem(LAST_SESSION_KEY);
    if (!id) return;
    void loadHistory({ ID: id } as Session);
  }

  function attachActiveSession() {
    if (activeSession?.ID && live.status === "live") {
      live.send({ type: "attach_session", request_id: crypto.randomUUID(), session_id: activeSession.ID });
    }
  }

  function messageForActiveSession(msg: ServerMessage): boolean {
    return !msg.session_id || msg.session_id === activeSession?.ID;
  }

  // applyTurnState updates only this page's view of the active session; the
  // store maintains the activeTurns map (and thus attention) independently.
  function applyTurnState(state: TurnState | undefined) {
    if (!state?.session_id) return;
    if (state.session_id !== activeSession?.ID) return;
    sending = state.status === "running";
    pendingAssistant = state.pending_assistant ?? "";
    if (state.pending_permission) {
      upsertApprovalRecord(state.session_id, state.pending_permission);
      pendingPermission = approvalResolved(state.session_id, state.pending_permission.id) ? null : state.pending_permission;
    } else {
      pendingPermission = null;
    }
    if (!state.pending_permission) resetApprovalForm();
    pendingUserInput = state.pending_user_input ?? null;
    if (pendingUserInput) userInputAnswers = initialUserInputAnswers(pendingUserInput);
    if (state.error) error = state.error;
    updatePermissionRemaining();
  }

  function sameMessage(a: Message, b: Message | undefined) {
    return !!b && a.ID === b.ID && a.SessionID === b.SessionID;
  }

  async function scrollMessagesToBottom(behavior: ScrollBehavior = "smooth") {
    await tick();
    requestAnimationFrame(() => {
      if (!msgsEl) return;
      msgsEl.scrollTo({ top: msgsEl.scrollHeight, behavior });
    });
  }

  // forceScrollToBottom re-engages auto-follow for events the user must see.
  function forceScrollToBottom(behavior: ScrollBehavior = "smooth") {
    stick = true;
    void scrollMessagesToBottom(behavior);
  }

  // onMsgsScroll tracks whether the user is near the bottom so auto-follow only
  // kicks in when they haven't deliberately scrolled up.
  function onMsgsScroll() {
    if (!msgsEl) return;
    stick = msgsEl.scrollHeight - msgsEl.scrollTop - msgsEl.clientHeight < 120;
  }

  // Follow new content to the bottom: fires on the user's own message, on each
  // streaming delta, and on the final assistant message — but only while stuck.
  $effect(() => {
    // Touch the reactive deps so the effect re-runs when they change.
    void messages.length;
    void pendingAssistant;
    if (stick) void scrollMessagesToBottom();
  });

  // Errors and notices render at the bottom; make their appearance visible.
  $effect(() => {
    if (error || notice) forceScrollToBottom();
  });

  async function loadHistory(session: Session) {
    error = null;
    try {
      const detail = await getSession(session.ID);
      activeSession = detail.session;
      selectedAgent = detail.session.AgentName;
      rememberSession(detail.session.ID);
      messages = detail.history ?? [];
      projectName = detail.project_name ?? (detail.session.ProjectID ? projectLabel(detail.session.ProjectID) : "");
      pendingAssistant = "";
      pendingPermission = null;
      resetApprovalForm();
      pendingUserInput = null;
      sending = !!activeTurns[detail.session.ID];
      attachActiveSession();
      // Opening a session (incl. via a notification tap) lands on the newest
      // message. Instant jump — no animated scroll through the whole history.
      stick = true;
      void scrollMessagesToBottom("auto");
      if (isPhone) sessOpen = false;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function confirmDeleteSession() {
    if (!pendingDelete) return;
    const id = pendingDelete.ID;
    deleteBusy = true;
    deleteError = null;
    try {
      await deleteSession(id);
      // The store owns the session list; refresh it from the daemon.
      live.send({ type: "list" });
      if (activeSession?.ID === id) {
        activeSession = null;
        messages = [];
        projectName = "";
        pendingAssistant = "";
        pendingPermission = null;
        resetApprovalForm();
        pendingUserInput = null;
        sending = false;
        localStorage.removeItem(LAST_SESSION_KEY);
      }
      pendingDelete = null;
      send({ type: "list" });
    } catch (e) {
      deleteError = e instanceof Error ? e.message : String(e);
    } finally {
      deleteBusy = false;
    }
  }

  function sendTurn(text = messageText.trim()) {
    if (!text) return;
    if (!activeSession && !selectedAgent) {
      error = "Create or select an agent first";
      return;
    }
    if (live.status !== "live") {
      error = "WebSocket is offline — reconnecting…";
      return;
    }
    error = null;
    notice = null;
    sending = true;
    pendingAssistant = "";
    markApprovalRecord(activeSession?.ID, pendingPermission?.id, "cleared");
    pendingPermission = null;
    resetApprovalForm();
    pendingUserInput = null;
    if (!send({
      type: "send_turn",
      request_id: crypto.randomUUID(),
      agent_name: activeSession ? undefined : selectedAgent,
      session_id: activeSession?.ID,
      message: text,
      provider: activeSession ? undefined : usageProvider,
      profile: activeSession ? undefined : usageProfile || undefined,
      model: activeSession ? undefined : draftModel || undefined,
      effort: activeSession ? undefined : draftEffort || undefined,
      permission_mode: activeSession ? undefined : draftPermissionMode || undefined,
      project_id: activeSession ? undefined : draftProjectID || undefined,
    })) return;
    messageText = "";
    forceScrollToBottom();
  }

  function resetDraftSettings() {
    draftProvider = "";
    draftProfile = "";
    draftModel = "";
    draftEffort = "";
    draftPermissionMode = "";
    draftProjectID = "";
  }

  function newSession(resetDrafts = true) {
    activeSession = null;
    messages = [];
    projectName = "";
    localStorage.removeItem(LAST_SESSION_KEY);
    if (resetDrafts) resetDraftSettings();
    pendingAssistant = "";
    pendingPermission = null;
    resetApprovalForm();
    pendingUserInput = null;
    notice = null;
    error = null;
    if (isPhone) sessOpen = false;
  }

  function startSessionWith(agentName: string) {
    selectedAgent = agentName;
    draftProvider = "";
    draftProfile = "";
    draftModel = "";
    draftEffort = "";
    draftPermissionMode = "";
    newSession(false);
    newSessionOpen = false;
  }

  function setDraftProvider(provider: Provider) {
    draftProvider = provider;
    draftProfile = "";
    if (!modelsFor(provider).includes(draftModel)) draftModel = "";
    openDropdown = null;
  }

  function setDraftProfile(profile: ProfileInfo) {
    draftProvider = profile.Provider;
    draftProfile = profile.Name;
    if (!modelsFor(profile.Provider).includes(draftModel)) draftModel = "";
    openDropdown = null;
  }

  function setModel(model: string) {
    if (activeSession) {
      updateSessionSettings({ model });
      return;
    }
    draftModel = model;
    openDropdown = null;
  }

  function setEffort(effort: string) {
    if (activeSession) {
      updateSessionSettings({ effort });
      return;
    }
    draftEffort = effort;
    openDropdown = null;
  }

  function setPermissionMode(mode: PermissionMode) {
    if (activeSession) {
      updateSessionSettings({ permission_mode: mode });
      return;
    }
    draftPermissionMode = mode;
    openDropdown = null;
  }

  function setDraftProject(projectID: string) {
    draftProjectID = projectID;
    projectName = "";
    openDropdown = null;
  }

  function updateSessionSettings(patch: { model?: string; effort?: string; permission_mode?: PermissionMode }) {
    openDropdown = null;
    if (!activeSession) return;
    send({
      type: "update_session_settings",
      request_id: crypto.randomUUID(),
      session_id: activeSession.ID,
      ...patch,
    });
  }

  function stopActiveTurn() {
    if (!activeSession) return;
    send({ type: "stop_turn", request_id: crypto.randomUUID(), session_id: activeSession.ID });
  }

  function activeApprovalSessionID(): string | undefined {
    return activeSession?.ID;
  }

  function sessionIDForMessage(msg: ServerMessage): string | undefined {
    return msg.session_id || activeSession?.ID;
  }

  function approvalRecord(request: PermissionRequest, status: ApprovalStatus, note = ""): ApprovalRecord {
    return {
      request,
      status,
      risk: approvalRisk(request),
      note: note.trim() || undefined,
      at: Date.now(),
    };
  }

  function upsertApprovalRecord(sessionID: string | undefined, request: PermissionRequest) {
    if (!sessionID) return;
    const existing = approvalHistoryBySession[sessionID] ?? [];
    const nextRecord = approvalRecord(request, "pending");
    const idx = existing.findIndex((r) => r.request.id === request.id);
    const next =
      idx >= 0
        ? existing.map((r, i) =>
            i === idx && r.status === "pending" ? { ...r, request, risk: approvalRisk(request) } : r,
          )
        : [...existing, nextRecord];
    approvalHistoryBySession = { ...approvalHistoryBySession, [sessionID]: next };
  }

  function approvalResolved(sessionID: string | undefined, requestID: string | undefined): boolean {
    if (!sessionID || !requestID) return false;
    const record = approvalHistoryBySession[sessionID]?.find((r) => r.request.id === requestID);
    return !!record && record.status !== "pending";
  }

  function markApprovalRecord(sessionID: string | undefined, requestID: string | undefined, status: ApprovalStatus, note = "") {
    if (!sessionID || !requestID) return;
    const existing = approvalHistoryBySession[sessionID] ?? [];
    let changed = false;
    const next = existing.map((r) => {
      if (r.request.id !== requestID || r.status !== "pending") return r;
      changed = true;
      return { ...r, status, note: note.trim() || r.note, at: Date.now() };
    });
    if (changed) approvalHistoryBySession = { ...approvalHistoryBySession, [sessionID]: next };
  }

  function resetApprovalForm() {
    denyingPermissionID = null;
    denyText = "";
  }

  function approvalTextExpanded(requestID: string): boolean {
    return !!expandedApprovalText[requestID];
  }

  function toggleApprovalText(requestID: string) {
    expandedApprovalText = { ...expandedApprovalText, [requestID]: !expandedApprovalText[requestID] };
  }

  function approvePermission() {
    // Guard against acting on an expired request (its broker entry is gone, so
    // the decision would be rejected as "not found").
    if (!pendingPermission || permissionRemaining <= 0) {
      markApprovalRecord(activeApprovalSessionID(), pendingPermission?.id, "expired");
      pendingPermission = null;
      resetApprovalForm();
      return;
    }
    const request = pendingPermission;
    if (!send({
      type: "permission_decision",
      request_id: request.id,
      decision: { behavior: "allow", updatedInput: request.input },
    })) return;
    markApprovalRecord(activeApprovalSessionID(), request.id, "approved");
    pendingPermission = null;
    resetApprovalForm();
    forceScrollToBottom();
  }

  function startDenyPermission() {
    if (!pendingPermission) return;
    denyingPermissionID = pendingPermission.id;
    denyText = "";
  }

  function cancelDenyPermission() {
    resetApprovalForm();
  }

  function submitDenyPermission() {
    if (!pendingPermission) return;
    if (permissionRemaining <= 0) {
      markApprovalRecord(activeApprovalSessionID(), pendingPermission.id, "expired");
      pendingPermission = null;
      resetApprovalForm();
      return;
    }
    const request = pendingPermission;
    const message = denyText.trim() || "Denied from web";
    if (!send({
      type: "permission_decision",
      request_id: request.id,
      decision: { behavior: "deny", message },
    })) return;
    markApprovalRecord(activeApprovalSessionID(), request.id, "denied", message);
    pendingPermission = null;
    resetApprovalForm();
    forceScrollToBottom();
  }

  function onDenyKeydown(e: KeyboardEvent) {
    if (e.key !== "Enter") return;
    e.preventDefault();
    submitDenyPermission();
  }

  function initialUserInputAnswers(req: UserInputRequest | null): Record<string, string[]> {
    const answers: Record<string, string[]> = {};
    for (const q of req?.questions ?? []) answers[q.id] = [];
    return answers;
  }

  function toggleUserInput(q: UserInputQuestion, value: string) {
    const current = userInputAnswers[q.id] ?? [];
    if (q.multi_select) {
      userInputAnswers = {
        ...userInputAnswers,
        [q.id]: current.includes(value) ? current.filter((v) => v !== value) : [...current, value],
      };
      return;
    }
    userInputAnswers = { ...userInputAnswers, [q.id]: [value] };
  }

  function setFreeUserInput(q: UserInputQuestion, value: string) {
    userInputAnswers = { ...userInputAnswers, [q.id]: value.trim() ? [value] : [] };
  }

  function userInputSelected(q: UserInputQuestion, value: string) {
    return (userInputAnswers[q.id] ?? []).includes(value);
  }

  function userInputReady(req: UserInputRequest | null) {
    return !!req && req.questions.every((q) => (userInputAnswers[q.id] ?? []).some((v) => v.trim()));
  }

  function submitUserInput() {
    const req = pendingUserInput;
    if (!req || !userInputReady(req)) return;
    const decision = { answers: userInputAnswers };
    if (!send({ type: "user_input_decision", request_id: req.id, input: decision })) return;
    pendingUserInput = null;
    if (req.provider === "claude") {
      sendTurn(formatUserInputFollowup(req, userInputAnswers));
      return;
    }
    forceScrollToBottom();
  }

  function formatUserInputFollowup(req: UserInputRequest, answers: Record<string, string[]>): string {
    if (req.questions.length === 1) {
      const q = req.questions[0];
      return `Answer to "${q.question}": ${(answers[q.id] ?? []).join(", ")}`;
    }
    return [
      "Answers:",
      ...req.questions.map((q) => `- ${q.question}: ${(answers[q.id] ?? []).join(", ")}`),
    ].join("\n");
  }

  function updatePermissionRemaining() {
    if (!pendingPermission?.expires_at) {
      permissionRemaining = 0;
      return;
    }
    permissionRemaining = Math.max(
      0,
      Math.ceil((new Date(pendingPermission.expires_at).getTime() - Date.now()) / 1000),
    );
    // Expired: the daemon has already auto-denied, so drop the dead modal rather
    // than let the user click Approve and hit "permission request not found".
    if (permissionRemaining <= 0) {
      markApprovalRecord(activeApprovalSessionID(), pendingPermission.id, "expired");
      pendingPermission = null;
      resetApprovalForm();
      notice = "The approval request expired.";
    }
  }

  function toggleDropdown(key: string) {
    openDropdown = openDropdown === key ? null : key;
  }

  function closeMobilePanels() {
    if (!isPhone) return;
    sessOpen = false;
    ctxOpen = false;
    openDropdown = null;
  }

  function permissionCmd(input: Record<string, unknown>): string {
    const command = rawString(input.command ?? input.cmd);
    if (command) return "$ " + command;
    return Object.entries(input)
      .filter(([k]) => !["description", "risk", "severity"].includes(k))
      .map(([k, v]) => `${k}: ${typeof v === "string" ? v : JSON.stringify(v)}`)
      .join("\n");
  }

  function permissionTitle(req: PermissionRequest): string {
    const inputDescription = req.input.description;
    return (
      req.description?.trim() ||
      (typeof inputDescription === "string" ? inputDescription.trim() : "") ||
      req.tool_name
    );
  }

  function approvalRisk(req: PermissionRequest): ApprovalRisk {
    const explicit = rawString(req.input.risk ?? req.input.severity ?? req.input.riskLevel ?? req.input.danger);
    if (explicit) {
      const value = explicit.toLowerCase();
      if (["danger", "high", "critical", "destructive"].includes(value)) return "danger";
      if (["caution", "medium", "moderate", "warning"].includes(value)) return "caution";
      if (["safe", "low", "info", "read"].includes(value)) return "safe";
    }

    const tool = req.tool_name.toLowerCase();
    const command = rawString(req.input.command ?? req.input.cmd).toLowerCase();
    const haystack = `${tool}\n${permissionTitle(req).toLowerCase()}\n${command}\n${JSON.stringify(req.input).toLowerCase()}`;

    if (
      /\bgit\s+push\b.*\s--force\b/.test(command) ||
      /\brm\s+(-[a-z]*r[a-z]*f|-rf|-fr)\b/.test(command) ||
      /\bsudo\b|\bchmod\b|\bchown\b/.test(command) ||
      /\b(drop|truncate)\s+(database|schema|table)\b/.test(command) ||
      /\b(main|master|prod|production)\b/.test(haystack) &&
        /\b(force|overwrite|delete|remove|drop|deploy|publish|release)\b/.test(haystack) ||
      /\b(npm|pnpm|yarn)\s+publish\b/.test(command) ||
      /\b(terraform|kubectl)\s+(apply|delete|destroy)\b/.test(command)
    ) {
      return "danger";
    }

    if (
      /\b(bash|shell|command|exec|file_change|applypatch|write|edit|permissions)\b/.test(tool) ||
      /\b(npm|pnpm|yarn|bun|pip|brew|cargo|go)\s+(install|add|get|update)\b/.test(command) ||
      /\bgit\s+push\b/.test(command) ||
      /\b(curl|wget|ssh|scp|rsync)\b/.test(command) ||
      /\b(mkdir|mv|cp|rm|touch|tee)\b/.test(command)
    ) {
      return "caution";
    }

    if (/\b(read|cat|ls|grep|rg|find|status|diff|show|log|pwd|head|tail)\b/.test(haystack)) {
      return "safe";
    }

    return "caution";
  }

  function rawString(value: unknown): string {
    return typeof value === "string" ? value.trim() : "";
  }

  function approvalSummary(records: ApprovalRecord[]): string {
    const approved = records.filter((r) => r.status === "approved").length;
    const pending = records.filter((r) => r.status === "pending").length;
    const denied = records.filter((r) => r.status === "denied").length;
    const expired = records.filter((r) => r.status === "expired").length;
    const parts = [`${approved} approved`, `${pending} pending`];
    if (denied) parts.push(`${denied} denied`);
    if (expired) parts.push(`${expired} expired`);
    return parts.join(" · ");
  }

  function approvalStatusLabel(status: ApprovalStatus): string {
    switch (status) {
      case "approved":
        return "approved";
      case "denied":
        return "denied";
      case "expired":
        return "expired";
      case "cleared":
        return "cleared";
      default:
        return "pending";
    }
  }

  function approvalStatusIcon(status: ApprovalStatus): string {
    switch (status) {
      case "approved":
        return "✓";
      case "denied":
        return "×";
      case "expired":
      case "cleared":
        return "–";
      default:
        return "●";
    }
  }

  function approvalTime(record: ApprovalRecord): string {
    return new Date(record.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
</script>

<div class="chat" style="flex:1;display:flex;min-height:0">
  {#if isPhone && (sessOpen || ctxOpen)}
    <button class="mobile-panel-backdrop" aria-label="Close panel" onclick={closeMobilePanels}></button>
  {/if}

  <!-- ===== sessions column ===== -->
  {#if sessOpen}
    <div class="sess-col">
      <div class="sess-head">
        <div class="sess-title">Sessions</div>
        <button class="sq-btn teal" onclick={() => (newSessionOpen = true)} title="New session">+</button>
        <button class="sq-btn" onclick={() => (sessOpen = false)} title="Collapse">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 6l-6 6 6 6" /></svg>
        </button>
      </div>

      <div class="sess-filters">
        <div class="dd-wrap">
          <button class="filter-chip" onclick={() => toggleDropdown("fProject")}>
            {projectFilter === "all" ? "all projects" : projectLabel(projectFilter)} <span style="opacity:.55">▾</span>
          </button>
          {#if openDropdown === "fProject"}
            <div class="dd-menu">
              <button class="dd-opt" class:sel={projectFilter === "all"} onclick={() => { projectFilter = "all"; openDropdown = null; }}>all projects</button>
              {#each projects as p}
                <button class="dd-opt" class:sel={projectFilter === p.id} onclick={() => { projectFilter = p.id; openDropdown = null; }}>{p.name}</button>
              {/each}
            </div>
          {/if}
        </div>
        <div class="dd-wrap">
          <button class="filter-chip" onclick={() => toggleDropdown("fAgent")}>
            {agentFilter === "all" ? "all agents" : agentFilter} <span style="opacity:.55">▾</span>
          </button>
          {#if openDropdown === "fAgent"}
            <div class="dd-menu">
              <button class="dd-opt" class:sel={agentFilter === "all"} onclick={() => { agentFilter = "all"; openDropdown = null; }}>all agents</button>
              {#each agents as a}
                <button class="dd-opt" class:sel={agentFilter === a.Name} onclick={() => { agentFilter = a.Name; openDropdown = null; }}>{a.Name}</button>
              {/each}
            </div>
          {/if}
        </div>
        <div class="dd-wrap">
          <button class="filter-chip" onclick={() => toggleDropdown("fOrigin")}>
            {originFilter === "all" ? "all origins" : originFilter} <span style="opacity:.55">▾</span>
          </button>
          {#if openDropdown === "fOrigin"}
            <div class="dd-menu">
              {#each ["all", "web", "cli", "onboarding", "schedule", "roadmap"] as o}
                <button class="dd-opt" class:sel={originFilter === o} onclick={() => { originFilter = o as SessionOrigin | "all"; openDropdown = null; }}>{o === "all" ? "all origins" : o}</button>
              {/each}
            </div>
          {/if}
        </div>
      </div>

      <div class="sess-list">
        {#each filteredSessions as s (s.ID)}
          <div class="sess-row-wrap">
            <button class="sess-row" class:sel={activeSession?.ID === s.ID} onclick={() => loadHistory(s)}>
              <span class="sess-avatar-wrap">
                <span style={avatarStyle(agentGradient(s.AgentName), 32, 10, 13)}>{initial(s.AgentName)}</span>
                {#if live.attention.has(s.ID)}
                  <span class="attn-dot" title="Needs your attention"></span>
                {/if}
              </span>
              <span class="sess-row-text">
                <span class="sess-row-title">{s.Name || s.AgentName}</span>
                <span class="sess-row-sub mono">{sessionSub(s)}</span>
              </span>
              {#if activeTurns[s.ID]}
                <span class="run-pill mono" class:needs={activeTurns[s.ID].pending === "permission" || activeTurns[s.ID].pending === "question"}>
                  {activeTurns[s.ID].pending === "permission" ? "approve" : activeTurns[s.ID].pending === "question" ? "question" : "running"}
                </span>
              {/if}
              <span style={originStyle(s.Origin)}>{originLabel(s.Origin)}</span>
            </button>
            <button class="sess-x" title="Delete session" aria-label="Delete session" onclick={() => (pendingDelete = s)}>
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
            </button>
          </div>
        {/each}
        {#if filteredSessions.length === 0}
          <p class="empty-note">No sessions yet. Pick an agent and say hello.</p>
        {/if}
      </div>
    </div>
  {/if}

  <!-- ===== conversation ===== -->
  <div
    class="conv"
    class:approval-paused={approvingPaused}
    style={approvalTone ? `--approval-text:${approvalTone.text};--approval-bg:${approvalTone.bg};--approval-border:${approvalTone.border};--approval-dot:${approvalTone.dot};--approval-ring:${approvalTone.ring};--approval-tint:${approvalTone.tint}` : ""}
  >
    <div class="conv-head">
      {#if !sessOpen}
        <button class="sq-btn" onclick={() => (sessOpen = true)} title="Show sessions">
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M9 4v16" /></svg>
        </button>
      {/if}
      <div class="conv-title">{sessionTitle}</div>
      {#if activeSession}<span style={originStyle(activeSession.Origin)}>{originLabel(activeSession.Origin)}</span>{/if}
      {#if !ctxOpen}
        <button class="sq-btn" onclick={() => (ctxOpen = true)} title="Show details">
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M15 4v16" /></svg>
        </button>
      {/if}
    </div>

    {#if pendingPermission && approvalTone}
      <div class="approval-banner">
        <span class="approval-banner-dot"></span>
        <span>
          {currentApproval?.risk === "danger"
            ? "Paused - a destructive action needs your approval"
            : "Paused - an action needs your approval"}
        </span>
        {#if permissionRemaining > 0}
          <span class="approval-banner-time mono">auto-denies in {permissionRemaining}s</span>
        {/if}
      </div>
    {/if}

    {#if linkedProjectName}
      <div class="proj-strip">
        <span class="proj-dot-sm" style="background:#3F8F7E"></span>
        <span class="mono proj-strip-text">part of <b>{linkedProjectName}</b></span>
      </div>
    {/if}

    <div
      class="msgs"
      bind:this={msgsEl}
      onscroll={onMsgsScroll}
      onloadcapture={() => {
        if (stick) void scrollMessagesToBottom("auto");
      }}
    >
      {#each messages as m (m.ID)}
        {#if m.Role === "user"}
          <div class="row-end">
            <div class="bubble-user">{m.Content}</div>
          </div>
        {:else}
          <div class="row-start">
            <div style={avatarStyle(activeGrad, 30, 10, 13)}>{activeMono}</div>
            <div class="bubble-assistant">{@html renderMarkdown(m.Content)}</div>
          </div>
        {/if}
      {/each}

      {#if pendingAssistant}
        <div class="row-start">
          <div style={avatarStyle(activeGrad, 30, 10, 13)}>{activeMono}</div>
          <div class="bubble-assistant">{@html renderMarkdown(pendingAssistant)}<span class="cursor"></span></div>
        </div>
      {/if}

      {#if sending && !pendingAssistant && !pendingPermission && !pendingUserInput}
        <div class="row-start" style="align-items:center">
          <div style={avatarStyle(activeGrad, 30, 10, 13)}>{activeMono}</div>
          <span class="thinking">
            <span class="tdot"></span><span class="tdot d2"></span><span class="tdot d3"></span>
          </span>
        </div>
      {/if}

      {#if pendingUserInput}
        <div class="row-start question-wrap">
          <div style={avatarStyle(activeGrad, 30, 10, 13)}>{activeMono}</div>
          <div class="question-card">
            <div class="question-head">
              <span class="approve-tag mono">question · {pendingUserInput.provider ?? "provider"}</span>
            </div>
            <div class="question-body">
              {#each pendingUserInput.questions as q}
                <div class="question-block">
                  {#if q.header}<div class="question-header">{q.header}</div>{/if}
                  <div class="question-text">{q.question}</div>
                  {#if q.options && q.options.length > 0}
                    <div class="question-options">
                      {#each q.options as option}
                        <button
                          class="question-option"
                          class:sel={userInputSelected(q, option.label)}
                          onclick={() => toggleUserInput(q, option.label)}
                        >
                          <span class="question-dot">{q.multi_select ? (userInputSelected(q, option.label) ? "✓" : "") : ""}</span>
                          <span class="question-option-text">
                            <span>{option.label}</span>
                            {#if option.description}<small>{option.description}</small>{/if}
                          </span>
                        </button>
                      {/each}
                    </div>
                  {:else}
                    <input
                      class="question-free"
                      type={q.is_secret ? "password" : "text"}
                      placeholder="Answer"
                      value={(userInputAnswers[q.id] ?? [])[0] ?? ""}
                      oninput={(e) => setFreeUserInput(q, e.currentTarget.value)}
                    />
                  {/if}
                </div>
              {/each}
              <div class="approve-actions">
                <button class="approve-yes" disabled={!userInputReady(pendingUserInput)} onclick={submitUserInput}>Send answer</button>
              </div>
            </div>
          </div>
        </div>
      {/if}

      {#if notice}<div class="notice">{notice}</div>{/if}
      {#if error}<div class="row-start"><div class="bubble-error"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" style="flex:none"><path d="M12 9v4" /><path d="M12 17h.01" /><path d="M10.3 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.7 3.86a2 2 0 0 0-3.4 0z" /></svg>{error}</div></div>{/if}
    </div>

    {#if approvalHistory.length > 0}
      <div class="approval-dock">
        <button class="approval-toggle" onclick={() => (approvalDockOpen = !approvalDockOpen)}>
          <span class="approval-chev">{approvalDockOpen ? "▾" : "▸"}</span>
          <span class="mono">{approvalHistory.length} requests this session</span>
          <span class="approval-toggle-spacer"></span>
          <span class="approval-summary mono">{approvalSummary(approvalHistory)}</span>
        </button>

        {#if approvalDockOpen}
          <div class="approval-history">
            {#each approvalHistory as record (record.request.id)}
              <div class="approval-history-row" class:current={pendingPermission?.id === record.request.id}>
                <span
                  class="approval-history-icon"
                  class:approved={record.status === "approved"}
                  class:denied={record.status === "denied"}
                  class:muted={record.status === "expired" || record.status === "cleared"}
                  style={`--row-dot:${APPROVAL_TONES[record.risk].dot}`}
                >
                  {approvalStatusIcon(record.status)}
                </span>
                <span class="approval-risk-pill mono" style={`--risk-text:${APPROVAL_TONES[record.risk].text};--risk-bg:${APPROVAL_TONES[record.risk].bg};--risk-border:${APPROVAL_TONES[record.risk].border};--risk-dot:${APPROVAL_TONES[record.risk].dot}`}>
                  <span></span>{record.risk}
                </span>
                <div class="approval-history-main">
                  <div class="approval-history-title">{permissionTitle(record.request)}</div>
                  <div class="approval-command-box" class:expanded={approvalTextExpanded(record.request.id)}>
                    <pre class="approval-command mono">{permissionCmd(record.request.input)}</pre>
                    <button
                      type="button"
                      class="approval-command-toggle"
                      aria-expanded={approvalTextExpanded(record.request.id)}
                      aria-label={approvalTextExpanded(record.request.id) ? "Collapse approval text" : "Expand approval text"}
                      title={approvalTextExpanded(record.request.id) ? "Collapse approval text" : "Expand approval text"}
                      onclick={() => toggleApprovalText(record.request.id)}
                    >
                      {approvalTextExpanded(record.request.id) ? "Collapse" : "Expand"}
                    </button>
                  </div>
                  {#if record.status === "denied" && record.note}
                    <div class="approval-history-note">"{record.note}"</div>
                  {/if}
                </div>
                <span class="approval-history-meta mono">{approvalStatusLabel(record.status)} · {approvalTime(record)}</span>
              </div>
            {/each}
          </div>
        {/if}

        {#if pendingPermission && currentApproval && approvalTone}
          {@const livePermission = pendingPermission}
          <div class="approval-live">
            <div class="approval-live-row">
              <span class="approval-risk-pill mono" style={`--risk-text:${approvalTone.text};--risk-bg:${approvalTone.bg};--risk-border:${approvalTone.border};--risk-dot:${approvalTone.dot}`}>
                <span></span>{currentApproval.risk}
              </span>
              <div class="approval-live-main">
                <div class="approval-live-title">{permissionTitle(livePermission)}</div>
                <div class="approval-command-box live" class:expanded={approvalTextExpanded(livePermission.id)}>
                  <pre class="approval-command mono">{permissionCmd(livePermission.input)}</pre>
                  <button
                    type="button"
                    class="approval-command-toggle"
                    aria-expanded={approvalTextExpanded(livePermission.id)}
                    aria-label={approvalTextExpanded(livePermission.id) ? "Collapse approval text" : "Expand approval text"}
                    title={approvalTextExpanded(livePermission.id) ? "Collapse approval text" : "Expand approval text"}
                    onclick={() => toggleApprovalText(livePermission.id)}
                  >
                    {approvalTextExpanded(livePermission.id) ? "Collapse" : "Expand"}
                  </button>
                </div>
              </div>
              {#if denyingPermissionID !== livePermission.id}
                <div class="approval-live-actions">
                  <button class="approval-deny" onclick={startDenyPermission}>Deny</button>
                  <button class="approval-approve" onclick={approvePermission}>Approve</button>
                </div>
              {/if}
            </div>

            {#if denyingPermissionID === livePermission.id}
              <div class="approval-deny-form">
                <div class="approval-deny-label mono">Tell {activeAgent?.Name ?? "the agent"} why - they'll try another way</div>
                <input
                  class="approval-deny-input"
                  bind:value={denyText}
                  placeholder="e.g. rebase onto main first - don't force-push"
                  onkeydown={onDenyKeydown}
                />
                <div class="approval-deny-chips">
                  {#each DENY_CHIPS as chip}
                    <button onclick={() => (denyText = chip)}>{chip}</button>
                  {/each}
                </div>
                <div class="approval-deny-actions">
                  <button class="approval-deny-cancel" onclick={cancelDenyPermission}>Cancel</button>
                  <span></span>
                  <button class="approval-deny-submit" onclick={submitDenyPermission}>
                    {denyText.trim() ? "Send denial" : "Deny anyway"}
                  </button>
                </div>
              </div>
            {/if}
          </div>
        {/if}
      </div>
    {/if}

    <!-- composer -->
    <div class="composer">
      {#if showSlash}
        <div class="slash-menu">
          {#each SLASH_CMDS as c}
            <button class="slash-item" onclick={() => (messageText = c.cmd + " ")}>
              <span class="slash-cmd mono">{c.cmd}</span>
              <span class="slash-desc">{c.desc}</span>
            </button>
          {/each}
        </div>
      {/if}
      <div class="composer-box">
        <input
          class="composer-input"
          bind:value={messageText}
          placeholder={`Message ${activeAgent?.Name ?? "agent"}…   / for commands`}
          onkeydown={(e) => { if (e.key === "Enter") { e.preventDefault(); sendTurn(); } }}
        />
        {#if activeTurn}
          <button class="composer-stop" title="Stop active turn" onclick={stopActiveTurn}>■</button>
        {:else}
          <button class="composer-send" disabled={sending || status !== "live"} onclick={() => sendTurn()}>↑</button>
        {/if}
      </div>
      <div class="composer-meta">
        {#if !activeSession}
          <!-- Pre-session group: project, permission, account. The whole group and
               its trailing divider drop away once a session is running — project,
               approval mode, and the agent/account are all fixed for a live chat. -->
          <div class="dd-wrap">
            <button class="chip-btn" onclick={() => toggleDropdown("project")} title="Project for this new session">
              <span class="chip-ico">📁</span> {curProjectID ? projectLabel(curProjectID) : "no project"} <span class="chip-chev">▾</span>
            </button>
            {#if openDropdown === "project"}
              <div class="dd-menu up">
                <button class="dd-opt" class:sel={!draftProjectID} onclick={() => setDraftProject("")}>no project</button>
                {#each projects as p}
                  <button class="dd-opt mono" class:sel={draftProjectID === p.id} onclick={() => setDraftProject(p.id)}>{p.name}</button>
                {/each}
              </div>
            {/if}
          </div>
          <div class="dd-wrap">
            <button class="chip-btn" onclick={() => toggleDropdown("perm")}>
              <span class="perm-dot" style={`background:${curMode === "yolo" ? "#C0392B" : "#2F6E60"}`}></span>
              {curMode} <span class="chip-chev">▾</span>
            </button>
            {#if openDropdown === "perm"}
              <div class="dd-menu up wide">
                <button class="dd-opt2" class:sel={curMode === "approve"} onclick={() => setPermissionMode("approve")}>
                  <span class="dd-opt2-label">approve</span>
                  <span class="dd-opt2-desc">Confirm each action</span>
                </button>
                <button class="dd-opt2" class:sel={curMode === "yolo"} onclick={() => setPermissionMode("yolo")}>
                  <span class="dd-opt2-label">yolo</span>
                  <span class="dd-opt2-desc">Auto-run everything</span>
                </button>
              </div>
            {/if}
          </div>
          <!-- Account chip: provider glyph + provider · profile for the new session. -->
          <div class="dd-wrap">
            <button class="chip-btn" onclick={() => toggleDropdown("account")} title="Provider & profile for this session">
              <span class="prov-glyph" class:diamond={usageProvider === "codex"}
                style={`background:${usageProvider === "codex" ? "#4B5560" : "#B0572F"}`}></span>
              {accountLabel} <span class="chip-chev">▾</span>
            </button>
            {#if openDropdown === "account"}
              <div class="dd-menu up wide">
                <div class="dd-section">PROVIDER</div>
                <button class="dd-opt2" class:sel={usageProvider === "claude" && !usageProfile} onclick={() => setDraftProvider("claude")}>
                  <span class="dd-opt2-label">Claude</span>
                  <span class="dd-opt2-desc">Use default Claude login</span>
                </button>
                <button class="dd-opt2" class:sel={usageProvider === "codex" && !usageProfile} onclick={() => setDraftProvider("codex")}>
                  <span class="dd-opt2-label">Codex</span>
                  <span class="dd-opt2-desc">Use default Codex login</span>
                </button>
                {#if providerProfiles.length}
                  <div class="dd-section">PROFILE · {providerProfiles.length} account{providerProfiles.length === 1 ? "" : "s"}</div>
                  {#each providerProfiles as p}
                    <button class="dd-opt mono" class:sel={p.Name === usageProfile} onclick={() => setDraftProfile(p)}>{p.Name}</button>
                  {/each}
                {/if}
              </div>
            {/if}
          </div>
          <span class="chip-divider"></span>
        {/if}

        <!-- Model chip -->
        <div class="dd-wrap">
          <button class="chip-btn mono" onclick={() => toggleDropdown("model")}>{curModel} <span class="chip-chev">▾</span></button>
          {#if openDropdown === "model"}
            <div class="dd-menu up">
              {#each modelOptions as o}
                <button class="dd-opt mono" class:sel={o === curModel} onclick={() => setModel(o)}>{o}</button>
              {/each}
            </div>
          {/if}
        </div>

        <!-- Effort chip -->
        <div class="dd-wrap">
          <button class="chip-btn mono" onclick={() => toggleDropdown("effort")}>{curEffort} <span class="chip-chev">▾</span></button>
          {#if openDropdown === "effort"}
            <div class="dd-menu up">
              {#each EFFORTS as o}
                <button class="dd-opt mono" class:sel={o === curEffort} onclick={() => setEffort(o)}>{o}</button>
              {/each}
            </div>
          {/if}
        </div>

        <!-- Usage chip (right-aligned) -->
        <UsageChip
          snapshot={usageSnapshot}
          provider={usageProvider}
          profileLabel={accountLabel}
          open={openDropdown === "usage"}
          refreshing={live.usageRefreshing}
          refreshError={live.usageRefreshError}
          onToggle={() => toggleDropdown("usage")}
          onRefresh={() => live.refreshUsage()}
        />
      </div>
    </div>
  </div>

  <!-- ===== context panel ===== -->
  {#if ctxOpen}
    <div class="ctx">
      <div class="ctx-collapse">
        <button class="sq-btn" onclick={() => (ctxOpen = false)} title="Collapse">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6" /></svg>
        </button>
      </div>
      <div style={avatarStyle(activeGrad, 64, 20, 26) + ";animation:floaty 5s ease-in-out infinite"}>{activeMono}</div>
      <div class="ctx-name">{activeAgent?.Name ?? selectedAgent ?? "—"}</div>
      <div class="ctx-chips">
        {#if activeAgent}<span style={providerChip(activeAgent.Provider)}>{activeAgent.Provider}</span>{/if}
        <span style={modeChip(curMode)}>{curMode}</span>
      </div>
      <div class="ctx-soul">
        {activeSession?.Description || `Runs on ${activeAgent?.Provider ?? "—"} · ${curModel} · effort ${curEffort}.`}
      </div>

      {#if linkedProjectName}
        <div class="label-mono" style="margin:24px 0 10px">linked project</div>
        <div class="ctx-proj">
          <div class="ctx-proj-row">
            <span class="proj-dot-sm" style="background:#3F8F7E"></span>
            <span class="ctx-proj-name">{linkedProjectName}</span>
          </div>
        </div>
      {/if}

      <div class="label-mono" style="margin:24px 0 10px">engine</div>
      <div class="ctx-specs">
        <div class="spec-row"><span>Model</span><span class="mono">{curModel}</span></div>
        <div class="spec-row"><span>Effort</span><span class="mono">{curEffort}</span></div>
        {#if activeAgent?.Profile}<div class="spec-row"><span>Profile</span><span class="mono">{activeAgent.Profile}</span></div>{/if}
        <div class="spec-row"><span>Permission</span><span class="mono">{curMode}</span></div>
      </div>
    </div>
  {/if}
</div>

<!-- ===== New session modal ===== -->
{#if newSessionOpen}
  <div class="modal-backdrop" role="presentation" onclick={() => (newSessionOpen = false)}>
    <div class="modal-card ns-modal" role="dialog" aria-modal="true" aria-label="New session" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <div style="padding:24px 26px 4px">
        <div class="modal-title">New session</div>
        <div class="modal-sub">Who do you want to work with? Pick a colleague and we'll open a fresh chat.</div>
      </div>
      <div class="ns-controls">
        <div class="dd-wrap">
          <button class="filter-chip ns-project-chip" onclick={() => toggleDropdown("nsProject")}>
            {draftProjectID ? projectLabel(draftProjectID) : "no project"} <span style="opacity:.55">▾</span>
          </button>
          {#if openDropdown === "nsProject"}
            <div class="dd-menu">
              <button class="dd-opt" class:sel={!draftProjectID} onclick={() => setDraftProject("")}>no project</button>
              {#each projects as p}
                <button class="dd-opt" class:sel={draftProjectID === p.id} onclick={() => setDraftProject(p.id)}>{p.name}</button>
              {/each}
            </div>
          {/if}
        </div>
      </div>
      <div class="ns-list">
        {#each agents as a}
          <button class="ns-row" onclick={() => startSessionWith(a.Name)}>
            <span style={avatarStyle(agentGradient(a.Name), 46, 14, 19)}>{initial(a.Name)}</span>
            <span class="ns-row-text">
              <span class="ns-row-head">
                <b>{a.Name}</b>
                <span style={providerChip(a.Provider)}>{a.Provider}</span>
                <span style={modeChip(a.PermissionMode)}>{a.PermissionMode}</span>
              </span>
              <span class="ns-row-sub mono">{a.Model || a.Provider} · effort {a.Effort || "medium"}</span>
            </span>
            <span class="ns-arrow">→</span>
          </button>
        {/each}
        {#if agents.length === 0}<p class="empty-note">No agents yet — hire one first.</p>{/if}
      </div>
    </div>
  </div>
{/if}

{#if pendingDelete}
  <ConfirmModal
    title="Delete session"
    message="This permanently removes {pendingDelete.Name || pendingDelete.AgentName} and its chat history. This cannot be undone."
    confirmLabel="Delete session"
    busy={deleteBusy}
    error={deleteError}
    onConfirm={confirmDeleteSession}
    onCancel={() => (pendingDelete = null)}
  />
{/if}

<style>
  .sess-col {
    width: 286px;
    flex: none;
    background: var(--surface-2);
    border-right: 1px solid var(--line);
    display: flex;
    flex-direction: column;
  }

  .sess-head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 18px 18px 12px;
  }

  .sess-title {
    font: 800 17px "Hanken Grotesk";
    flex: 1;
  }

  .sq-btn {
    width: 30px;
    height: 30px;
    flex: none;
    border: 1px solid var(--field-line);
    background: #fff;
    border-radius: 9px;
    cursor: pointer;
    color: #9a8e80;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 17px;
  }

  .sq-btn.teal {
    color: var(--teal-deep);
  }

  .sess-filters {
    display: flex;
    gap: 6px;
    padding: 0 18px 12px;
    flex-wrap: wrap;
    position: relative;
    z-index: 5;
  }

  .dd-wrap {
    position: relative;
  }

  .filter-chip {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 5px 11px;
    border-radius: 999px;
    background: #f1eadf;
    border: 1px solid #e6dbcb;
    font: 500 11px "JetBrains Mono", monospace;
    color: #6f5b45;
    cursor: pointer;
  }

  .dd-menu {
    position: absolute;
    top: calc(100% + 6px);
    left: 0;
    min-width: 150px;
    background: #fff;
    border: 1px solid var(--field-line);
    border-radius: 12px;
    box-shadow: 0 16px 40px -16px rgba(43, 37, 32, 0.34);
    padding: 6px;
    z-index: 25;
    display: flex;
    flex-direction: column;
  }

  .dd-menu.up {
    top: auto;
    bottom: calc(100% + 7px);
  }

  .dd-opt {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 11px;
    border-radius: 8px;
    font: 500 12.5px "JetBrains Mono", monospace;
    cursor: pointer;
    color: #5a5048;
    background: transparent;
    border: none;
    text-align: left;
  }

  .dd-opt:hover {
    background: #f6efe6;
  }

  .dd-opt.sel {
    color: var(--teal-deep);
    background: #e3f1ec;
  }

  .dd-opt:disabled {
    opacity: 0.5;
    cursor: default;
  }

  /* ---- Composer chip-bar (usage redesign) ---- */
  .chip-chev {
    font-size: 10px;
    opacity: 0.55;
    transition: transform 0.15s ease;
  }
  .chip-ico {
    font-size: 12px;
    opacity: 0.8;
  }
  .perm-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    display: inline-block;
  }
  .prov-glyph {
    width: 8px;
    height: 8px;
    border-radius: 2px;
    display: inline-block;
  }
  .prov-glyph.diamond {
    transform: rotate(45deg);
  }
  .chip-divider {
    width: 1px;
    height: 18px;
    background: #e4d8c8;
    margin: 0 2px;
    flex: 0 0 auto;
  }
  .dd-menu.wide {
    min-width: 210px;
  }
  .dd-section {
    font-size: 10px;
    letter-spacing: 0.08em;
    color: #a2937c;
    font-weight: 700;
    padding: 6px 11px 3px;
  }
  .dd-opt2 {
    display: flex;
    flex-direction: column;
    gap: 1px;
    padding: 7px 11px;
    border-radius: 8px;
    cursor: pointer;
    background: transparent;
    border: none;
    text-align: left;
  }
  .dd-opt2:hover {
    background: #f6efe6;
  }
  .dd-opt2.sel {
    background: #e3f1ec;
  }
  .dd-opt2-label {
    font: 600 12.5px "JetBrains Mono", monospace;
    color: #4a4032;
  }
  .dd-opt2-desc {
    font-size: 11px;
    color: #93856f;
  }

  .sess-list {
    flex: 1;
    overflow-y: auto;
    padding: 0 12px 16px;
  }

  .sess-row-wrap {
    position: relative;
  }

  .sess-row {
    display: flex;
    align-items: center;
    gap: 11px;
    width: 100%;
    padding: 11px 12px;
    border-radius: 13px;
    cursor: pointer;
    margin-bottom: 2px;
    background: transparent;
    border: 1px solid transparent;
    text-align: left;
  }

  .sess-x {
    position: absolute;
    top: 5px;
    right: 6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    border: none;
    border-radius: 7px;
    background: transparent;
    color: #b98b7c;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.12s ease;
  }

  .sess-row-wrap:hover .sess-x {
    opacity: 1;
  }

  .sess-x:hover {
    background: #fbeeea;
    color: #a23e22;
  }

  .sess-row:hover {
    background: #f6efe6;
  }

  .sess-row.sel {
    background: #fff;
    box-shadow: 0 2px 10px -6px rgba(43, 37, 32, 0.2);
    border: 1px solid var(--line-3);
  }

  .sess-row-text {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }

  .sess-row-title {
    font: 600 13.5px "Hanken Grotesk";
    color: var(--ink);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .sess-row-sub {
    font-size: 11px;
    color: #9a8e80;
    margin-top: 2px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .run-pill {
    flex: none;
    padding: 3px 7px;
    border-radius: 999px;
    border: 1px solid #cfe3d8;
    background: #eaf5f0;
    color: var(--teal-deep);
    font-size: 9.5px;
    font-weight: 700;
    text-transform: uppercase;
  }

  .run-pill.needs {
    border-color: #f0dca9;
    background: #fbf1dd;
    color: #9a6e1e;
  }

  .sess-avatar-wrap {
    position: relative;
    display: inline-flex;
    flex: none;
  }

  /* Red dot flags a session that is blocked on the user (permission/question). */
  .attn-dot {
    position: absolute;
    top: -2px;
    right: -2px;
    width: 11px;
    height: 11px;
    border-radius: 999px;
    background: #d64528;
    border: 2px solid var(--surface);
    box-shadow: 0 0 0 2px rgba(214, 69, 40, 0.22);
  }

  .conv {
    flex: 1;
    min-width: 360px;
    display: flex;
    flex-direction: column;
    background: var(--bg);
    position: relative;
    isolation: isolate;
  }

  .conv.approval-paused::after {
    content: "";
    position: absolute;
    inset: 0;
    z-index: 12;
    pointer-events: none;
    border: 2px solid var(--approval-border);
    box-shadow:
      inset 0 0 0 3px var(--approval-ring),
      0 0 0 3px var(--approval-ring),
      0 0 34px var(--approval-ring);
    transition:
      border-color 0.22s ease,
      box-shadow 0.22s ease;
  }

  .conv-head {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 16px 24px;
    border-bottom: 1px solid var(--line);
    background: var(--surface-2);
  }

  .conv-title {
    font: 700 16px "Hanken Grotesk";
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .proj-strip {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 24px;
    background: rgba(63, 143, 126, 0.06);
    border-bottom: 1px solid #e6efe9;
  }

  .proj-dot-sm {
    width: 8px;
    height: 8px;
    border-radius: 99px;
    flex: none;
  }

  .proj-strip-text {
    font-size: 11.5px;
    font-weight: 500;
    color: #5e8a7b;
  }

  .proj-strip-text b {
    color: var(--teal-ink);
  }

  .approval-banner {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 9px 24px;
    flex: none;
    background: var(--approval-tint);
    border-bottom: 1px solid var(--approval-border);
    color: var(--approval-text);
    font: 600 12.5px "Hanken Grotesk";
  }

  .approval-banner-dot {
    width: 7px;
    height: 7px;
    flex: none;
    border-radius: 99px;
    background: var(--approval-dot);
    box-shadow: 0 0 0 4px var(--approval-ring);
  }

  .approval-banner-time {
    margin-left: auto;
    color: color-mix(in srgb, var(--approval-text) 72%, #fff);
    font-size: 11px;
    font-weight: 600;
    white-space: nowrap;
  }

  .msgs {
    flex: 1;
    overflow-y: auto;
    padding: 26px 24px;
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  .approval-dock {
    flex: none;
    position: relative;
    z-index: 14;
    border-top: 1px solid var(--line);
    background: #fff;
  }

  .approval-toggle {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 9px 24px;
    background: transparent;
    border: none;
    border-bottom: 1px solid #f1e9dd;
    cursor: pointer;
    text-align: left;
    color: var(--muted);
    font: 600 11.5px "JetBrains Mono", monospace;
  }

  .approval-chev {
    width: 10px;
    color: #9a8e80;
    font-size: 10px;
  }

  .approval-toggle-spacer {
    flex: 1;
  }

  .approval-summary {
    color: var(--faint);
    font-size: 11px;
    font-weight: 500;
    white-space: nowrap;
  }

  .approval-history {
    max-height: 224px;
    overflow-y: auto;
    padding: 8px 16px;
    background: var(--surface-3);
    border-bottom: 1px solid #f0e8dc;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }

  .approval-history-row {
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 9px 10px;
    border-radius: 10px;
    border: 1px solid transparent;
  }

  .approval-history-row.current {
    background: var(--approval-tint);
    border-color: var(--approval-border);
  }

  .approval-history-icon {
    width: 20px;
    height: 20px;
    flex: none;
    border-radius: 999px;
    background: var(--row-dot, var(--gold));
    color: #fff;
    display: flex;
    align-items: center;
    justify-content: center;
    font: 800 11px/1 "Hanken Grotesk";
  }

  .approval-history-icon.approved {
    background: var(--teal);
  }

  .approval-history-icon.denied,
  .approval-history-icon.muted {
    background: #b7a99a;
  }

  .approval-risk-pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    width: 78px;
    justify-content: center;
    flex: none;
    color: var(--risk-text);
    background: var(--risk-bg);
    border: 1px solid var(--risk-border);
    border-radius: 20px;
    padding: 4px 8px;
    font-size: 10px;
    font-weight: 800;
    text-transform: uppercase;
  }

  .approval-risk-pill span {
    width: 6px;
    height: 6px;
    border-radius: 99px;
    background: var(--risk-dot);
  }

  .approval-history-main,
  .approval-live-main {
    flex: 1;
    min-width: 0;
  }

  .approval-history-title,
  .approval-live-title {
    font: 700 13.5px/1.25 "Hanken Grotesk";
    color: var(--ink);
    overflow-wrap: anywhere;
  }

  .approval-command-box {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: start;
    gap: 8px;
    margin-top: 2px;
  }

  .approval-command {
    min-width: 0;
    margin: 0;
    padding: 0;
    border: 0;
    background: transparent;
    color: #5a524a;
    font-size: 11.5px;
    font-weight: 500;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    overflow-wrap: anywhere;
  }

  .approval-command-box.expanded .approval-command {
    max-height: min(280px, 38vh);
    overflow: auto;
    white-space: pre-wrap;
    padding: 8px 10px;
    border: 1px solid #eadfd1;
    border-radius: 8px;
    background: rgba(255, 255, 255, 0.62);
    line-height: 1.45;
  }

  .approval-command-box.live.expanded .approval-command {
    border-color: var(--approval-border);
    background: rgba(255, 255, 255, 0.74);
  }

  .approval-command-toggle {
    align-self: start;
    flex: none;
    border: 1px solid #e6d9cc;
    border-radius: 8px;
    padding: 3px 8px;
    background: rgba(255, 255, 255, 0.72);
    color: var(--muted);
    cursor: pointer;
    font: 700 10.5px "Hanken Grotesk";
  }

  .approval-command-toggle:hover {
    border-color: #d7c7b6;
    color: var(--ink);
  }

  .approval-history-note {
    margin-top: 4px;
    color: var(--orange-ink);
    font: italic 500 11px "Hanken Grotesk";
  }

  .approval-history-meta {
    flex: none;
    color: #b3a899;
    font-size: 10.5px;
    font-weight: 500;
    white-space: nowrap;
  }

  .approval-live {
    background: var(--approval-tint, #fbf3e1);
  }

  .approval-live-row {
    display: flex;
    align-items: center;
    gap: 13px;
    padding: 13px 24px;
  }

  .approval-live-actions {
    display: flex;
    gap: 9px;
    flex: none;
  }

  .approval-approve,
  .approval-deny,
  .approval-deny-submit,
  .approval-deny-cancel,
  .approval-deny-chips button {
    border-radius: 10px;
    font: 700 13px "Hanken Grotesk";
    cursor: pointer;
  }

  .approval-approve {
    border: none;
    padding: 9px 19px;
    background: var(--teal);
    color: #fff;
    box-shadow: 0 6px 13px -6px rgba(63, 143, 126, 0.7);
  }

  .approval-deny {
    border: 1px solid #e6d9cc;
    padding: 9px 15px;
    background: #fff;
    color: var(--muted);
  }

  .approval-deny-form {
    padding: 4px 24px 16px;
    background: var(--approval-tint, #fbf3e1);
  }

  .approval-deny-label {
    margin-bottom: 9px;
    color: var(--orange-ink);
    font-size: 10.5px;
    font-weight: 800;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .approval-deny-input {
    width: 100%;
    border: 1px solid #eee0d2;
    border-radius: 10px;
    padding: 10px 12px;
    background: #fdfbf7;
    color: var(--ink);
    outline: none;
    font: 400 13.5px "Hanken Grotesk";
  }

  .approval-deny-input:focus {
    border-color: #d9b59f;
    box-shadow: 0 0 0 3px rgba(177, 78, 42, 0.12);
  }

  .approval-deny-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 10px;
  }

  .approval-deny-chips button {
    border: 1px solid #eddfd1;
    padding: 5px 11px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 11.5px;
    font-weight: 700;
  }

  .approval-deny-actions {
    display: flex;
    align-items: center;
    gap: 9px;
    margin-top: 12px;
  }

  .approval-deny-actions span {
    flex: 1;
  }

  .approval-deny-cancel {
    border: none;
    padding: 9px 6px;
    background: transparent;
    color: #9a8e80;
  }

  .approval-deny-submit {
    border: none;
    padding: 9px 18px;
    background: var(--orange-ink);
    color: #fff;
    box-shadow: 0 6px 13px -6px rgba(176, 78, 42, 0.7);
  }

  .row-end {
    display: flex;
    justify-content: flex-end;
  }

  .row-start {
    display: flex;
    gap: 12px;
    max-width: 88%;
  }

  .bubble-user {
    max-width: 74%;
    background: #fff;
    border: 1px solid var(--line-3);
    border-radius: 18px 18px 6px 18px;
    padding: 12px 16px;
    font: 400 15px/1.5 "Hanken Grotesk";
    color: var(--ink);
    box-shadow: 0 2px 8px -5px rgba(43, 37, 32, 0.14);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .bubble-assistant {
    background: #fff;
    border: 1px solid var(--line-3);
    border-radius: 6px 18px 18px 18px;
    padding: 13px 16px;
    font: 400 15px/1.6 "Hanken Grotesk";
    color: var(--ink-soft);
    box-shadow: 0 2px 8px -6px rgba(43, 37, 32, 0.12);
    min-width: 0;
    word-break: break-word;
  }

  .bubble-assistant :global(*) {
    max-width: 100%;
  }

  .bubble-assistant :global(:first-child) {
    margin-top: 0;
  }

  .bubble-assistant :global(:last-child) {
    margin-bottom: 0;
  }

  .bubble-assistant :global(p) {
    margin: 0 0 0.72em;
  }

  .bubble-assistant :global(h1),
  .bubble-assistant :global(h2),
  .bubble-assistant :global(h3),
  .bubble-assistant :global(h4),
  .bubble-assistant :global(h5),
  .bubble-assistant :global(h6) {
    margin: 0.9em 0 0.42em;
    font-weight: 700;
    line-height: 1.25;
    color: var(--ink);
    letter-spacing: 0;
  }

  .bubble-assistant :global(h1) {
    font-size: 1.22em;
  }

  .bubble-assistant :global(h2) {
    font-size: 1.14em;
  }

  .bubble-assistant :global(h3),
  .bubble-assistant :global(h4),
  .bubble-assistant :global(h5),
  .bubble-assistant :global(h6) {
    font-size: 1.04em;
  }

  .bubble-assistant :global(ul),
  .bubble-assistant :global(ol) {
    margin: 0 0 0.78em;
    padding-left: 1.25em;
  }

  .bubble-assistant :global(li) {
    margin: 0.16em 0;
  }

  .bubble-assistant :global(li > p) {
    margin: 0.22em 0;
  }

  .bubble-assistant :global(a) {
    color: var(--teal-deep);
    font-weight: 600;
    text-decoration: underline;
    text-decoration-color: rgba(47, 110, 96, 0.32);
    text-underline-offset: 2px;
  }

  .bubble-assistant :global(blockquote) {
    margin: 0 0 0.82em;
    padding: 0.08em 0 0.08em 0.85em;
    border-left: 3px solid var(--line);
    color: var(--muted);
  }

  .bubble-assistant :global(code) {
    border: 1px solid var(--line-3);
    border-radius: 5px;
    background: var(--surface-2);
    padding: 0.08em 0.32em;
    font: 500 0.88em/1.5 "JetBrains Mono", monospace;
    color: var(--ink);
    overflow-wrap: anywhere;
  }

  .bubble-assistant :global(pre) {
    max-width: 100%;
    margin: 0 0 0.86em;
    overflow-x: auto;
    border: 1px solid var(--line-3);
    border-radius: 8px;
    background: var(--surface-2);
    padding: 11px 12px;
  }

  .bubble-assistant :global(pre code) {
    display: block;
    min-width: max-content;
    border: 0;
    border-radius: 0;
    background: transparent;
    padding: 0;
    color: var(--ink);
    white-space: pre;
    overflow-wrap: normal;
  }

  .bubble-assistant :global(table) {
    display: block;
    width: 100%;
    margin: 0 0 0.86em;
    overflow-x: auto;
    border-collapse: collapse;
    font-size: 0.94em;
  }

  .bubble-assistant :global(th),
  .bubble-assistant :global(td) {
    border: 1px solid var(--line-3);
    padding: 6px 8px;
    text-align: left;
    vertical-align: top;
  }

  .bubble-assistant :global(th) {
    background: var(--surface-2);
    color: var(--ink);
    font-weight: 700;
  }

  .bubble-assistant :global(hr) {
    margin: 1em 0;
    border: 0;
    border-top: 1px solid var(--line-3);
  }

  .bubble-error {
    display: inline-flex;
    align-items: flex-start;
    gap: 7px;
    background: #fbeeea;
    border: 1px solid #e7c3b5;
    border-radius: 6px 18px 18px 18px;
    padding: 13px 16px;
    font: 500 14px/1.6 "Hanken Grotesk";
    color: #a23e22;
    box-shadow: 0 2px 8px -6px rgba(162, 62, 34, 0.18);
  }

  .cursor {
    display: inline-block;
    width: 8px;
    height: 16px;
    background: var(--orange);
    border-radius: 2px;
    vertical-align: -3px;
    margin-left: 2px;
    animation: blink 1s steps(1) infinite;
  }

  .thinking {
    display: inline-flex;
    gap: 4px;
    align-items: center;
    padding: 9px 13px;
    border-radius: 16px;
    background: #fff;
    border: 1px solid var(--line-3);
  }

  .tdot {
    width: 6px;
    height: 6px;
    border-radius: 9px;
    background: var(--orange);
    animation: dotPulse 1.2s infinite;
  }

  .tdot.d2 {
    animation-delay: 0.2s;
  }

  .tdot.d3 {
    animation-delay: 0.4s;
  }

  .question-wrap {
    margin-left: 0;
    max-width: 640px;
  }

  .question-card {
    flex: 1;
    background: #fff;
    border: 1px solid #cfe3d8;
    border-radius: 15px;
    overflow: hidden;
    box-shadow: 0 8px 22px -14px rgba(63, 143, 126, 0.32);
  }

  .question-head {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 11px 15px;
    background: #eaf5f0;
    border-bottom: 1px solid #cfe3d8;
  }

  .approve-tag {
    font-weight: 700;
    font-size: 11px;
    letter-spacing: 0;
    color: var(--gold);
    text-transform: uppercase;
    flex: none;
  }

  .question-body {
    padding: 14px 15px;
  }

  .question-block + .question-block {
    margin-top: 15px;
    padding-top: 14px;
    border-top: 1px solid var(--line-3);
  }

  .question-header {
    font: 700 11px "JetBrains Mono", monospace;
    color: var(--teal-deep);
    text-transform: uppercase;
    margin-bottom: 6px;
  }

  .question-text {
    font: 700 15px/1.35 "Hanken Grotesk";
    color: var(--ink);
    margin-bottom: 10px;
  }

  .question-options {
    display: grid;
    gap: 8px;
  }

  .question-option {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    width: 100%;
    min-height: 44px;
    padding: 10px 11px;
    border: 1px solid var(--line-3);
    border-radius: 10px;
    background: var(--surface-3);
    color: var(--ink);
    cursor: pointer;
    text-align: left;
  }

  .question-option.sel {
    border-color: #9bcdbd;
    background: #eaf5f0;
  }

  .question-dot {
    width: 17px;
    height: 17px;
    flex: none;
    border-radius: 50%;
    border: 1px solid #9bcdbd;
    color: var(--teal-deep);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 11px;
    line-height: 1;
    margin-top: 1px;
  }

  .question-option.sel .question-dot {
    background: var(--teal);
    border-color: var(--teal);
    box-shadow: inset 0 0 0 4px #fff;
  }

  .question-option-text {
    display: flex;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
    font: 700 13.5px/1.25 "Hanken Grotesk";
  }

  .question-option-text small {
    font: 500 12px/1.3 "Hanken Grotesk";
    color: var(--muted);
  }

  .question-free {
    width: 100%;
    min-height: 40px;
    border: 1px solid var(--field-line);
    border-radius: 10px;
    padding: 9px 11px;
    font: 500 13.5px "Hanken Grotesk";
    color: var(--ink);
    background: #fff;
  }

  .approve-actions {
    display: flex;
    gap: 9px;
    margin-top: 12px;
  }

  .approve-yes {
    flex: 1;
    border: none;
    border-radius: 10px;
    padding: 9px;
    background: var(--teal);
    color: #fff;
    font: 600 13.5px "Hanken Grotesk";
    cursor: pointer;
    box-shadow: 0 6px 13px -6px rgba(63, 143, 126, 0.7);
  }

  .approve-yes:disabled {
    opacity: 0.45;
    cursor: default;
    box-shadow: none;
  }

  .notice {
    border: 1px solid #cfe3d8;
    background: #eef6f0;
    color: #285d3f;
    border-radius: 12px;
    padding: 11px 14px;
    font: 500 13px "Hanken Grotesk";
  }

  .composer {
    padding: 14px 24px 18px;
    border-top: 1px solid var(--line);
    background: var(--surface-2);
    position: relative;
  }

  .slash-menu {
    position: absolute;
    bottom: 94px;
    left: 24px;
    right: 24px;
    max-width: 420px;
    background: #fff;
    border: 1px solid var(--field-line);
    border-radius: 14px;
    box-shadow: 0 16px 40px -16px rgba(43, 37, 32, 0.3);
    padding: 7px;
    animation: popIn 0.16s ease;
    display: flex;
    flex-direction: column;
  }

  .slash-item {
    display: flex;
    gap: 12px;
    align-items: baseline;
    padding: 8px 11px;
    border-radius: 9px;
    border: none;
    background: transparent;
    cursor: pointer;
    text-align: left;
  }

  .slash-item:hover {
    background: #f6efe6;
  }

  .slash-cmd {
    font: 600 13px "JetBrains Mono", monospace;
    color: var(--orange-ink);
    min-width: 104px;
  }

  .slash-desc {
    font: 400 12.5px "Hanken Grotesk";
    color: var(--muted-2);
  }

  .composer-box {
    display: flex;
    align-items: center;
    gap: 10px;
    background: #fff;
    border: 1px solid var(--field-line);
    border-radius: 14px;
    padding: 8px 8px 8px 16px;
  }

  .composer-input {
    flex: 1;
    border: none;
    outline: none;
    background: transparent;
    font: 400 15px "Hanken Grotesk";
    color: var(--ink);
    padding: 6px 0;
  }

  .composer-send {
    width: 36px;
    height: 36px;
    border: none;
    border-radius: 11px;
    background: var(--teal);
    color: #fff;
    font-size: 16px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    box-shadow: 0 6px 14px -6px rgba(63, 143, 126, 0.7);
  }

  .composer-stop {
    width: 36px;
    height: 36px;
    border: 1px solid #e7c3b5;
    border-radius: 11px;
    background: #fbeeea;
    color: #a23e22;
    font-size: 13px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .composer-meta {
    display: flex;
    gap: 7px;
    margin-top: 11px;
    flex-wrap: wrap;
    align-items: center;
  }

  .chip-btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 5px 11px;
    border: 1px solid #e6dbcb;
    border-radius: 999px;
    background: #f1eadf;
    font: 500 12px "JetBrains Mono", monospace;
    color: #6f5b45;
    cursor: pointer;
  }

  .ctx {
    width: 288px;
    flex: none;
    background: var(--surface-2);
    border-left: 1px solid var(--line);
    padding: 18px 22px 24px;
    overflow-y: auto;
  }

  .ctx-collapse {
    display: flex;
    justify-content: flex-end;
    margin-bottom: 4px;
  }

  .ctx-name {
    font: 800 22px "Hanken Grotesk";
    margin-top: 15px;
    letter-spacing: -0.01em;
  }

  .ctx-chips {
    display: flex;
    gap: 6px;
    margin-top: 9px;
    flex-wrap: wrap;
  }

  .ctx-soul {
    font: 400 13.5px/1.6 "Hanken Grotesk";
    color: var(--muted);
    margin-top: 14px;
    font-style: italic;
  }

  .ctx-proj {
    background: #fff;
    border: 1px solid var(--line-3);
    border-radius: 14px;
    padding: 14px;
  }

  .ctx-proj-row {
    display: flex;
    align-items: center;
    gap: 9px;
  }

  .ctx-proj-name {
    font: 600 14px "Hanken Grotesk";
    color: var(--ink);
  }

  .ctx-specs {
    background: #fff;
    border: 1px solid var(--line-3);
    border-radius: 14px;
    padding: 4px 14px;
  }

  .spec-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 9px 0;
    border-top: 1px solid #f1eae0;
    font: 400 13.5px "Hanken Grotesk";
    color: var(--muted);
  }

  .spec-row:first-child {
    border-top: none;
  }

  .spec-row span:last-child {
    font: 600 12.5px "JetBrains Mono", monospace;
    color: var(--ink);
  }

  /* new session modal */
  .ns-modal {
    width: 440px;
    max-width: 92vw;
  }

  .ns-list {
    padding: 16px 18px 22px;
    display: flex;
    flex-direction: column;
    gap: 9px;
  }

  .ns-controls {
    padding: 14px 26px 0;
    display: flex;
    justify-content: flex-start;
  }

  .ns-project-chip {
    max-width: 240px;
  }

  .ns-row {
    display: flex;
    gap: 14px;
    align-items: center;
    padding: 13px;
    border: 1px solid var(--line-3);
    border-radius: 15px;
    cursor: pointer;
    background: #fff;
    text-align: left;
  }

  .ns-row:hover {
    border-color: #bfe0d6;
    background: #fbfdfc;
  }

  .ns-row-text {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }

  .ns-row-head {
    display: flex;
    align-items: center;
    gap: 7px;
    font: 800 16px "Hanken Grotesk";
  }

  .ns-row-sub {
    font-size: 12px;
    color: var(--muted-2);
    margin-top: 4px;
  }

  .ns-arrow {
    font: 600 16px "Hanken Grotesk";
    color: var(--teal-deep);
    flex: none;
  }


  .mobile-panel-backdrop {
    display: none;
  }

  @media (max-width: 768px) {
    .mobile-panel-backdrop {
      display: block;
      position: fixed;
      inset: 0 0 calc(72px + env(safe-area-inset-bottom)) 0;
      z-index: 30;
      border: none;
      background: rgba(43, 37, 32, 0.22);
      backdrop-filter: blur(1px);
      padding: 0;
    }

    .chat {
      width: 100%;
      min-width: 0;
      overflow: hidden;
    }

    .sess-col,
    .ctx {
      position: fixed;
      top: 0;
      bottom: calc(72px + env(safe-area-inset-bottom));
      z-index: 35;
      width: min(88vw, 340px);
      max-width: calc(100vw - 34px);
      box-shadow: 18px 0 42px -32px rgba(43, 37, 32, 0.58);
    }

    .sess-col {
      left: 0;
      border-right: 1px solid var(--line);
    }

    .ctx {
      right: 0;
      border-left: 1px solid var(--line);
      padding: 16px 18px 20px;
      box-shadow: -18px 0 42px -32px rgba(43, 37, 32, 0.58);
    }

    .conv {
      width: 100%;
      min-width: 0;
    }

    .conv-head {
      padding: 12px 14px;
      gap: 9px;
    }

    .conv-title {
      font-size: 15px;
    }

    .proj-strip {
      padding: 6px 14px;
    }

    .approval-banner {
      padding: 8px 14px;
      align-items: flex-start;
      flex-wrap: wrap;
    }

    .approval-banner-time {
      width: 100%;
      margin-left: 16px;
    }

    .msgs {
      padding: 18px 14px;
      gap: 14px;
    }

    .approval-toggle {
      padding: 9px 14px;
      gap: 7px;
      align-items: flex-start;
    }

    .approval-toggle-spacer {
      display: none;
    }

    .approval-summary {
      white-space: normal;
      text-align: right;
      margin-left: auto;
    }

    .approval-history {
      max-height: 190px;
      padding: 8px 8px;
    }

    .approval-history-row {
      align-items: flex-start;
      gap: 8px;
      padding: 9px 8px;
      flex-wrap: wrap;
    }

    .approval-history-main {
      flex-basis: calc(100% - 108px);
    }

    .approval-history-meta {
      width: 100%;
      padding-left: 31px;
    }

    .approval-live-row {
      align-items: flex-start;
      padding: 12px 14px;
      gap: 10px;
      flex-wrap: wrap;
    }

    .approval-live-main {
      flex-basis: calc(100% - 92px);
    }

    .approval-live-actions {
      width: 100%;
      padding-left: 92px;
    }

    .approval-live-actions button {
      flex: 1;
    }

    .approval-deny-form {
      padding: 2px 14px 14px;
    }

    .row-start {
      max-width: 100%;
    }

    .row-start {
      gap: 9px;
    }

    .bubble-user {
      max-width: 88%;
      padding: 11px 13px;
      font-size: 14.5px;
    }

    .bubble-assistant,
    .bubble-error {
      padding: 11px 13px;
      font-size: 14.5px;
    }

    .approve-actions {
      flex-direction: column;
    }

    .composer {
      padding: 10px 12px 12px;
    }

    .composer-box {
      gap: 8px;
      padding: 7px 7px 7px 12px;
    }

    .composer-input {
      min-width: 0;
      font-size: 14.5px;
    }

    .composer-send {
      width: 34px;
      height: 34px;
    }

    .composer-meta {
      flex-wrap: nowrap;
      overflow-x: auto;
      padding-bottom: 2px;
      scrollbar-width: none;
    }

    .composer-meta::-webkit-scrollbar {
      display: none;
    }

    .composer-meta .dd-wrap,
    .chip-btn {
      flex: none;
    }

    .slash-menu {
      left: 12px;
      right: 12px;
      bottom: 82px;
      max-width: none;
    }

    .slash-item {
      align-items: flex-start;
      flex-direction: column;
      gap: 2px;
    }

    .slash-cmd {
      min-width: 0;
    }

    .ns-list {
      max-height: calc(100dvh - 138px);
      overflow-y: auto;
      padding: 14px;
    }

    .ns-row-head {
      align-items: flex-start;
      flex-direction: column;
    }
  }
</style>
