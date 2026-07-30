<script lang="ts">
  import { onMount } from "svelte";
  import {
    addGoalFeedback,
    answerAgentQuestion,
    approveAccessRequest,
    createGoal,
    deleteGoal,
    denyAccessRequest,
    getGoal,
    getGoalRun,
    listAccessRequests,
    listGoalEvents,
    listGoals,
    listProfiles,
    listProjects,
    patchGoal,
    resolveGoalRateLimit,
    runGoalReview,
    updateGoalFeedback,
  } from "../lib/api";
  import { live } from "../lib/live.svelte";
  import { renderMarkdown } from "../lib/markdown";
  import AgentAvatar from "../lib/AgentAvatar.svelte";
  import ConfirmModal from "../lib/ConfirmModal.svelte";
  import VoiceButton from "../lib/VoiceButton.svelte";
  import { appendTranscript } from "../lib/voice";
  import RunTargetPicker from "../lib/RunTargetPicker.svelte";
  import type { RunTargetValue } from "../lib/RunTargetPicker.svelte";
  import UsageBar from "../lib/UsageBar.svelte";
  import type {
    AccessRequest,
    AccessRequestKind,
    Agent,
    AgentQuestion,
    Goal,
    GoalDetail,
    GoalEvent,
    GoalEventKind,
    GoalMetric,
    GoalRateLimitBlock,
    GoalRunDetail,
    GoalStatus,
    Message,
    ProfileInfo,
    Project,
  } from "../lib/types";

  interface ChatTarget {
    sessionId?: string;
    agentName?: string;
    seed?: string;
  }

  let {
    agents = [],
    target = null,
    onConsumeTarget = () => {},
    onOpenChat = (_t: ChatTarget) => {},
  }: {
    agents?: Agent[];
    target?: string | null;
    onConsumeTarget?: () => void;
    onOpenChat?: (t: ChatTarget) => void;
  } = $props();

  let view = $state<"list" | "detail" | "create">("list");
  let goals = $state<Goal[]>([]);
  let openRequests = $state<AccessRequest[]>([]); // pending + failed, all goals
  let detail = $state<GoalDetail | null>(null);
  let moreEvents = $state(true);
  let loadingMore = $state(false);
  let error = $state<string | null>(null);
  let busy = $state("");
  // reviewBusy bridges the gap between clicking "Review now" and the review's
  // run showing up on the detail response; after that, running_run drives the
  // spinner until the run finishes (the finish ping refreshes the detail).
  let reviewBusy = $state("");
  let feedbackOpen = $state(false);
  let feedbackBody = $state("");
  let feedbackBusy = $state(false);
  let editingFeedbackID = $state(0);
  let editingFeedbackBody = $state("");
  let editingFeedbackBusy = $state(false);
  // Deferred-question answering (podiom_ask_user). Keyed by question item id.
  let questionAnswers = $state<Record<string, string[]>>({});
  let questionBusy = $state(false);
  let selectedRunEvent = $state<GoalEvent | null>(null);
  let runDetail = $state<GoalRunDetail | null>(null);
  let runLoading = $state(false);
  let runError = $state<string | null>(null);

  // Create form.
  let cTitle = $state("");
  let cDesc = $state("");
  let cCriteria = $state("");
  let cMetrics = $state<{ name: string; target: string; unit: string }[]>([{ name: "", target: "", unit: "" }]);
  let cAgent = $state("");
  let cProvider = $state<RunTargetValue["provider"]>("");
  let cProfile = $state("");
  let cModel = $state("");
  let cEffort = $state("");
  let cProject = $state("");
  let cCadence = $state("24h");
  let cBusy = $state(false);
  let projects = $state<Project[]>([]);
  let profiles = $state<ProfileInfo[]>([]);

  // Rate-limit recovery target.
  let recoveryBlockID = $state("");
  let recoveryProvider = $state<RunTargetValue["provider"]>("");
  let recoveryProfile = $state("");
  let recoveryModel = $state("");
  let recoveryEffort = $state("");
  let recoveryBusy = $state(false);

  const CADENCES = [
    { label: "every 6h", v: "6h" },
    { label: "every 12h", v: "12h" },
    { label: "every 24h", v: "24h" },
    { label: "every 3d", v: "72h" },
    { label: "weekly", v: "168h" },
  ];

  // Access-request decision dialog.
  let dialog = $state<{ req: AccessRequest; action: "approve" | "deny"; note: string; secret: string; busy: boolean } | null>(null);

  // Destructive confirmations.
  let pendingAbandon = $state<Goal | null>(null);
  let pendingDelete = $state<Goal | null>(null);
  let confirmBusy = $state(false);
  let menuOpen = $state(false);

  const PAGE = 50;

  // ---- visual language (from the Goals design comp) -------------------------

  const EK: Record<GoalEventKind, { c: string; t: string; label: string; ic: string }> = {
    created: { c: "#8a7f73", t: "#f1ece6", label: "Goal created", ic: '<path d="M5 21V4"/><path d="M5 4h11l-1.6 3L16 10H5"/>' },
    planning_started: { c: "#5847b8", t: "#eeeafb", label: "Planning", ic: '<path d="M9 18h6"/><path d="M10 21h4"/><path d="M12 3a6 6 0 0 0-3.8 10.6c.6.6.8 1 .8 1.9h6c0-.9.2-1.3.8-1.9A6 6 0 0 0 12 3z"/>' },
    review_started: { c: "#9a6e1e", t: "#fbf1dd", label: "Review session", ic: '<path d="M20 11A8 8 0 1 1 11 4"/><path d="M20 4v4h-4"/><path d="M12 8v4l2.4 1.5"/>' },
    progress: { c: "#3f8f7e", t: "#e3f1ec", label: "Progress", ic: '<path d="M3 17l5-5 4 4 8-9"/><path d="M21 7v4h-4"/>' },
    metric_update: { c: "#2f6e60", t: "#e2f0ec", label: "Metric update", ic: '<path d="M5 20V11"/><path d="M12 20V5"/><path d="M19 20v-6"/><path d="M3 20h18"/>' },
    plan_change: { c: "#5847b8", t: "#eeeafb", label: "Plan updated", ic: '<path d="M4 4h6v16H4z"/><path d="M14 4h6v10h-6z"/>' },
    user_feedback: { c: "#4a6fa8", t: "#e7eef7", label: "Your feedback", ic: '<path d="M21 15a2 2 0 0 1-2 2H8l-5 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/><path d="M8 8h8"/><path d="M8 12h5"/>' },
    access_requested: { c: "#d9663d", t: "#fbeae0", label: "Access requested", ic: '<path d="M8 11V8a4 4 0 0 1 8 0"/><path d="M5 11h14v9H5z"/><path d="M12 14v3"/>' },
    access_decided: { c: "#4f9e78", t: "#eaf1ed", label: "Access decided", ic: '<path d="M20 6 9 17l-5-5"/>' },
    status_change: { c: "#8a7f73", t: "#f1ece6", label: "Status changed", ic: '<path d="M7 4 4 7l3 3"/><path d="M4 7h11"/><path d="M17 20l3-3-3-3"/><path d="M20 17H9"/>' },
    completion_proposed: { c: "#b14322", t: "#fbe7e0", label: "Completion proposed", ic: '<path d="M12 3l2.6 5.8 6.4.6-4.8 4.2 1.4 6.2L12 17l-5.6 3 1.4-6.2L3 9.4l6.4-.6z"/>' },
    rate_limited: { c: "#b14e2a", t: "#fbeae0", label: "Rate limited", ic: '<path d="M12 2v5"/><path d="M12 17v5"/><path d="m4.9 4.9 3.5 3.5"/><path d="m15.6 15.6 3.5 3.5"/><path d="M2 12h5"/><path d="M17 12h5"/><path d="m4.9 19.1 3.5-3.5"/><path d="m15.6 8.4 3.5-3.5"/>' },
    rate_limit_resolved: { c: "#2f6e60", t: "#e2f0ec", label: "Rate limit resolved", ic: '<path d="M20 6 9 17l-5-5"/><path d="M14 6h6v6"/>' },
    tool_use: { c: "#6b6257", t: "#f0ece6", label: "Tool call", ic: '<path d="M4 17l6-6-6-6"/><path d="M12 19h8"/>' },
    question_asked: { c: "#9a6e1e", t: "#fbf1dd", label: "Question asked", ic: '<path d="M9.1 9a3 3 0 1 1 4 2.8c-.8.4-1.1 1-1.1 2"/><path d="M12 17h.01"/>' },
    question_answered: { c: "#4f9e78", t: "#eaf1ed", label: "Question answered", ic: '<path d="M20 6 9 17l-5-5"/>' },
  };

  const RK: Record<AccessRequestKind, { label: string; c: string; t: string; b: string; ic: string }> = {
    mcp_server: { label: "MCP server", c: "#2f6e60", t: "#e2f0ec", b: "#c7e2da", ic: '<path d="M4 5h16v6H4z"/><path d="M4 13h16v6H4z"/><path d="M7.5 8h.01"/><path d="M7.5 16h.01"/>' },
    skill: { label: "Skill install", c: "#5847b8", t: "#eeeafb", b: "#d8cff3", ic: '<path d="M4 4h7v7H4z"/><path d="M13 4h7v7h-7z"/><path d="M4 13h7v7H4z"/><path d="M13 13h7v7h-7z"/>' },
    permission_mode: { label: "Permission mode", c: "#b14322", t: "#fbe7e0", b: "#efc0ad", ic: '<path d="M12 3l7 3v6c0 4-3 6.7-7 8-4-1.3-7-4-7-8V6z"/><path d="M12 9v4"/><path d="M12 16h.01"/>' },
    cli_tool: { label: "Host tool", c: "#9a6e1e", t: "#fbf1dd", b: "#ecd9ae", ic: '<path d="M4 5h16v14H4z"/><path d="M8 10l3 2-3 2"/><path d="M13 15h4"/>' },
    env_var: { label: "Credential", c: "#4a6fa8", t: "#e7eef7", b: "#cbd9ec", ic: '<path d="M10 14a4 4 0 1 1 4-4"/><path d="M12.5 11.5 20 19"/><path d="M17 16l2 2"/>' },
  };

  const STATUS_PILL: Record<GoalStatus | "planning", [string, string, string, string, boolean]> = {
    active: ["#e3f1ec", "#bfe0d6", "#2f6e60", "active", false],
    paused: ["#f1ece3", "#e2d6c4", "#8a7560", "paused", false],
    review: ["#fbe7e0", "#efbfab", "#b14322", "needs review", true],
    done: ["#eaf1ed", "#cfe3d8", "#3f7a5f", "done", false],
    abandoned: ["#f1ece6", "#e2d8cc", "#9a8e80", "abandoned", false],
    planning: ["#eeeafb", "#d8cff3", "#5847b8", "planning", false],
  };

  // ---- data ------------------------------------------------------------------

  async function refreshAll() {
    try {
      const [gs, pending, failed] = await Promise.all([
        listGoals(),
        listAccessRequests("", "pending"),
        listAccessRequests("", "failed"),
      ]);
      goals = gs;
      openRequests = [...failed, ...pending];
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Couldn't load goals.";
    }
  }

  async function openGoal(id: string) {
    try {
      const next = await getGoal(id);
      detail = next;
      moreEvents = next.events.length >= PAGE;
      seedRecoveryTarget(next);
      questionAnswers = {};
      feedbackOpen = false;
      feedbackBody = "";
      editingFeedbackID = 0;
      editingFeedbackBody = "";
      closeRun();
      view = "detail";
      menuOpen = false;
      void ensureProfiles();
      scrollTop();
    } catch (e) {
      error = e instanceof Error ? e.message : "Couldn't load the goal.";
    }
  }

  async function refreshDetail() {
    if (!detail) return;
    try {
      const next = await getGoal(detail.goal.ID);
      moreEvents = next.events.length >= PAGE;
      detail = next;
      seedRecoveryTarget(next);
    } catch {
      // Deleted elsewhere: fall back to the list.
      detail = null;
      view = "list";
    }
  }

  async function ensureProfiles() {
    if (profiles.length > 0) return;
    try {
      profiles = await listProfiles();
    } catch {
      profiles = [];
    }
  }

  function pendingRateLimitOf(d: GoalDetail | null): GoalRateLimitBlock | null {
    return d?.rate_limit_blocks.find((b) => b.Status === "pending") ?? null;
  }

  function seedRecoveryTarget(d: GoalDetail) {
    const block = pendingRateLimitOf(d);
    if (!block) {
      recoveryBlockID = "";
      return;
    }
    if (recoveryBlockID === block.ID) return;
    recoveryBlockID = block.ID;
    recoveryProvider = d.goal.Provider || block.Provider || "";
    recoveryProfile = d.goal.Profile || block.Profile || "";
    recoveryModel = d.goal.Model || block.Model || "";
    recoveryEffort = d.goal.Effort || block.Effort || "";
  }

  async function loadMoreEvents() {
    if (!detail || loadingMore || detail.events.length === 0) return;
    loadingMore = true;
    try {
      const before = detail.events[detail.events.length - 1].ID;
      const older = await listGoalEvents(detail.goal.ID, PAGE, before);
      moreEvents = older.length >= PAGE;
      detail = { ...detail, events: [...detail.events, ...older] };
    } finally {
      loadingMore = false;
    }
  }

  onMount(() => {
    void refreshAll().then(() => {
      if (target) {
        void openGoal(target);
        onConsumeTarget();
      }
    });
    const unsubscribe = live.subscribe((msg) => {
      if (msg.type !== "goal_event" || !msg.goal_event) return;
      const ev = msg.goal_event;
      // tool_use events stream rapidly while a goal works. When the goal is open,
      // append the new one in place rather than refetching the whole page for
      // each call; the list-level rollups don't change on a tool call anyway.
      if (ev.Kind === "tool_use" && ev.ID) {
        if (detail && ev.GoalID === detail.goal.ID && !detail.events.some((e) => e.ID === ev.ID)) {
          detail = { ...detail, events: [ev, ...detail.events] };
        }
		if (runDetail && ev.RunID === runDetail.run.ID && !runDetail.events.some((e) => e.ID === ev.ID)) {
		  runDetail = { ...runDetail, events: [...runDetail.events, ev] };
		}
        return;
      }
      void refreshAll();
      if (detail && ev.GoalID === detail.goal.ID) void refreshDetail();
	  if (runDetail && ev.RunID === runDetail.run.ID) {
		void getGoalRun(ev.GoalID, ev.RunID).then((next) => (runDetail = next)).catch(() => {});
	  }
    });
    return unsubscribe;
  });

  // Deep-links after mount (a toast/push tap while the page is already open).
  $effect(() => {
    if (target) {
      void openGoal(target);
      onConsumeTarget();
    }
  });

  // ---- derived list groups ----------------------------------------------------

  const openReqsByGoal = $derived.by(() => {
    const map = new Map<string, AccessRequest[]>();
    for (const r of openRequests) {
      map.set(r.GoalID, [...(map.get(r.GoalID) ?? []), r]);
    }
    return map;
  });

  const rateLimitByGoal = $derived.by(() => {
    const map = new Map<string, GoalRateLimitBlock>();
    for (const g of goals) {
      if (g.pending_rate_limit) map.set(g.ID, g.pending_rate_limit);
    }
    return map;
  });

  const questionByGoal = $derived.by(() => {
    const map = new Map<string, AgentQuestion>();
    for (const g of goals) {
      if (g.pending_question) map.set(g.ID, g.pending_question);
    }
    return map;
  });

  const needsAttention = (g: Goal) => g.Status === "review" || (openReqsByGoal.get(g.ID)?.length ?? 0) > 0 || rateLimitByGoal.has(g.ID) || questionByGoal.has(g.ID);
  const attention = $derived(goals.filter((g) => needsAttention(g) && g.Status !== "done" && g.Status !== "abandoned" && g.Status !== "paused"));
  const activeRest = $derived(goals.filter((g) => g.Status === "active" && !needsAttention(g)));
  const paused = $derived(goals.filter((g) => g.Status === "paused"));
  const closed = $derived(goals.filter((g) => g.Status === "done" || g.Status === "abandoned"));

  // ---- helpers -----------------------------------------------------------------

  function parseTime(raw: string): Date | null {
    if (!raw) return null;
    // SQLite datetime('now') is "YYYY-MM-DD HH:MM:SS" in UTC (no zone marker);
    // Go-written stamps are RFC3339. Normalize both.
    let iso = raw.includes("T") ? raw : raw.replace(" ", "T");
    if (!/[zZ]|[+-]\d\d:\d\d$/.test(iso)) iso += "Z";
    const d = new Date(iso);
    return isNaN(d.getTime()) ? null : d;
  }

  function relTime(raw: string): string {
    const d = parseTime(raw);
    if (!d) return "—";
    const diff = d.getTime() - Date.now();
    const abs = Math.abs(diff);
    const units: [number, string][] = [
      [7 * 24 * 3600_000, "w"],
      [24 * 3600_000, "d"],
      [3600_000, "h"],
      [60_000, "m"],
    ];
    let label = "now";
    for (const [ms, suffix] of units) {
      if (abs >= ms) {
        label = `${Math.round(abs / ms)}${suffix}`;
        break;
      }
    }
    if (label === "now") return diff >= 0 ? "soon" : "just now";
    return diff >= 0 ? `in ${label}` : `${label} ago`;
  }

  function cadenceLabel(g: Goal): string {
    return g.ReviewEvery ? `every ${g.ReviewEvery}` : "manual only";
  }

  function nextLabel(g: Goal): string {
    if (g.Status === "review") return "awaiting you";
    if (g.Status === "paused") return "paused";
    if (g.Status === "done" || g.Status === "abandoned") return "—";
    if (!g.NextReviewAt) return "manual";
    return relTime(g.NextReviewAt);
  }

  // reviewButtonLabel names the review trigger by the next scheduled review
  // ("Next review on Friday"); manual-only and overdue goals fall back to
  // "Review now". Clicking it always starts a review immediately.
  function reviewButtonLabel(g: Goal): string {
    const d = parseTime(g.NextReviewAt);
    if (!d || d.getTime() <= Date.now()) return "Review now";
    const startOfDay = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
    const days = Math.round((startOfDay(d) - startOfDay(new Date())) / 86_400_000);
    if (days === 0) return "Next review today";
    if (days === 1) return "Next review tomorrow";
    if (days < 7) return `Next review on ${d.toLocaleDateString(undefined, { weekday: "long" })}`;
    return `Next review ${d.toLocaleDateString(undefined, { month: "short", day: "numeric" })}`;
  }

  function metricPct(m: GoalMetric): number {
    if (!m.target) return 0;
    return Math.max(0, Math.min(100, Math.round((m.current / m.target) * 100)));
  }

  function metricMet(m: GoalMetric): boolean {
    return m.target > 0 && m.current >= m.target;
  }

  function metricValue(m: GoalMetric): string {
    const unit = m.unit ?? "";
    return `${m.current}${unit} / ${m.target}${unit}`;
  }

  function payloadOf(req: AccessRequest): Record<string, string> {
    try {
      return JSON.parse(req.Payload) as Record<string, string>;
    } catch {
      return {};
    }
  }

  function isInstallable(req: AccessRequest): boolean {
    return req.Kind === "cli_tool" && !!payloadOf(req).installer;
  }

  // installCommand renders the exact command shape Podiom will run on approval
  // (workspace-tool-installs spec §3); <tools> stands for the agent's tool dir.
  function installCommand(req: AccessRequest): string {
    const p = payloadOf(req);
    const ver = p.version ?? "";
    switch (p.installer) {
      case "npm":
        return `npm install -g --prefix <tools>/npm ${p.package}${ver ? `@${ver}` : ""}`;
      case "uv":
        return `UV_TOOL_DIR=<tools>/uv UV_TOOL_BIN_DIR=<tools>/bin uv tool install ${p.package}${ver ? `==${ver}` : ""}`;
      case "go":
        return `GOBIN=<tools>/bin go install ${p.package}${p.package?.includes("@") ? "" : `@${ver || "latest"}`}`;
      case "binary":
        return `download ${p.url} → verify sha256 → <tools>/bin/${p.tool}`;
      default:
        return "";
    }
  }

  function payloadEntries(req: AccessRequest): [string, string][] {
    try {
      const obj = JSON.parse(req.Payload) as Record<string, string>;
      return Object.entries(obj);
    } catch {
      return [];
    }
  }

  function metricDelta(ev: GoalEvent): string {
    try {
      const obj = JSON.parse(ev.Payload) as { updates?: { name: string; from: number; to: number }[] };
      const updates = obj.updates ?? [];
      return updates.map((u) => `${u.name}: ${u.from} → ${u.to}`).join("  ·  ");
    } catch {
      return "";
    }
  }

  function eventTitle(ev: GoalEvent): string {
    if (ev.Kind === "metric_update") return "Metrics moved";
    if (ev.Body) {
      const firstLine = ev.Body.split("\n", 1)[0];
      return firstLine.length > 120 ? firstLine.slice(0, 120) + "…" : firstLine;
    }
    return EK[ev.Kind]?.label ?? ev.Kind;
  }

  function eventBody(ev: GoalEvent): string {
    if (!ev.Body || ev.Kind === "metric_update") return "";
    const rest = ev.Body.split("\n").slice(1).join("\n").trim();
    return rest;
  }

  function canEditFeedback(ev: GoalEvent): boolean {
    if (!detail || ev.Kind !== "user_feedback") return false;
    return !detail.events.some((other) => other.ID > ev.ID && (other.Kind === "planning_started" || other.Kind === "review_started"));
  }

  // ---- tool_use audit rendering ---------------------------------------------

  type ToolPayload = {
    tool: string;
    summary: string;
    read_only: boolean;
    input: string;
    input_truncated: boolean;
  };

  function toolPayload(ev: GoalEvent): ToolPayload {
    try {
      const p = JSON.parse(ev.Payload) as Partial<ToolPayload>;
      return {
        tool: p.tool ?? ev.Kind,
        summary: p.summary ?? "",
        read_only: p.read_only ?? false,
        input: p.input ?? "",
        input_truncated: p.input_truncated ?? false,
      };
    } catch {
      return { tool: ev.Kind, summary: "", read_only: false, input: "", input_truncated: false };
    }
  }

  function canViewRun(ev: GoalEvent): boolean {
    return !!ev.RunID && !["created", "user_feedback", "access_decided", "status_change", "rate_limit_resolved", "question_answered"].includes(ev.Kind);
  }

  async function openRun(ev: GoalEvent) {
    if (!detail || !canViewRun(ev)) return;
    selectedRunEvent = ev;
    runDetail = null;
    runError = null;
    runLoading = true;
    try {
      runDetail = await getGoalRun(detail.goal.ID, ev.RunID);
    } catch (e) {
      runError = e instanceof Error ? e.message : "Couldn't load this run.";
    } finally {
      runLoading = false;
    }
  }

  function closeRun() {
    selectedRunEvent = null;
    runDetail = null;
    runError = null;
    runLoading = false;
  }

  function runKindLabel(kind: GoalRunDetail["run"]["Kind"]): string {
    switch (kind) {
      case "planning": return "Planning run";
      case "review": return "Review run";
      case "task": return "Task run";
      case "schedule": return "Schedule run";
      default: return "Goal run";
    }
  }

  function messageLabel(message: Message): string {
    if (message.Role === "user") return "Run request";
    if (message.Kind === "reasoning") return "Reasoning";
    if (message.Kind === "error") return "Run error";
    return "Agent response";
  }

  // A rendered timeline row is either a normal event or a collapsed run of
  // consecutive read-only tool calls (Read/Grep/WebFetch…), which the goal chain
  // can emit in bulk. Keeping them grouped keeps the audit trail scannable while
  // still recording every call.
  type TimelineItem =
    | { kind: "event"; ev: GoalEvent }
    | { kind: "toolGroup"; id: number; events: GoalEvent[] };

  const timelineItems = $derived.by<TimelineItem[]>(() => {
    const evs = detail?.events ?? [];
    const items: TimelineItem[] = [];
    let group: GoalEvent[] = [];
    const flush = () => {
      if (group.length === 1) {
        items.push({ kind: "event", ev: group[0] });
      } else if (group.length > 1) {
        items.push({ kind: "toolGroup", id: group[0].ID, events: group });
      }
      group = [];
    };
    for (const ev of evs) {
      if (ev.Kind === "tool_use" && toolPayload(ev).read_only) {
		if (group.length > 0 && group[0].RunID !== ev.RunID) flush();
        group.push(ev);
      } else {
        flush();
        items.push({ kind: "event", ev });
      }
    }
    flush();
    return items;
  });

  let expandedGroups = $state(new Set<number>());
  function toggleGroup(id: number) {
    const next = new Set(expandedGroups);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    expandedGroups = next;
  }

  // isPlanning: the goal was just created and the decomposition session is the
  // newest thing on the timeline — show the "agent is planning now" banner.
  const isPlanning = $derived(
    detail !== null &&
      detail.goal.Status === "active" &&
      detail.events.length > 0 &&
      (detail.events[0].Kind === "planning_started" || detail.events[0].Kind === "created"),
  );

  // The next-step panel answers "what will the agent do?", so it is hidden
  // whenever that question has no meaningful answer: while planning is still
  // deciding, once completion is proposed, and on a finished goal. The empty
  // placeholder is active-only, because on a paused goal no review is coming to
  // fill it in.
  const nextStepVisible = $derived(
    detail !== null &&
      !isPlanning &&
      detail.goal.Status !== "review" &&
      detail.goal.Status !== "done" &&
      detail.goal.Status !== "abandoned" &&
      (detail.goal.NextStep.trim() !== "" || detail.goal.Status === "active"),
  );

  // A review run is in flight for the open goal — from click (reviewBusy)
  // until the finish ping refreshes the detail and clears running_run.
  const reviewRunning = $derived(
    detail !== null && (reviewBusy === detail.goal.ID || detail.running_run?.Kind === "review"),
  );

  // Open = needs the user (pending/failed) or is mid-install (approved
  // installable cli_tool — the async grant hasn't landed executed/failed yet).
  const detailOpenReqs = $derived(
    detail === null
      ? []
      : detail.access_requests.filter(
          (r) => r.Status === "pending" || r.Status === "failed" || (r.Status === "approved" && isInstallable(r)),
        ),
  );

  function scrollTop() {
    requestAnimationFrame(() => {
      document.querySelector(".goals-scroll")?.scrollTo({ top: 0 });
    });
  }

  // ---- actions ------------------------------------------------------------------

  async function transition(goal: Goal, status: GoalStatus, note = "") {
    busy = goal.ID;
    try {
      await patchGoal(goal.ID, { status, status_note: note });
      await Promise.all([refreshAll(), detail?.goal.ID === goal.ID ? refreshDetail() : Promise.resolve()]);
      void live.refreshGoalAttention();
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Status change failed.";
    } finally {
      busy = "";
    }
  }

  async function reviewNow(goal: Goal) {
    reviewBusy = goal.ID;
    try {
      await runGoalReview(goal.ID);
      // The review starts in the background; hold the spinner until its run
      // is visible on the detail response, then running_run takes over.
      for (let i = 0; i < 10 && detail?.goal.ID === goal.ID; i++) {
        await refreshDetail();
        if (detail?.running_run) break;
        await new Promise((r) => setTimeout(r, 300));
      }
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Couldn't start the review.";
    } finally {
      reviewBusy = "";
    }
  }

  async function submitFeedback(goal: Goal) {
    const body = feedbackBody.trim();
    if (!body || feedbackBusy) return;
    feedbackBusy = true;
    try {
      await addGoalFeedback(goal.ID, body);
      feedbackBody = "";
      feedbackOpen = false;
      await refreshDetail();
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Couldn't add feedback.";
    } finally {
      feedbackBusy = false;
    }
  }

  function startEditFeedback(ev: GoalEvent) {
    editingFeedbackID = ev.ID;
    editingFeedbackBody = ev.Body;
  }

  function cancelEditFeedback() {
    editingFeedbackID = 0;
    editingFeedbackBody = "";
  }

  async function submitFeedbackEdit(goal: Goal, ev: GoalEvent) {
    const body = editingFeedbackBody.trim();
    if (!body || editingFeedbackBusy) return;
    editingFeedbackBusy = true;
    try {
      await updateGoalFeedback(goal.ID, ev.ID, body);
      cancelEditFeedback();
      await refreshDetail();
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Couldn't update feedback.";
    } finally {
      editingFeedbackBusy = false;
    }
  }

  async function confirmAbandon() {
    if (!pendingAbandon) return;
    confirmBusy = true;
    try {
      await transition(pendingAbandon, "abandoned", "Abandoned by you.");
      pendingAbandon = null;
    } finally {
      confirmBusy = false;
    }
  }

  async function confirmDelete() {
    if (!pendingDelete) return;
    confirmBusy = true;
    try {
      await deleteGoal(pendingDelete.ID);
      pendingDelete = null;
      detail = null;
      view = "list";
      await refreshAll();
      void live.refreshGoalAttention();
    } catch (e) {
      error = e instanceof Error ? e.message : "Delete failed.";
    } finally {
      confirmBusy = false;
    }
  }

  // ---- access-request dialog -------------------------------------------------

  function dialogCopy(req: AccessRequest, action: "approve" | "deny"): { title: string; body: string; warn: boolean; confirmLabel: string } {
    const agent = req.AgentName || "the agent";
    if (action === "deny") {
      return {
        title: `Deny — ${RK[req.Kind].label}`,
        body: `This request will be denied. Add a note to tell ${agent} why — it's relayed at the next review so it can adjust its plan.`,
        warn: false,
        confirmLabel: "Deny request",
      };
    }
    switch (req.Kind) {
      case "mcp_server":
        return { title: "Approve — assign MCP server", body: `Approving assigns this MCP server to ${agent} and runs the assignment now. It takes effect immediately.`, warn: false, confirmLabel: "Approve" };
      case "skill":
        return { title: "Approve — install skill", body: `Approving installs this skill from the marketplace and makes it available to ${agent} right away.`, warn: false, confirmLabel: "Approve" };
      case "permission_mode":
        return { title: "Grant autonomous mode", body: `Autonomous (yolo) mode lets ${agent} run tools, edit files, and execute commands without asking for each step — everywhere, not just on this goal. Only grant this if you trust it to act unattended. You can revoke it anytime.`, warn: true, confirmLabel: "Grant autonomous mode" };
      case "cli_tool":
        if (isInstallable(req)) {
          return { title: "Approve — install workspace tool", body: `Approving installs this tool into ${agent}'s own workspace now — the exact command is shown below, and only ${agent} sees the tool on its PATH. The result lands on the goal timeline when the install finishes.`, warn: false, confirmLabel: "Approve & install" };
        }
        return { title: "Acknowledge — host tool", body: `Podiom can't install host-wide tools. Approving marks this acknowledged and shows you the command to run yourself; ${agent} resumes once it detects the tool.`, warn: false, confirmLabel: "Acknowledge" };
      case "env_var":
        return { title: `Grant credential — ${payloadOf(req).var_name || "env var"}`, body: `Enter the value below and Podiom stores it on this machine (readable only by your user) and injects it into ${agent}'s environment on future runs — the value is never shown again. Leave it empty to just acknowledge; you can instead set the variable yourself where podiomd runs.`, warn: false, confirmLabel: "Grant" };
    }
  }

  async function confirmDialog() {
    if (!dialog) return;
    dialog = { ...dialog, busy: true };
    try {
      if (dialog.action === "approve") {
        await approveAccessRequest(dialog.req.ID, dialog.note, dialog.secret);
      } else {
        await denyAccessRequest(dialog.req.ID, dialog.note);
      }
      dialog = null;
      await Promise.all([refreshAll(), refreshDetail()]);
      void live.refreshGoalAttention();
    } catch (e) {
      error = e instanceof Error ? e.message : "Decision failed.";
      dialog = null;
    }
  }

  async function resolveRateLimit(block: GoalRateLimitBlock) {
    recoveryBusy = true;
    try {
      await resolveGoalRateLimit(block.ID, {
        provider: recoveryProvider,
        profile: recoveryProfile,
        model: recoveryModel,
        effort: recoveryEffort,
        retry: true,
      });
      await Promise.all([refreshAll(), refreshDetail()]);
      void live.refreshGoalAttention();
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Couldn't retry the goal.";
    } finally {
      recoveryBusy = false;
    }
  }

  // ---- create -------------------------------------------------------------------

  async function openCreate() {
    view = "create";
    cAgent = cAgent || agents[0]?.Name || "";
    scrollTop();
    try {
      [projects, profiles] = await Promise.all([listProjects(), listProfiles()]);
    } catch {
      projects = [];
      profiles = [];
    }
  }

  const canSubmit = $derived(cTitle.trim() !== "" && cAgent !== "");
  const selectedCreateAgent = $derived(agents.find((a) => a.Name === cAgent) ?? null);

  async function submitCreate() {
    if (!canSubmit || cBusy) return;
    cBusy = true;
    try {
      const metrics: GoalMetric[] = cMetrics
        .filter((m) => m.name.trim() !== "")
        .map((m) => ({ name: m.name.trim(), target: Number(m.target) || 0, current: 0, unit: m.unit.trim() || undefined }));
      const goal = await createGoal({
        title: cTitle.trim(),
        description: cDesc.trim(),
        success_criteria: cCriteria.trim(),
        metrics,
        review_every: cCadence,
        lead_agent: cAgent,
        project_id: cProject,
        provider: cProvider,
        profile: cProfile,
        model: cModel,
        effort: cEffort,
      });
      cTitle = "";
      cDesc = "";
      cCriteria = "";
      cMetrics = [{ name: "", target: "", unit: "" }];
      cProject = "";
      cProvider = "";
      cProfile = "";
      cModel = "";
      cEffort = "";
      await refreshAll();
      await openGoal(goal.ID);
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Couldn't create the goal.";
    } finally {
      cBusy = false;
    }
  }

  const statusPill = (g: Goal) => STATUS_PILL[g.Status] ?? STATUS_PILL.active;
  const selectedDetailAgent = $derived.by(() => {
    const lead = detail?.goal.LeadAgent;
    return lead ? (agents.find((a) => a.Name === lead) ?? null) : null;
  });
  const detailPendingRateLimit = $derived(pendingRateLimitOf(detail));
  const detailPendingQuestion = $derived<AgentQuestion | null>(detail?.pending_question ?? null);

  // ---- deferred-question answering (mirrors the chat question card) ----------
  const qSelected = (itemId: string, label: string) => (questionAnswers[itemId] ?? []).includes(label);

  function qToggle(item: { id: string; multi_select?: boolean }, label: string) {
    const cur = questionAnswers[item.id] ?? [];
    if (item.multi_select) {
      questionAnswers = { ...questionAnswers, [item.id]: cur.includes(label) ? cur.filter((l) => l !== label) : [...cur, label] };
    } else {
      questionAnswers = { ...questionAnswers, [item.id]: [label] };
    }
  }

  function qSetFree(itemId: string, value: string) {
    questionAnswers = { ...questionAnswers, [itemId]: value ? [value] : [] };
  }

  const qReady = (pq: AgentQuestion) => pq.Questions.every((item) => (questionAnswers[item.id] ?? []).some((a) => a.trim() !== ""));

  async function submitQuestionAnswer(pq: AgentQuestion) {
    if (questionBusy || !qReady(pq)) return;
    questionBusy = true;
    try {
      await answerAgentQuestion(pq.ID, questionAnswers);
      questionAnswers = {};
      await refreshDetail();
      await refreshAll();
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Couldn't submit your answer.";
    } finally {
      questionBusy = false;
    }
  }

  const reqStatusChip: Record<string, [string, string, string, string]> = {
    pending: ["#fbf1dd", "#ecd9ae", "#9a6e1e", "pending"],
    failed: ["#fbeae0", "#f2d6c5", "#b14e2a", "grant failed"],
    approved: ["#eaf1ed", "#cfe3d8", "#3f7a5f", "approved"],
    executed: ["#e3f1ec", "#bfe0d6", "#2f6e60", "granted"],
    denied: ["#f1ece6", "#e2d8cc", "#8a7f73", "denied"],
  };
</script>

<div class="goals-scroll">
  {#if error}
    <div class="page-error">{error} <button class="dismiss" onclick={() => (error = null)}>✕</button></div>
  {/if}

  <!-- ================= LIST ================= -->
  {#if view === "list"}
    <div class="page">
      <div class="page-head">
        <div class="page-title-wrap">
          <div class="page-title">Goals</div>
          <div class="page-sub">
            Set an outcome, pick a lead agent, and walk away. Each agent plans the work, runs unattended reviews, and
            comes back to you only when it needs a decision or believes it’s done.
          </div>
        </div>
        <button class="btn-primary" onclick={openCreate}>+ New goal</button>
      </div>

      {#if goals.length === 0}
        <div class="empty">
          <div class="empty-icon">
            <svg width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="#2f6e60" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="4.5"/><circle cx="12" cy="12" r="0.7"/></svg>
          </div>
          <div class="empty-title">No goals yet</div>
          <div class="empty-sub">
            A goal is an outcome, not a task list. Describe what “done” looks like and hand it to one agent — it
            decomposes the work into tasks and schedules, checks in on its own cadence, and asks for access when it’s
            blocked.
          </div>
          <div class="empty-steps">
            <div class="empty-step"><span class="step-n mono">1</span>Write the outcome</div>
            <div class="empty-step"><span class="step-n mono">2</span>Pick a lead agent</div>
            <div class="empty-step"><span class="step-n mono">3</span>Walk away</div>
          </div>
          <button class="btn-primary big" onclick={openCreate}>Create your first goal</button>
        </div>
      {:else}
        <div class="groups">
          {#each [
            { label: "Needs you", tone: "attn", items: attention },
            { label: "Active", tone: "", items: activeRest },
            { label: "Paused", tone: "", items: paused },
            { label: "Done & abandoned", tone: "dim", items: closed },
          ] as group}
            {#if group.items.length > 0}
              <div>
                <div class="group-head">
                  <span class="group-label mono" class:attn={group.tone === "attn"} class:dim={group.tone === "dim"}>{group.label}</span>
                  <span class="group-rule"></span>
                </div>
                <div class="cards">
                  {#each group.items as g (g.ID)}
                    {@const [bg, bd, tc, lbl, pulse] = statusPill(g)}
                    {@const pend = openReqsByGoal.get(g.ID) ?? []}
                    {@const rate = rateLimitByGoal.get(g.ID)}
                    {@const question = questionByGoal.get(g.ID)}
                    {@const failed = pend.some((r) => r.Status === "failed")}
                    {@const primary = g.Metrics[0]}
                    <button
                      class="card"
                      class:review-ring={g.Status === "review"}
                      class:attn-ring={g.Status !== "review" && (pend.length > 0 || !!rate || !!question)}
                      class:dim={g.Status === "done" || g.Status === "abandoned"}
                      onclick={() => openGoal(g.ID)}>
                      <div class="card-top">
                        <div class="card-meta">
                          <span class="pill mono" style="background:{bg};border-color:{bd};color:{tc}">
                            {#if pulse}<span class="pill-dot" style="background:{tc};box-shadow:0 0 0 3px {bg}"></span>{/if}
                            {lbl}
                          </span>
                          <span class="agent-chip">
                            <AgentAvatar name={g.LeadAgent} size={20} radius={6} fontSize={9} />
                            {g.LeadAgent}
                          </span>
                          {#if g.ProjectID}<span class="proj mono">◆ {g.ProjectID}</span>{/if}
                        </div>
                        <svg class="chev" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#cdbfad" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6"/></svg>
                      </div>
                      <div class="card-title">{g.Title}</div>

                      {#if g.Usage}
                        <div class="card-usage"><UsageBar usage={g.Usage} compact /></div>
                      {/if}

                      {#if g.Status === "review" || pend.length > 0 || rate || question}
                        <div class="card-attn" class:hot={g.Status === "review"}>
                          <span class="attn-dot" class:hot={g.Status === "review"}></span>
                          {#if g.Status === "review"}
                            {g.LeadAgent} proposed completion — confirm or reopen
                          {:else if question}
                            {g.LeadAgent} asked a question — answer to continue
                          {:else if rate}
                            Rate limit reached — choose a model to continue
                          {:else if failed}
                            {pend.length} request{pend.length > 1 ? "s" : ""} need you · an auto-grant failed
                          {:else}
                            {pend.length} access request{pend.length > 1 ? "s" : ""} waiting
                          {/if}
                        </div>
                      {/if}

                      {#if primary}
                        <div class="card-metric">
                          <div class="metric-row">
                            <span class="metric-name">{primary.name}</span>
                            <span class="metric-val mono">{metricValue(primary)}</span>
                          </div>
                          <div class="bar"><div class="fill" class:met={metricMet(primary)} style="width:{metricPct(primary)}%"></div></div>
                          {#if g.Metrics.length > 1}
                            <div class="metric-extra mono">+{g.Metrics.length - 1} more metric{g.Metrics.length > 2 ? "s" : ""}</div>
                          {/if}
                        </div>
                      {/if}

                      <div class="card-foot">
                        <span class="foot-next">
                          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>
                          next review&nbsp;<b>{nextLabel(g)}</b>
                        </span>
                        {#if rate}
                          <span class="pend-chip mono failed">rate limit</span>
                        {:else if pend.length > 0}
                          <span class="pend-chip mono" class:failed>{pend.length} pending</span>
                        {/if}
                        <span class="foot-updated mono">updated {relTime(g.UpdatedAt)}</span>
                      </div>
                    </button>
                  {/each}
                </div>
              </div>
            {/if}
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- ================= DETAIL ================= -->
  {#if view === "detail" && detail}
    {@const g = detail.goal}
    {@const [bg, bd, tc, lbl, pulse] = isPlanning ? STATUS_PILL.planning : statusPill(g)}
    <div class="page narrow">
      <button class="back" onclick={() => { view = "list"; void refreshAll(); }}>
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 6l-6 6 6 6"/></svg>All goals
      </button>

      <!-- header -->
      <div class="detail-head">
        <div class="detail-head-main">
          <div class="card-meta" style="margin-bottom:9px">
            <span class="pill mono lg" style="background:{bg};border-color:{bd};color:{tc}">
              {#if pulse}<span class="pill-dot" style="background:{tc};box-shadow:0 0 0 3px {bg}"></span>{/if}
              {isPlanning ? "planning" : lbl}
            </span>
            <span class="agent-chip lg">
              <AgentAvatar name={g.LeadAgent} size={22} radius={7} fontSize={10} />
              {g.LeadAgent}
            </span>
            {#if g.ProjectID}<span class="proj mono">◆ {g.ProjectID}</span>{/if}
            <span class="yolo-pill mono" title="This goal runs autonomously with full access (yolo): its sessions, tasks, and schedules run shell commands, edit files, and install software without per-action approval. Every tool call is recorded on the timeline below.">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l7 3v6c0 4-3 6.7-7 8-4-1.3-7-4-7-8V6z"/><path d="M12 9v4"/><path d="M12 16h.01"/></svg>
              autonomous · full access
            </span>
          </div>
          <div class="detail-title">{g.Title}</div>
          <div class="detail-meta">
            <span class="meta-item">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--faint)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>
              reviews {cadenceLabel(g)}
            </span>
            <span class="meta-item next">next review <b>{nextLabel(g)}</b></span>
          </div>
          {#if detail.usage}
            <div class="detail-usage">
              <span class="detail-usage-label">token usage</span>
              <div class="detail-usage-bars"><UsageBar usage={detail.usage} /></div>
            </div>
          {/if}
        </div>
        <div class="detail-actions">
          {#if g.Status === "active"}
            {#if reviewRunning}
              <button class="btn teal reviewing" disabled>
                <span class="btn-spinner" aria-hidden="true"></span>Reviewing…
              </button>
            {:else}
              <button class="btn teal" disabled={busy === g.ID} title="Run a review now" onclick={() => reviewNow(g)}>
                {reviewButtonLabel(g)}
              </button>
            {/if}
          {/if}
          {#if g.Status === "active" || g.Status === "paused"}
            <button class="btn" disabled={busy === g.ID} onclick={() => transition(g, g.Status === "paused" ? "active" : "paused", g.Status === "paused" ? "Resumed by you." : "Paused by you.")}>
              {g.Status === "paused" ? "Resume" : "Pause"}
            </button>
          {/if}
          {#if g.Status === "done" || g.Status === "abandoned"}
            <button class="btn" disabled={busy === g.ID} onclick={() => transition(g, "active", "Reopened by you.")}>Reopen</button>
          {/if}
          <div class="menu-wrap">
            <button class="btn icon" title="More" onclick={() => (menuOpen = !menuOpen)}>
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></svg>
            </button>
            {#if menuOpen}
              <div class="menu">
                {#if g.Status !== "done" && g.Status !== "abandoned"}
                  <button class="menu-item" onclick={() => { menuOpen = false; pendingAbandon = g; }}>Abandon goal</button>
                {/if}
                <button class="menu-item danger" onclick={() => { menuOpen = false; pendingDelete = g; }}>Delete goal</button>
              </div>
            {/if}
          </div>
        </div>
      </div>

      <div class="stack">
        <!-- PLANNING banner -->
        {#if isPlanning}
          <div class="banner planning">
            <div class="banner-icon violet">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#5847b8" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18h6"/><path d="M10 21h4"/><path d="M12 3a6 6 0 0 0-3.8 10.6c.6.6.8 1 .8 1.9h6c0-.9.2-1.3.8-1.9A6 6 0 0 0 12 3z"/></svg>
            </div>
            <div>
              <div class="banner-title violet-ink">{g.LeadAgent} is planning this goal now<span class="dots"><span class="dot"></span><span class="dot" style="animation-delay:.15s"></span><span class="dot" style="animation-delay:.3s"></span></span></div>
              <div class="banner-sub violet-sub">
                Decomposing the goal into tasks and schedules. This runs in the background — metrics, the timeline, and
                access requests will fill in as the agent makes progress. You can safely leave.
              </div>
            </div>
          </div>
        {/if}

        <!-- PENDING QUESTION banner: the agent is blocked on a decision -->
        {#if detailPendingQuestion}
          <div class="banner goal-question">
            <div class="banner-icon amber">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#9a6e1e" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9.1 9a3 3 0 1 1 4 2.8c-.8.4-1.1 1-1.1 2"/><path d="M12 17h.01"/></svg>
            </div>
            <div class="recovery-main">
              <div class="banner-title amber-ink">{g.LeadAgent} needs an answer to continue</div>
              <div class="banner-sub amber-sub">Reviews are paused until you answer. Your answer reaches the agent on its next session.</div>
              <div class="goal-q-body">
                {#each detailPendingQuestion.Questions as item}
                  <div class="goal-q-block">
                    {#if item.header}<div class="goal-q-header">{item.header}</div>{/if}
                    <div class="goal-q-text">{item.question}</div>
                    {#if item.options && item.options.length > 0}
                      <div class="goal-q-options">
                        {#each item.options as option}
                          <button
                            class="goal-q-option"
                            class:sel={qSelected(item.id, option.label)}
                            onclick={() => qToggle(item, option.label)}
                          >
                            <span class="goal-q-dot">{item.multi_select ? (qSelected(item.id, option.label) ? "✓" : "") : ""}</span>
                            <span class="goal-q-option-text">
                              <span>{option.label}</span>
                              {#if option.description}<small>{option.description}</small>{/if}
                            </span>
                          </button>
                        {/each}
                      </div>
                    {:else}
                      <input
                        class="goal-q-free"
                        type={item.is_secret ? "password" : "text"}
                        placeholder="Your answer"
                        value={(questionAnswers[item.id] ?? [])[0] ?? ""}
                        oninput={(e) => qSetFree(item.id, e.currentTarget.value)}
                      />
                    {/if}
                  </div>
                {/each}
                <button class="btn-primary" disabled={questionBusy || !qReady(detailPendingQuestion)} onclick={() => submitQuestionAnswer(detailPendingQuestion)}>
                  {questionBusy ? "Sending…" : "Send answer"}
                </button>
              </div>
            </div>
          </div>
        {/if}

        <!-- RATE LIMIT RECOVERY banner -->
        {#if detailPendingRateLimit}
          <div class="banner rate-limit">
            <div class="banner-icon amber">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#b14e2a" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2v5"/><path d="M12 17v5"/><path d="m4.9 4.9 3.5 3.5"/><path d="m15.6 15.6 3.5 3.5"/><path d="M2 12h5"/><path d="M17 12h5"/><path d="m4.9 19.1 3.5-3.5"/><path d="m15.6 8.4 3.5-3.5"/></svg>
            </div>
            <div class="recovery-main">
              <div class="banner-title amber-ink">Rate limit reached</div>
              <div class="banner-sub amber-sub">
                {g.LeadAgent} could not continue the {detailPendingRateLimit.Phase} run after automatic fallback. Pick a target and retry.
              </div>
              <div class="recovery-picker">
                <RunTargetPicker
                  agent={selectedDetailAgent}
                  {profiles}
                  variant="inline"
                  value={{ provider: recoveryProvider, profile: recoveryProfile, model: recoveryModel, effort: recoveryEffort }}
                  onChange={(next) => {
                    recoveryProvider = next.provider || "";
                    recoveryProfile = next.profile || "";
                    recoveryModel = next.model || "";
                    recoveryEffort = next.effort || "";
                  }}
                />
                <button class="btn-primary" disabled={recoveryBusy} onclick={() => resolveRateLimit(detailPendingRateLimit)}>
                  {recoveryBusy ? "Retrying…" : "Retry with this model"}
                </button>
              </div>
              {#if detailPendingRateLimit.Error}
                <div class="recovery-error mono">{detailPendingRateLimit.Error}</div>
              {/if}
            </div>
          </div>
        {/if}

        <!-- COMPLETION banner -->
        {#if g.Status === "review"}
          <div class="completion">
            <div class="completion-head">
              <span class="pulse-dot"></span>
              <div>
                <div class="completion-title">{g.LeadAgent} believes this goal is done</div>
                <div class="completion-sub">Review the closing report, then confirm. Only you can mark a goal done.</div>
              </div>
            </div>
            <div class="completion-body">
              <div class="section-label mono">Closing report</div>
              <div class="md">{@html renderMarkdown(g.ClosingReport)}</div>
              <div class="completion-actions">
                <button class="btn-primary grow" disabled={busy === g.ID} onclick={() => transition(g, "done", "Marked done by you.")}>Mark done</button>
                <button class="btn" disabled={busy === g.ID} onclick={() => transition(g, "active", "Reopened by you.")}>Reopen</button>
              </div>
            </div>
          </div>
        {/if}

        <!-- PENDING ACCESS REQUESTS -->
        {#if detailOpenReqs.length > 0}
          <div class="requests">
            <div class="requests-head">
              <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#b37a1e" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 11V8a4 4 0 0 1 8 0"/><rect x="5" y="11" width="14" height="9" rx="1.5"/><path d="M12 15v2"/></svg>
              <div class="requests-title">Access requests waiting on you</div>
              <span class="req-count mono">{detailOpenReqs.length}</span>
            </div>
            <div class="requests-body">
              {#each detailOpenReqs as r (r.ID)}
                {@const rk = RK[r.Kind]}
                {@const [sbg, sbd, stc, rawLbl] = reqStatusChip[r.Status] ?? reqStatusChip.pending}
                {@const slbl = r.Status === "approved" && isInstallable(r) ? "installing…" : rawLbl}
                <div class="req-card" class:failed={r.Status === "failed"}>
                  <span class="req-icon" style="background:{rk.t};border-color:{rk.b}">
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke={rk.c} stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{@html rk.ic}</svg>
                  </span>
                  <div class="req-main">
                    <div class="req-top">
                      <span class="req-kind">{rk.label}</span>
                      <span class="pill mono sm" style="background:{sbg};border-color:{sbd};color:{stc}">{slbl}</span>
                    </div>
                    <div class="req-reason">{r.Reason}</div>
                    <div class="req-payload">
                      {#each payloadEntries(r) as [k, v]}
                        <span class="kv mono"><span class="k">{k}</span><span class="v">{v}</span></span>
                      {/each}
                    </div>
                    {#if installCommand(r)}
                      <div class="req-cmd mono" title="The exact command Podiom runs on approval">$ {installCommand(r)}</div>
                    {/if}
                    {#if r.ExecutionError}
                      <div class="req-error">
                        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 9v4"/><path d="M12 17h.01"/><path d="M10.3 4.3 2.5 18a2 2 0 0 0 1.7 3h15.6a2 2 0 0 0 1.7-3L13.7 4.3a2 2 0 0 0-3.4 0z"/></svg>
                        {r.ExecutionError}
                      </div>
                    {/if}
                    {#if r.Status === "pending" || r.Status === "failed"}
                      <div class="req-actions">
                        <button class="btn-approve" onclick={() => (dialog = { req: r, action: "approve", note: "", secret: "", busy: false })}>Approve</button>
                        <button class="btn-deny" onclick={() => (dialog = { req: r, action: "deny", note: "", secret: "", busy: false })}>Deny</button>
                      </div>
                    {/if}
                  </div>
                </div>
              {/each}
            </div>
          </div>
        {/if}

        <!-- NEXT STEP -->
        {#if nextStepVisible}
          <div class="panel">
            <div class="ns-head">
              <span class="section-label mono ns-label">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h13"/><path d="M12 5l7 7-7 7"/></svg>
                Next step
              </span>
              {#if g.NextStepAt}
                <span class="ns-age mono">stated {relTime(g.NextStepAt)}</span>
              {/if}
            </div>
            {#if g.NextStep}
              <div class="ns-action">{g.NextStep}</div>
              {#if g.NextStepWhy}
                <div class="ns-why">{g.NextStepWhy}</div>
              {/if}
              <div class="ns-foot mono">
                <AgentAvatar name={g.LeadAgent} size={17} radius={5} fontSize={8} />
                {g.LeadAgent} decided this · restated at every review
              </div>
            {:else}
              <div class="desc muted">
                No next step stated yet — {g.LeadAgent} sets one at its next review.
              </div>
            {/if}
          </div>
        {/if}

        <!-- OUTCOME -->
        <div class="panel">
          {#if g.Description}
            <div class="desc">{g.Description}</div>
          {/if}
          {#if g.SuccessCriteria}
            <div class="criteria" class:mt={!!g.Description}>
              <div class="criteria-label mono">Success criteria — what “done” means</div>
              <div class="criteria-text">{g.SuccessCriteria}</div>
            </div>
          {/if}
          {#if !g.Description && !g.SuccessCriteria}
            <div class="desc muted">No description or success criteria yet.</div>
          {/if}
        </div>

        <!-- METRICS -->
        {#if g.Metrics.length > 0}
          <div class="panel">
            <div class="section-label mono">Metrics</div>
            <div class="metrics">
              {#each g.Metrics as m (m.name)}
                <div>
                  <div class="metric-row">
                    <span class="metric-name strong">{m.name}</span>
                    {#if metricMet(m)}
                      <span class="met mono">
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>
                        target met
                      </span>
                    {/if}
                    <span class="metric-val mono">{metricValue(m)}</span>
                  </div>
                  <div class="bar lg"><div class="fill" class:met={metricMet(m)} style="width:{metricPct(m)}%"></div></div>
                </div>
              {/each}
            </div>
          </div>
        {/if}

        <!-- TIMELINE -->
        <div class="panel">
          <div class="timeline-head">
            <span class="section-label mono" style="margin-bottom:0">Activity</span>
            <span class="group-rule"></span>
            <button class="btn feedback-toggle" onclick={() => (feedbackOpen = !feedbackOpen)}>
              {feedbackOpen ? "Cancel" : "Add feedback"}
            </button>
          </div>
          {#if feedbackOpen}
            <div class="feedback-composer">
              <div class="field-label-row">
                <div class="field-label mono">Feedback</div>
                <VoiceButton size="sm" onText={(t) => (feedbackBody = appendTranscript(feedbackBody, t))} />
              </div>
              <textarea
                class="field"
                rows="3"
                bind:value={feedbackBody}
                placeholder="Strategy notes, constraints, or next-step thoughts for the next goal run."></textarea>
              <div class="feedback-actions">
                <button class="btn-primary" disabled={feedbackBusy || !feedbackBody.trim()} onclick={() => submitFeedback(g)}>
                  {feedbackBusy ? "Saving…" : "Save feedback"}
                </button>
                <button class="btn" disabled={feedbackBusy} onclick={() => { feedbackOpen = false; feedbackBody = ""; }}>Cancel</button>
              </div>
            </div>
          {/if}
          <div class="timeline">
            <div class="timeline-rule"></div>
            <div class="events">
              {#each timelineItems as item (item.kind === "toolGroup" ? "g" + item.id : "e" + item.ev.ID)}
                {#if item.kind === "toolGroup"}
                  {@const k = EK.tool_use}
                  <div class="event">
                    <span class="event-dot" style="background:{k.t};border-color:{k.c}22">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke={k.c} stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{@html k.ic}</svg>
                    </span>
                    <div class="event-main">
                      <button class="tool-group-toggle" onclick={() => toggleGroup(item.id)}>
                        <span class="event-kind mono" style="color:{k.c}">{item.events.length} read-only tool calls</span>
                        <span class="arrow" class:open={expandedGroups.has(item.id)}>›</span>
                      </button>
                      {#if expandedGroups.has(item.id)}
                        <div class="tool-group-list">
                          {#each item.events as ev (ev.ID)}
                            {@const tp = toolPayload(ev)}
                            <div class="tool-row">
                              <span class="tool-name mono">{tp.tool}</span>
                              {#if tp.summary}<span class="tool-summary mono">{tp.summary}</span>{/if}
                              <span class="event-time mono">{relTime(ev.CreatedAt)}</span>
                            </div>
                          {/each}
                        </div>
                      {/if}
                      {#if canViewRun(item.events[0])}
                        <button class="event-session mono" onclick={() => openRun(item.events[0])}>
                          view run <span class="arrow">↗</span>
                        </button>
                      {/if}
                    </div>
                  </div>
                {:else if item.ev.Kind === "tool_use"}
                  {@const ev = item.ev}
                  {@const k = EK.tool_use}
                  {@const tp = toolPayload(ev)}
                  <div class="event">
                    <span class="event-dot" style="background:{k.t};border-color:{k.c}22">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke={k.c} stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{@html k.ic}</svg>
                    </span>
                    <div class="event-main">
                      <div class="event-top">
                        <span class="event-kind mono" style="color:{k.c}">{k.label}</span>
                        <span class="event-time mono">{relTime(ev.CreatedAt)}</span>
                      </div>
                      <div class="event-title">{tp.tool}</div>
                      {#if tp.summary}
                        <div class="event-cmd mono">{tp.summary}{#if tp.input_truncated}<span class="tool-trunc"> …truncated</span>{/if}</div>
                      {/if}
					  {#if canViewRun(ev)}
						<button class="event-session mono" onclick={() => openRun(ev)}>
                          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
						  view run <span class="arrow">↗</span>
                        </button>
                      {/if}
                    </div>
                  </div>
                {:else}
                  {@const ev = item.ev}
                  {@const k = EK[ev.Kind] ?? EK.progress}
                  <div class="event">
                    <span class="event-dot" style="background:{k.t};border-color:{k.c}22">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke={k.c} stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{@html k.ic}</svg>
                    </span>
                    <div class="event-main">
                      <div class="event-top">
                        <span class="event-kind mono" style="color:{k.c}">{k.label}</span>
                        <span class="event-time mono">{relTime(ev.CreatedAt)}</span>
                        {#if canEditFeedback(ev) && editingFeedbackID !== ev.ID}
                          <button class="event-edit mono" onclick={() => startEditFeedback(ev)}>edit</button>
                        {/if}
                      </div>
                      {#if editingFeedbackID === ev.ID}
                        <div class="feedback-edit">
                          <textarea class="field" rows="3" bind:value={editingFeedbackBody}></textarea>
                          <div class="feedback-actions">
                            <button class="btn-primary" disabled={editingFeedbackBusy || !editingFeedbackBody.trim()} onclick={() => submitFeedbackEdit(g, ev)}>
                              {editingFeedbackBusy ? "Saving…" : "Save changes"}
                            </button>
                            <button class="btn" disabled={editingFeedbackBusy} onclick={cancelEditFeedback}>Cancel</button>
                          </div>
                        </div>
                      {:else}
                        <div class="event-title">{eventTitle(ev)}</div>
                        {#if ev.Kind === "metric_update" && metricDelta(ev)}
                          <div class="event-delta mono">{metricDelta(ev)}</div>
                        {/if}
                        {#if eventBody(ev)}
                          <div class="event-body md">{@html renderMarkdown(eventBody(ev))}</div>
                        {/if}
                      {/if}
					  {#if canViewRun(ev)}
						<button class="event-session mono" onclick={() => openRun(ev)}>
                          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
						  view run <span class="arrow">↗</span>
                        </button>
                      {/if}
                    </div>
                  </div>
                {/if}
              {/each}
            </div>
          </div>
          {#if moreEvents}
            <button class="load-more mono" disabled={loadingMore} onclick={loadMoreEvents}>
              {loadingMore ? "loading…" : "load older activity"}
            </button>
          {/if}
        </div>
      </div>
    </div>
  {/if}

  <!-- ================= CREATE ================= -->
  {#if view === "create"}
    <div class="page form-page">
      <button class="back" onclick={() => (view = "list")}>
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 6l-6 6 6 6"/></svg>All goals
      </button>

      <div class="page-title">New goal</div>
      <div class="page-sub">Describe the outcome and hand it to one agent. It plans the rest — you’ll be able to leave as soon as you create it.</div>

      <div class="form">
        <div class="field-label mono">Title</div>
        <input class="field" bind:value={cTitle} placeholder="e.g. Grow the newsletter to 500 subscribers" />

        <div class="field-label-row">
          <div class="field-label mono">Description</div>
          <VoiceButton size="sm" onText={(t) => (cDesc = appendTranscript(cDesc, t))} />
        </div>
        <textarea class="field" rows="3" bind:value={cDesc} placeholder="What is the outcome, and why does it matter? Give the agent the context a teammate would need."></textarea>

        <div class="field-label-row">
          <div class="field-label mono">Success criteria</div>
          <VoiceButton size="sm" onText={(t) => (cCriteria = appendTranscript(cCriteria, t))} />
        </div>
        <textarea class="field" rows="2" bind:value={cCriteria} placeholder='What does "done" look like? The agent only proposes completion when this is met.'></textarea>

        <div class="field-label mono">Metrics <span class="opt">· optional</span></div>
        <div class="metric-rows">
          {#each cMetrics as m, i}
            <div class="metric-input-row">
              <input class="field grow" bind:value={m.name} placeholder="metric name" />
              <input class="field w90 mono" bind:value={m.target} placeholder="target" />
              <input class="field w70" bind:value={m.unit} placeholder="unit" />
              {#if cMetrics.length > 1}
                <button class="row-remove" onclick={() => (cMetrics = cMetrics.filter((_, j) => j !== i))} aria-label="Remove metric">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
                </button>
              {/if}
            </div>
          {/each}
        </div>
        <button class="add-metric" onclick={() => (cMetrics = [...cMetrics, { name: "", target: "", unit: "" }])}>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M12 5v14"/><path d="M5 12h14"/></svg>Add metric
        </button>

        <div class="field-label mono">Lead agent</div>
        <div class="chips">
          {#each agents as a (a.Name)}
            <button class="agent-pick" class:on={cAgent === a.Name} onclick={() => (cAgent = a.Name)}>
              <AgentAvatar name={a.Name} size={22} radius={7} fontSize={10} />{a.Name}
            </button>
          {/each}
          {#if agents.length === 0}
            <span class="muted-note">Hire an agent first — a goal needs a lead.</span>
          {/if}
        </div>

        <div class="field-label mono">Run target</div>
        <RunTargetPicker
          agent={selectedCreateAgent}
          {profiles}
          variant="stacked"
          value={{ provider: cProvider, profile: cProfile, model: cModel, effort: cEffort }}
          onChange={(next) => {
            cProvider = next.provider || "";
            cProfile = next.profile || "";
            cModel = next.model || "";
            cEffort = next.effort || "";
          }}
        />

        <div class="two-col">
          <div>
            <div class="field-label mono">Project <span class="opt">· optional</span></div>
            <div class="chips">
              <button class="chip mono" class:on={cProject === ""} onclick={() => (cProject = "")}>none</button>
              {#each projects as p (p.id)}
                <button class="chip mono" class:on={cProject === p.id} onclick={() => (cProject = p.id)}>{p.id}</button>
              {/each}
            </div>
          </div>
          <div>
            <div class="field-label mono">Review cadence</div>
            <div class="chips">
              {#each CADENCES as c (c.v)}
                <button class="chip mono" class:on={cCadence === c.v} onclick={() => (cCadence = c.v)}>{c.label}</button>
              {/each}
            </div>
          </div>
        </div>

        <div class="yolo-warn">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#b14322" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l7 3v6c0 4-3 6.7-7 8-4-1.3-7-4-7-8V6z"/><path d="M12 9v4"/><path d="M12 16h.01"/></svg>
          <div>
            <strong>Goals run autonomously with full access (yolo).</strong>
            {cAgent || "The lead agent"} — and every task or schedule this goal creates — can run shell commands, edit files, install software, and reach the network on this machine without asking first. Every action is recorded on the goal timeline.
          </div>
        </div>

        <button class="btn-primary submit" disabled={!canSubmit || cBusy} onclick={submitCreate}>
          {cBusy ? "Creating…" : "Create goal & start planning"}
        </button>
        <div class="submit-note">The agent begins planning in the background the moment you create it.</div>
      </div>
    </div>
  {/if}
</div>

{#if selectedRunEvent}
  <button class="run-backdrop" aria-label="Close run details" onclick={closeRun}></button>
  <div class="run-drawer" role="dialog" aria-modal="true" aria-label="Goal run details">
    <div class="run-drawer-head">
      <div>
        <div class="section-label mono">Focused activity</div>
        <div class="run-drawer-title">{runDetail ? runKindLabel(runDetail.run.Kind) : "Goal run"}</div>
      </div>
      <button class="run-close" onclick={closeRun} aria-label="Close">×</button>
    </div>

    <div class="run-selected">
      <div class="run-selected-top mono">
        <span>{EK[selectedRunEvent.Kind]?.label ?? selectedRunEvent.Kind}</span>
        <span>{relTime(selectedRunEvent.CreatedAt)}</span>
      </div>
      <strong>{eventTitle(selectedRunEvent)}</strong>
      {#if eventBody(selectedRunEvent)}
        <div class="md">{@html renderMarkdown(eventBody(selectedRunEvent))}</div>
      {/if}
    </div>

    {#if runLoading}
      <div class="run-state mono">Loading the exact run…</div>
    {:else if runError}
      <div class="run-state bad">{runError}</div>
    {:else if runDetail}
      <div class="run-meta mono">
        <span>{runDetail.run.AgentName}</span>
        <span>{runDetail.run.Status.replace("_", " ")}</span>
        {#if runDetail.run.SourceID}<span>{runDetail.run.SourceID}</span>{/if}
        {#if runDetail.session}<span>{runDetail.session.Provider}{runDetail.session.Model ? ` · ${runDetail.session.Model}` : ""}</span>{/if}
      </div>

      {#if runDetail.run.Legacy}
        <div class="run-legacy">This activity predates exact turn tracking. The transcript may include the full historical session.</div>
      {/if}

      <section class="run-section">
        <h3>Run activity</h3>
        <div class="run-event-list">
          {#each runDetail.events as ev (ev.ID)}
            <div class="run-event" class:selected={ev.ID === selectedRunEvent.ID}>
              <div class="run-event-top mono"><span>{EK[ev.Kind]?.label ?? ev.Kind}</span><span>{relTime(ev.CreatedAt)}</span></div>
              <div>{eventTitle(ev)}</div>
            </div>
          {/each}
        </div>
      </section>

      <section class="run-section">
        <h3>Conversation for this run</h3>
        {#if !runDetail.transcript_available}
          <div class="run-state">Transcript unavailable. The referenced session has been deleted.</div>
        {:else if runDetail.messages.length === 0}
          <div class="run-state">No transcript messages were stored for this run.</div>
        {:else}
          <div class="run-messages">
            {#each runDetail.messages as message (message.ID)}
              <article class="run-message" class:agent={message.Role === "assistant"} class:error={message.Kind === "error"}>
                <div class="run-message-label mono">{messageLabel(message)}</div>
                <div class="md">{@html renderMarkdown(message.Content)}</div>
              </article>
            {/each}
          </div>
        {/if}
      </section>

      {#if runDetail.session}
        <button class="btn run-full" onclick={() => onOpenChat({ sessionId: runDetail?.session?.ID })}>
          Open full conversation <span class="arrow">↗</span>
        </button>
      {/if}
    {/if}
  </div>
{/if}

<!-- ================= ACCESS DECISION DIALOG ================= -->
{#if dialog}
  {@const copy = dialogCopy(dialog.req, dialog.action)}
  {@const rk = RK[dialog.req.Kind]}
  <div class="overlay" role="presentation" onclick={() => (dialog = null)}>
    <div class="modal" role="dialog" aria-modal="true" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <div class="modal-head">
        <span class="req-icon lg" style="background:{copy.warn || dialog.action === 'deny' ? '#fbe7e0' : rk.t};border-color:{copy.warn || dialog.action === 'deny' ? '#efc0ad' : rk.b}">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke={copy.warn || dialog.action === "deny" ? "#b14322" : rk.c} stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{@html rk.ic}</svg>
        </span>
        <div class="modal-title">{copy.title}</div>
      </div>
      <div class="modal-body">
        <div class="modal-text">{copy.body}</div>
        {#if copy.warn}
          <div class="req-error warn">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 9v4"/><path d="M12 17h.01"/><path d="M10.3 4.3 2.5 18a2 2 0 0 0 1.7 3h15.6a2 2 0 0 0 1.7-3L13.7 4.3a2 2 0 0 0-3.4 0z"/></svg>
            Security-sensitive. Autonomous mode removes the per-action approval step for this agent everywhere.
          </div>
        {/if}
        <div class="req-payload">
          {#each payloadEntries(dialog.req) as [k, v]}
            <span class="kv mono"><span class="k">{k}</span><span class="v">{v}</span></span>
          {/each}
        </div>
        {#if installCommand(dialog.req)}
          <div class="req-cmd mono">$ {installCommand(dialog.req)}</div>
        {/if}
        {#if dialog.req.Kind === "env_var" && dialog.action === "approve"}
          <div class="field-label mono">Value for {payloadOf(dialog.req).var_name || "the variable"} <span class="opt">· optional · stored on this machine, never shown again</span></div>
          <input class="field" type="password" autocomplete="off" bind:value={dialog.secret} placeholder="Paste the token / secret value" />
        {/if}
        <div class="field-label mono">Note to agent <span class="opt">· optional · relayed at next review</span></div>
        <textarea class="field" rows="2" bind:value={dialog.note} placeholder="e.g. Approved — but keep DNS changes to the docs subdomain only."></textarea>
        <div class="modal-actions">
          <button
            class="btn-primary grow"
            class:danger={dialog.action === "deny" || copy.warn}
            disabled={dialog.busy}
            onclick={confirmDialog}>{dialog.busy ? "Working…" : copy.confirmLabel}</button>
          <button class="btn" disabled={dialog.busy} onclick={() => (dialog = null)}>Cancel</button>
        </div>
      </div>
    </div>
  </div>
{/if}

{#if pendingAbandon}
  <ConfirmModal
    title="Abandon this goal?"
    message={`“${pendingAbandon.Title}” stops reviewing and moves to abandoned. Its timeline is kept and you can reopen it later.`}
    confirmLabel="Abandon goal"
    busy={confirmBusy}
    onConfirm={confirmAbandon}
    onCancel={() => (pendingAbandon = null)} />
{/if}

{#if pendingDelete}
  <ConfirmModal
    title="Delete this goal?"
    message={`“${pendingDelete.Title}” and its entire timeline and access-request history are removed. Sessions the agent ran are kept. This cannot be undone.`}
    confirmLabel="Delete goal"
    busy={confirmBusy}
    onConfirm={confirmDelete}
    onCancel={() => (pendingDelete = null)} />
{/if}

<style>
  .run-backdrop {
    position: fixed;
    inset: 0;
    z-index: 80;
    border: 0;
    background: rgba(29, 24, 43, 0.34);
    cursor: default;
  }
  .run-drawer {
    position: fixed;
    z-index: 81;
    top: 0;
    right: 0;
    bottom: 0;
    width: min(580px, 92vw);
    overflow-y: auto;
    padding: 24px;
    background: #fcfbff;
    border-left: 1px solid #ddd4ef;
    box-shadow: -18px 0 48px rgba(37, 29, 61, 0.18);
  }
  .run-drawer-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 18px;
  }
  .run-drawer-title {
    margin-top: 4px;
    font-size: 24px;
    font-weight: 760;
    color: #2c2736;
  }
  .run-close {
    width: 34px;
    height: 34px;
    border: 1px solid #ddd7e7;
    border-radius: 10px;
    background: white;
    color: #766d83;
    font-size: 22px;
    cursor: pointer;
  }
  .run-selected {
    padding: 16px;
    border: 1px solid #bfb1e8;
    border-radius: 14px;
    background: #f3effd;
    color: #342d4a;
  }
  .run-selected-top,
  .run-event-top {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 6px;
    color: #6e609a;
    font-size: 10px;
    text-transform: uppercase;
  }
  .run-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
    margin: 14px 0;
  }
  .run-meta span {
    padding: 5px 8px;
    border: 1px solid #ded8e8;
    border-radius: 999px;
    background: white;
    color: #70667c;
    font-size: 10px;
  }
  .run-legacy,
  .run-state {
    margin: 14px 0;
    padding: 12px;
    border-radius: 10px;
    background: #f3f0ea;
    color: #796c5f;
    font-size: 12px;
  }
  .run-state.bad {
    background: #faebe6;
    color: #a54a2c;
  }
  .run-section {
    margin-top: 24px;
  }
  .run-section h3 {
    margin: 0 0 10px;
    color: #3f374c;
    font-size: 14px;
  }
  .run-event-list,
  .run-messages {
    display: grid;
    gap: 8px;
  }
  .run-event {
    padding: 11px 12px;
    border: 1px solid #e3dee9;
    border-radius: 10px;
    background: white;
    font-size: 12px;
  }
  .run-event.selected {
    border-color: #8b78cf;
    box-shadow: 0 0 0 2px rgba(105, 84, 185, 0.09);
  }
  .run-message {
    padding: 13px;
    border: 1px solid #e4dfe9;
    border-radius: 12px;
    background: white;
    font-size: 13px;
  }
  .run-message.agent {
    background: #f4f8f6;
    border-color: #d9e8e1;
  }
  .run-message.error {
    background: #fff2ed;
    border-color: #efcbbd;
  }
  .run-message-label {
    margin-bottom: 8px;
    color: #796f84;
    font-size: 10px;
    text-transform: uppercase;
  }
  .run-full {
    width: 100%;
    margin-top: 22px;
  }
  .goals-scroll {
    height: 100%;
    overflow-y: auto;
  }
  .page {
    max-width: 960px;
    margin: 0 auto;
    padding: 26px 30px 70px;
  }
  .page.narrow {
    max-width: 900px;
  }
  .page.form-page {
    max-width: 660px;
  }
  @media (max-width: 700px) {
    .run-drawer {
      top: 12vh;
      width: 100%;
      padding: 18px;
      border-top: 1px solid #ddd4ef;
      border-left: 0;
      border-radius: 18px 18px 0 0;
    }
  }
  .mono {
    font-family: "JetBrains Mono", monospace;
  }
  .page-error {
    max-width: 960px;
    margin: 14px auto 0;
    padding: 10px 16px;
    border: 1px solid #efc0ad;
    background: #fbefe9;
    border-radius: 12px;
    color: #a23e22;
    font-size: 13px;
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .page-error .dismiss {
    margin-left: auto;
    border: none;
    background: transparent;
    color: inherit;
  }

  /* ---- list ---- */
  .page-head {
    display: flex;
    align-items: flex-end;
    gap: 16px;
    flex-wrap: wrap;
    margin-bottom: 22px;
  }
  .page-title-wrap {
    flex: 1;
    min-width: 240px;
  }
  .page-title {
    font-size: 25px;
    font-weight: 800;
    letter-spacing: -0.02em;
  }
  .page-sub {
    font-size: 13.5px;
    line-height: 1.55;
    color: var(--muted-2);
    margin-top: 4px;
    max-width: 600px;
  }
  .btn-primary {
    padding: 10px 17px;
    border: none;
    border-radius: 11px;
    background: var(--teal);
    color: #fff;
    font-weight: 600;
    font-size: 13.5px;
    box-shadow: 0 6px 14px -6px rgba(63, 143, 126, 0.7);
  }
  .btn-primary.big {
    padding: 12px 22px;
    font-weight: 700;
    font-size: 14px;
    border-radius: 12px;
  }
  .btn-primary.grow {
    flex: 1;
  }
  .btn-primary.danger {
    background: #b14e2a;
    box-shadow: 0 6px 14px -6px rgba(177, 78, 42, 0.6);
  }
  .btn-primary.submit {
    width: 100%;
    margin-top: 24px;
    padding: 14px;
    font-size: 15px;
    font-weight: 700;
    border-radius: 13px;
  }

  .empty {
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 22px;
    padding: 56px 40px;
    text-align: center;
    box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04), 0 20px 46px -30px rgba(43, 37, 32, 0.22);
  }
  .empty-icon {
    width: 64px;
    height: 64px;
    border-radius: 18px;
    margin: 0 auto 20px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(150deg, #eaf2ee, #dcede6);
    border: 1px solid #cfe3d8;
  }
  .empty-title {
    font-size: 21px;
    font-weight: 800;
  }
  .empty-sub {
    font-size: 14px;
    line-height: 1.65;
    color: var(--muted-2);
    max-width: 460px;
    margin: 9px auto 0;
  }
  .empty-steps {
    display: flex;
    gap: 22px;
    justify-content: center;
    flex-wrap: wrap;
    margin: 26px 0 28px;
  }
  .empty-step {
    display: flex;
    align-items: center;
    gap: 9px;
    font-size: 13px;
    font-weight: 500;
    color: var(--muted);
  }
  .step-n {
    width: 26px;
    height: 26px;
    border-radius: 8px;
    background: #e3f1ec;
    color: var(--teal-deep);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    font-weight: 700;
  }

  .groups {
    display: flex;
    flex-direction: column;
    gap: 26px;
  }
  .group-head {
    display: flex;
    align-items: center;
    gap: 9px;
    margin-bottom: 12px;
  }
  .group-label {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--muted-2);
  }
  .group-label.attn {
    color: #b14e2a;
  }
  .group-label.dim {
    color: var(--faint);
  }
  .group-rule {
    height: 1px;
    flex: 1;
    background: var(--line-2);
  }
  .cards {
    display: flex;
    flex-direction: column;
    gap: 13px;
  }
  .card {
    text-align: left;
    width: 100%;
    display: block;
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 18px;
    padding: 20px 22px;
    box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04), 0 14px 34px -26px rgba(43, 37, 32, 0.18);
  }
  .card.review-ring {
    border-color: #efc0ad;
    box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04), 0 18px 40px -24px rgba(177, 67, 34, 0.32);
  }
  .card.attn-ring {
    border-color: #e7c9a8;
  }
  .card.dim {
    opacity: 0.62;
    filter: saturate(0.7);
  }
  .card-top {
    display: flex;
    align-items: flex-start;
    gap: 12px;
  }
  .card-meta {
    display: flex;
    align-items: center;
    gap: 9px;
    flex-wrap: wrap;
    margin-bottom: 7px;
    flex: 1;
    min-width: 0;
  }
  .chev {
    flex: none;
    margin-top: 3px;
  }
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 11px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    white-space: nowrap;
    border: 1px solid transparent;
  }
  .pill.lg {
    padding: 5px 13px;
    font-size: 12px;
  }
  .pill.sm {
    padding: 4px 10px;
    font-size: 10.5px;
  }
  .pill-dot {
    width: 6px;
    height: 6px;
    border-radius: 99px;
    animation: goalPulse 1.4s infinite;
  }
  .agent-chip {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    font-size: 12px;
    font-weight: 500;
    color: var(--muted);
  }
  .agent-chip.lg {
    font-size: 12.5px;
  }
  .proj {
    font-size: 11px;
    color: var(--faint);
  }
  .yolo-pill {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 3px 9px;
    border-radius: 999px;
    background: #fbe7e0;
    border: 1px solid #efc0ad;
    color: #b14322;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    cursor: help;
  }
  .card-title {
    font-size: 17px;
    font-weight: 700;
    line-height: 1.3;
    color: #241f1a;
    letter-spacing: -0.01em;
  }
  .card-attn {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 9px 14px;
    border-radius: 11px;
    margin-top: 14px;
    font-size: 12.5px;
    font-weight: 600;
    background: #fbf3e9;
    color: #9a6e1e;
  }
  .card-attn.hot {
    background: #fbefe9;
    color: #b14322;
  }
  .attn-dot {
    width: 7px;
    height: 7px;
    border-radius: 99px;
    flex: none;
    background: #c99a3c;
  }
  .attn-dot.hot {
    background: #d9663d;
  }
  .card-usage {
    margin-top: 11px;
  }
  .card-metric {
    margin-top: 14px;
  }
  .metric-row {
    display: flex;
    align-items: baseline;
    gap: 10px;
    margin-bottom: 6px;
  }
  .metric-name {
    font-size: 12.5px;
    font-weight: 500;
    color: var(--muted);
    flex: 1;
  }
  .metric-name.strong {
    font-size: 13.5px;
    font-weight: 600;
    color: var(--ink-soft);
  }
  .metric-val {
    font-size: 12px;
    font-weight: 600;
    color: #8a7560;
  }
  .met {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 11px;
    font-weight: 600;
    color: #3f7a5f;
  }
  .bar {
    height: 7px;
    border-radius: 99px;
    background: #f0e7dc;
    overflow: hidden;
  }
  .bar.lg {
    height: 9px;
  }
  .fill {
    height: 100%;
    border-radius: 99px;
    background: linear-gradient(90deg, #5bae97, var(--teal));
  }
  .fill.met {
    background: linear-gradient(90deg, #4f9e78, var(--teal-deep));
  }
  .metric-extra {
    font-size: 11px;
    color: #b4a897;
    margin-top: 7px;
  }
  .card-foot {
    display: flex;
    align-items: center;
    gap: 14px;
    margin-top: 15px;
    flex-wrap: wrap;
  }
  .foot-next {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    font-weight: 500;
    color: #a8825e;
  }
  .pend-chip {
    padding: 3px 9px;
    border-radius: 999px;
    font-size: 10.5px;
    font-weight: 600;
    background: #fbf1dd;
    color: #9a6e1e;
    border: 1px solid #ecd9ae;
  }
  .pend-chip.failed {
    background: #fbeae0;
    color: #b14e2a;
    border-color: #f2d6c5;
  }
  .foot-updated {
    margin-left: auto;
    font-size: 11.5px;
    color: #b4a897;
  }

  /* ---- detail ---- */
  .back {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    border: none;
    background: transparent;
    color: var(--muted-2);
    font-size: 12.5px;
    font-weight: 600;
    padding: 4px 0;
    margin-bottom: 14px;
  }
  .detail-head {
    display: flex;
    align-items: flex-start;
    gap: 14px;
    flex-wrap: wrap;
  }
  .detail-head-main {
    flex: 1;
    min-width: 280px;
  }
  .detail-title {
    font-size: 26px;
    font-weight: 800;
    line-height: 1.2;
    letter-spacing: -0.02em;
  }
  .detail-meta {
    display: flex;
    align-items: center;
    gap: 16px;
    margin-top: 11px;
    font-size: 12.5px;
    font-weight: 500;
    color: var(--muted-2);
    flex-wrap: wrap;
  }
  .detail-usage {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 13px;
    max-width: 380px;
  }
  .detail-usage-label {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--faint);
    flex: none;
  }
  .detail-usage-bars {
    flex: 1;
  }
  .meta-item {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .meta-item.next {
    color: #a8825e;
  }
  .detail-actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    position: relative;
  }
  .btn {
    padding: 9px 14px;
    border: 1px solid var(--field-line);
    border-radius: 10px;
    background: #fff;
    color: var(--muted);
    font-size: 12.5px;
    font-weight: 600;
  }
  .btn.teal {
    color: var(--teal-deep);
  }
  .btn.reviewing {
    display: inline-flex;
    align-items: center;
    gap: 7px;
  }
  .btn-spinner {
    width: 12px;
    height: 12px;
    border-radius: 999px;
    border: 2px solid #d9ebe5;
    border-top-color: var(--teal-deep);
    animation: btn-spin 900ms linear infinite;
    flex: none;
  }
  @keyframes btn-spin {
    to {
      transform: rotate(360deg);
    }
  }
  .btn.icon {
    width: 38px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0;
    color: var(--muted-2);
  }
  .menu-wrap {
    position: relative;
  }
  .menu {
    position: absolute;
    right: 0;
    top: 42px;
    z-index: 20;
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 12px;
    box-shadow: 0 18px 40px -18px rgba(43, 37, 32, 0.35);
    overflow: hidden;
    min-width: 160px;
  }
  .menu-item {
    display: block;
    width: 100%;
    text-align: left;
    padding: 10px 14px;
    border: none;
    background: transparent;
    font-size: 13px;
    font-weight: 600;
    color: var(--muted);
  }
  .menu-item:hover {
    background: var(--surface-3);
  }
  .menu-item.danger {
    color: #a23e22;
  }
  .stack {
    display: flex;
    flex-direction: column;
    gap: 16px;
    margin-top: 20px;
  }
  .panel {
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 18px;
    padding: 22px;
    box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04);
  }
  .section-label {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--faint);
    margin-bottom: 14px;
  }
  .desc {
    font-size: 14.5px;
    line-height: 1.7;
    color: var(--ink-soft);
  }
  .desc.muted {
    color: var(--faint);
  }
  .criteria {
    padding: 15px 17px;
    background: #f6f9f7;
    border: 1px solid #dcede6;
    border-radius: 13px;
  }
  .criteria.mt {
    margin-top: 16px;
  }
  .criteria-label {
    font-size: 10.5px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--teal);
    margin-bottom: 6px;
  }
  .criteria-text {
    font-size: 13.5px;
    line-height: 1.6;
    font-weight: 500;
    color: #2a4740;
  }
  .metrics {
    display: flex;
    flex-direction: column;
    gap: 17px;
  }

  /* Next step: the agent's stated intent. Action-first typography and a teal
     accent, deliberately unlike the header's "next review" clock — one is what
     the agent will do, the other is only when it wakes up. */
  .ns-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
  }
  .ns-label {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--teal);
    margin-bottom: 12px;
  }
  .ns-age {
    font-size: 11px;
    color: var(--faint);
    white-space: nowrap;
  }
  .ns-action {
    font-size: 16.5px;
    line-height: 1.45;
    font-weight: 600;
    color: var(--ink);
    letter-spacing: -0.01em;
  }
  .ns-why {
    margin-top: 8px;
    font-size: 13.5px;
    line-height: 1.65;
    color: var(--ink-soft);
  }
  .ns-foot {
    display: flex;
    align-items: center;
    gap: 7px;
    margin-top: 15px;
    padding-top: 13px;
    border-top: 1px solid var(--line-2);
    font-size: 11px;
    color: var(--faint);
  }

  .banner {
    display: flex;
    align-items: flex-start;
    gap: 14px;
    border-radius: 16px;
    padding: 18px 20px;
  }
  .banner.planning {
    background: #f3f0fc;
    border: 1px solid #ddd4f5;
  }
  .banner.rate-limit {
    background: #fff4ed;
    border: 1px solid #efc7b4;
  }
  .banner.goal-question {
    background: #fdf6e7;
    border: 1px solid #ecd9ae;
  }
  .goal-q-body {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-top: 12px;
    align-items: flex-start;
  }
  .goal-q-block {
    display: flex;
    flex-direction: column;
    gap: 7px;
    width: 100%;
  }
  .goal-q-header {
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: #9a6e1e;
  }
  .goal-q-text {
    font-size: 13.5px;
    font-weight: 600;
    color: #4a3f30;
  }
  .goal-q-options {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .goal-q-option {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    text-align: left;
    padding: 9px 12px;
    border-radius: 11px;
    border: 1px solid #e6dcc4;
    background: #fff;
    cursor: pointer;
    transition: border-color 0.12s, background 0.12s;
  }
  .goal-q-option:hover {
    border-color: #d9c69a;
  }
  .goal-q-option.sel {
    border-color: #c69a3f;
    background: #fbf1dd;
  }
  .goal-q-dot {
    width: 16px;
    flex: none;
    color: #9a6e1e;
    font-weight: 700;
  }
  .goal-q-option-text {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .goal-q-option-text small {
    color: #8a7f6a;
    font-size: 11.5px;
  }
  .goal-q-free {
    width: 100%;
    max-width: 420px;
    padding: 9px 12px;
    border-radius: 11px;
    border: 1px solid #e6dcc4;
    background: #fff;
    font: inherit;
  }
  .banner-icon {
    width: 38px;
    height: 38px;
    flex: none;
    border-radius: 11px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .banner-icon.violet {
    background: #eeeafb;
    border: 1px solid #d8cff3;
  }
  .banner-icon.amber {
    background: #fbeae0;
    border: 1px solid #efc0ad;
  }
  .banner-title {
    font-size: 15px;
    font-weight: 700;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .violet-ink {
    color: #4a3c8f;
  }
  .amber-ink {
    color: #a94724;
  }
  .banner-sub {
    font-size: 13px;
    line-height: 1.55;
    margin-top: 3px;
  }
  .violet-sub {
    color: #6e63a0;
  }
  .amber-sub {
    color: #985735;
  }
  .recovery-main {
    min-width: 0;
    flex: 1;
  }
  .recovery-picker {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    margin-top: 13px;
  }
  .recovery-error {
    margin-top: 10px;
    padding: 9px 10px;
    border-radius: 10px;
    background: #fffaf6;
    border: 1px solid #efc7b4;
    color: #9a4d2b;
    font-size: 11px;
    line-height: 1.45;
    overflow-wrap: anywhere;
  }
  .dots {
    display: inline-flex;
    gap: 3px;
    align-items: flex-end;
    padding-bottom: 3px;
  }
  .dot {
    display: inline-block;
    width: 5px;
    height: 5px;
    border-radius: 99px;
    background: #7c6fe0;
    animation: goalDot 1.2s infinite;
  }

  .completion {
    border: 1px solid #efc0ad;
    border-radius: 18px;
    overflow: hidden;
    box-shadow: 0 18px 40px -26px rgba(177, 67, 34, 0.4);
  }
  .completion-head {
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 15px 20px;
    background: linear-gradient(180deg, #fbefe9, #fbe7e0);
    border-bottom: 1px solid #f2d2c4;
  }
  .pulse-dot {
    width: 9px;
    height: 9px;
    border-radius: 99px;
    background: #d9663d;
    box-shadow: 0 0 0 4px rgba(217, 102, 61, 0.18);
    animation: goalPulse 1.4s infinite;
    flex: none;
  }
  .completion-title {
    font-size: 16px;
    font-weight: 800;
    color: #b14322;
  }
  .completion-sub {
    font-size: 12.5px;
    color: #b36a4e;
    margin-top: 1px;
  }
  .completion-body {
    padding: 20px;
    background: var(--surface);
  }
  .md {
    font-size: 14px;
    line-height: 1.65;
    color: var(--ink-soft);
  }
  .md :global(p) {
    margin: 0 0 10px;
  }
  .md :global(ul) {
    margin: 0 0 10px;
    padding-left: 20px;
  }
  .completion-actions {
    display: flex;
    gap: 10px;
    margin-top: 18px;
  }

  .requests {
    border: 1px solid #e7c9a8;
    border-radius: 18px;
    background: var(--surface);
    overflow: hidden;
  }
  .requests-head {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 15px 20px;
    background: #fbf3e9;
    border-bottom: 1px solid #efdcc0;
  }
  .requests-title {
    font-size: 14.5px;
    font-weight: 700;
    color: #9a6e1e;
    flex: 1;
  }
  .req-count {
    padding: 3px 10px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    background: #f6e7c7;
    color: #9a6e1e;
  }
  .requests-body {
    padding: 16px 20px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .req-card {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    border: 1px solid var(--line-3);
    border-radius: 14px;
    padding: 16px 17px;
    background: var(--surface);
  }
  .req-card.failed {
    border-color: #efc9b6;
    background: #fdf4f0;
  }
  .req-icon {
    width: 34px;
    height: 34px;
    border-radius: 10px;
    flex: none;
    display: flex;
    align-items: center;
    justify-content: center;
    border: 1px solid transparent;
  }
  .req-icon.lg {
    width: 40px;
    height: 40px;
    border-radius: 11px;
  }
  .req-main {
    flex: 1;
    min-width: 0;
  }
  .req-top {
    display: flex;
    align-items: center;
    gap: 9px;
    flex-wrap: wrap;
  }
  .req-kind {
    font-size: 13.5px;
    font-weight: 700;
  }
  .req-reason {
    font-size: 13px;
    line-height: 1.55;
    color: #5a5048;
    margin-top: 5px;
  }
  .req-payload {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 10px;
  }
  .kv {
    display: inline-flex;
    align-items: baseline;
    gap: 6px;
    padding: 4px 10px;
    border-radius: 8px;
    background: var(--surface-3);
    border: 1px solid var(--field-line);
    font-size: 11.5px;
    font-weight: 500;
  }
  .kv .k {
    color: var(--faint);
  }
  .kv .v {
    color: #5a5048;
    font-weight: 600;
  }
  .req-cmd {
    margin-top: 10px;
    padding: 9px 12px;
    border-radius: 10px;
    background: #2b2520;
    color: #e8e0d5;
    font-size: 11.5px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }
  .req-error {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-top: 11px;
    padding: 10px 12px;
    border: 1px solid #e7c3b5;
    background: #fbeeea;
    border-radius: 11px;
    font-size: 12.5px;
    line-height: 1.5;
    font-weight: 500;
    color: #a23e22;
  }
  .req-error.warn {
    margin-top: 14px;
  }
  .req-error svg {
    flex: none;
    margin-top: 1px;
  }
  .req-actions {
    display: flex;
    gap: 8px;
    margin-top: 13px;
  }
  .btn-approve {
    padding: 8px 16px;
    border: none;
    border-radius: 10px;
    background: var(--teal);
    color: #fff;
    font-size: 12.5px;
    font-weight: 600;
  }
  .btn-deny {
    padding: 8px 16px;
    border: 1px solid #e7c3b5;
    border-radius: 10px;
    background: #fff;
    color: #a23e22;
    font-size: 12.5px;
    font-weight: 600;
  }

  .timeline-head {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 18px;
  }
  .feedback-toggle {
    flex: none;
    padding: 7px 12px;
    font-size: 12px;
  }
  .feedback-composer {
    margin: -4px 0 20px;
    padding: 14px;
    border: 1px solid var(--field-line);
    border-radius: 12px;
    background: var(--surface-2);
  }
  .feedback-composer textarea {
    width: 100%;
    resize: vertical;
  }
  .feedback-actions {
    display: flex;
    gap: 8px;
    margin-top: 10px;
  }
  .timeline {
    position: relative;
  }
  .timeline-rule {
    position: absolute;
    left: 14px;
    top: 6px;
    bottom: 6px;
    width: 1.5px;
    background: #f0e7dc;
  }
  .events {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }
  .event {
    display: flex;
    gap: 14px;
    padding: 2px 0;
  }
  .event-dot {
    width: 30px;
    height: 30px;
    border-radius: 9px;
    flex: none;
    display: flex;
    align-items: center;
    justify-content: center;
    border: 1px solid transparent;
    position: relative;
    z-index: 1;
  }
  .event-main {
    flex: 1;
    min-width: 0;
    padding-top: 1px;
  }
  .event-top {
    display: flex;
    align-items: baseline;
    gap: 9px;
    flex-wrap: wrap;
  }
  .event-kind {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  .event-time {
    font-size: 11px;
    color: #b4a897;
    margin-left: auto;
  }
  .event-edit {
    border: 1px solid #eadfce;
    border-radius: 8px;
    background: #fff;
    color: #7d6f5f;
    padding: 3px 8px;
    font-size: 10.5px;
    cursor: pointer;
  }
  .event-edit:hover {
    border-color: #d6c7b3;
    color: var(--ink);
  }
  .event-title {
    font-size: 14px;
    font-weight: 700;
    color: var(--ink);
    margin-top: 3px;
    overflow-wrap: anywhere;
  }
  .event-delta {
    display: inline-block;
    margin-top: 6px;
    padding: 4px 11px;
    border-radius: 8px;
    background: #e2f0ec;
    font-size: 12.5px;
    font-weight: 600;
    color: var(--teal-deep);
  }
  .event-body {
    font-size: 13px;
    line-height: 1.55;
    color: var(--muted);
    margin-top: 5px;
    overflow-wrap: anywhere;
  }
  /* markdown paragraphs shouldn't add trailing margin inside a compact event */
  .event-body :global(p:last-child),
  .event-body :global(ul:last-child) {
    margin-bottom: 0;
  }
  .feedback-edit {
    margin-top: 8px;
  }
  .feedback-edit textarea {
    width: 100%;
    resize: vertical;
  }
  /* tool_use command / file-path, shown in a compact terminal-style block */
  .event-cmd {
    margin-top: 6px;
    padding: 7px 11px;
    border-radius: 9px;
    background: #2b2520;
    color: #e8e0d5;
    font-size: 11.5px;
    line-height: 1.5;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }
  .tool-trunc {
    color: #b4a897;
  }
  .tool-group-toggle {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    border: none;
    background: transparent;
    padding: 0;
    cursor: pointer;
  }
  .tool-group-toggle .arrow {
    color: #b4a897;
    transition: transform 0.12s ease;
  }
  .tool-group-toggle .arrow.open {
    transform: rotate(90deg);
  }
  .tool-group-list {
    margin-top: 7px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .tool-row {
    display: flex;
    align-items: baseline;
    gap: 9px;
    font-size: 11.5px;
  }
  .tool-name {
    font-weight: 700;
    color: var(--ink);
    flex: none;
  }
  .tool-summary {
    color: var(--muted);
    overflow-wrap: anywhere;
    min-width: 0;
  }
  .tool-row .event-time {
    margin-left: auto;
  }
  .event-session {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin-top: 8px;
    border: none;
    background: transparent;
    padding: 0;
    font-size: 11.5px;
    font-weight: 600;
    color: var(--teal-deep);
  }
  .event-session .arrow {
    color: #b4a897;
  }
  .load-more {
    display: block;
    margin: 18px auto 0;
    padding: 8px 16px;
    border: 1px dashed #cfbea9;
    border-radius: 10px;
    background: transparent;
    color: #a8825e;
    font-size: 12px;
    font-weight: 600;
  }

  /* ---- create ---- */
  .form {
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 20px;
    padding: 24px;
    margin-top: 20px;
    box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04), 0 20px 46px -32px rgba(43, 37, 32, 0.2);
  }
  .field-label {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--faint);
    margin: 18px 0 8px;
  }
  .form .field-label:first-child {
    margin-top: 0;
  }
  .field-label-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    margin: 18px 0 8px;
  }
  .field-label-row .field-label {
    margin: 0;
  }
  .opt {
    color: #c4b8a9;
    text-transform: none;
    letter-spacing: 0;
  }
  .field {
    width: 100%;
    border: 1px solid var(--field-line);
    border-radius: 12px;
    padding: 12px 14px;
    font-size: 14px;
    color: var(--ink);
    outline: none;
    background: var(--surface-3);
    resize: vertical;
  }
  .metric-rows {
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .metric-input-row {
    display: flex;
    gap: 8px;
  }
  .field.grow {
    flex: 1;
  }
  .field.w90 {
    width: 90px;
  }
  .field.w70 {
    width: 70px;
  }
  .row-remove {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 38px;
    flex: none;
    border: 1px solid #e7c3b5;
    border-radius: 10px;
    background: #fff;
    color: #a23e22;
  }
  .add-metric {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin-top: 10px;
    padding: 8px 13px;
    border: 1px dashed #cfbea9;
    border-radius: 10px;
    background: transparent;
    color: #a8825e;
    font-size: 12px;
    font-weight: 600;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .chip {
    padding: 6px 13px;
    border-radius: 9px;
    font-size: 12px;
    font-weight: 600;
    border: 1px solid var(--field-line);
    background: #fff;
    color: var(--muted);
  }
  .chip.on {
    border-color: #bfe0d6;
    background: #e3f1ec;
    color: var(--teal-deep);
  }
  .agent-pick {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    padding: 5px 14px 5px 5px;
    border-radius: 11px;
    font-size: 12.5px;
    font-weight: 600;
    border: 1px solid var(--field-line);
    background: #fff;
    color: var(--muted);
  }
  .agent-pick.on {
    border-color: #bfe0d6;
    background: #e3f1ec;
    color: var(--teal-deep);
  }
  .muted-note {
    font-size: 13px;
    color: var(--faint);
  }
  .two-col {
    display: flex;
    gap: 24px;
    flex-wrap: wrap;
    margin-top: 4px;
  }
  .two-col > div {
    flex: 1;
    min-width: 220px;
  }
  .submit-note {
    text-align: center;
    font-size: 12px;
    color: var(--faint);
    margin-top: 10px;
  }
  .yolo-warn {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    margin-top: 18px;
    padding: 12px 14px;
    border-radius: 12px;
    background: #fbeee9;
    border: 1px solid #efc7b8;
    font-size: 12.5px;
    line-height: 1.5;
    color: #7a3a24;
  }
  .yolo-warn svg {
    flex: none;
    margin-top: 1px;
  }
  .yolo-warn strong {
    color: #b14322;
  }

  /* ---- modal ---- */
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(43, 37, 32, 0.28);
    backdrop-filter: blur(2px);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 50;
    padding: 16px;
  }
  .modal {
    width: 460px;
    max-width: 100%;
    background: var(--surface);
    border-radius: 20px;
    box-shadow: 0 30px 80px -24px rgba(43, 37, 32, 0.5);
    overflow: hidden;
  }
  .modal-head {
    display: flex;
    align-items: center;
    gap: 13px;
    padding: 24px 26px 0;
  }
  .modal-title {
    font-size: 20px;
    font-weight: 800;
    letter-spacing: -0.01em;
  }
  .modal-body {
    padding: 16px 26px 26px;
  }
  .modal-text {
    font-size: 13.5px;
    line-height: 1.6;
    color: #5a5048;
  }
  .modal-body .req-payload {
    margin-top: 14px;
  }
  .modal-body .field-label {
    margin: 18px 0 7px;
  }
  .modal-actions {
    display: flex;
    gap: 10px;
    margin-top: 18px;
  }

  @keyframes goalPulse {
    0%,
    100% {
      opacity: 1;
    }
    50% {
      opacity: 0.35;
    }
  }
  @keyframes goalDot {
    0%,
    80%,
    100% {
      opacity: 0.25;
      transform: translateY(0);
    }
    40% {
      opacity: 1;
      transform: translateY(-3px);
    }
  }
</style>
