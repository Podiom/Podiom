<script lang="ts">
  import { onMount } from "svelte";
  import {
    approveAccessRequest,
    createGoal,
    deleteGoal,
    denyAccessRequest,
    getGoal,
    listAccessRequests,
    listGoalEvents,
    listGoals,
    listProjects,
    patchGoal,
    runGoalReview,
  } from "../lib/api";
  import { live } from "../lib/live.svelte";
  import { renderMarkdown } from "../lib/markdown";
  import { agentGradient, initial } from "../lib/theme";
  import ConfirmModal from "../lib/ConfirmModal.svelte";
  import type {
    AccessRequest,
    AccessRequestKind,
    Agent,
    Goal,
    GoalDetail,
    GoalEvent,
    GoalEventKind,
    GoalMetric,
    GoalStatus,
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

  // Create form.
  let cTitle = $state("");
  let cDesc = $state("");
  let cCriteria = $state("");
  let cMetrics = $state<{ name: string; target: string; unit: string }[]>([{ name: "", target: "", unit: "" }]);
  let cAgent = $state("");
  let cProject = $state("");
  let cCadence = $state("24h");
  let cBusy = $state(false);
  let projects = $state<Project[]>([]);

  const CADENCES = [
    { label: "every 6h", v: "6h" },
    { label: "every 12h", v: "12h" },
    { label: "every 24h", v: "24h" },
    { label: "every 3d", v: "72h" },
    { label: "weekly", v: "168h" },
  ];

  // Access-request decision dialog.
  let dialog = $state<{ req: AccessRequest; action: "approve" | "deny"; note: string; busy: boolean } | null>(null);

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
    access_requested: { c: "#d9663d", t: "#fbeae0", label: "Access requested", ic: '<path d="M8 11V8a4 4 0 0 1 8 0"/><path d="M5 11h14v9H5z"/><path d="M12 14v3"/>' },
    access_decided: { c: "#4f9e78", t: "#eaf1ed", label: "Access decided", ic: '<path d="M20 6 9 17l-5-5"/>' },
    status_change: { c: "#8a7f73", t: "#f1ece6", label: "Status changed", ic: '<path d="M7 4 4 7l3 3"/><path d="M4 7h11"/><path d="M17 20l3-3-3-3"/><path d="M20 17H9"/>' },
    completion_proposed: { c: "#b14322", t: "#fbe7e0", label: "Completion proposed", ic: '<path d="M12 3l2.6 5.8 6.4.6-4.8 4.2 1.4 6.2L12 17l-5.6 3 1.4-6.2L3 9.4l6.4-.6z"/>' },
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
      detail = await getGoal(id);
      moreEvents = detail.events.length >= PAGE;
      view = "detail";
      menuOpen = false;
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
    } catch {
      // Deleted elsewhere: fall back to the list.
      detail = null;
      view = "list";
    }
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
      void refreshAll();
      if (detail && msg.goal_event.GoalID === detail.goal.ID) void refreshDetail();
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

  const needsAttention = (g: Goal) => g.Status === "review" || (openReqsByGoal.get(g.ID)?.length ?? 0) > 0;
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

  // isPlanning: the goal was just created and the decomposition session is the
  // newest thing on the timeline — show the "agent is planning now" banner.
  const isPlanning = $derived(
    detail !== null &&
      detail.goal.Status === "active" &&
      detail.events.length > 0 &&
      (detail.events[0].Kind === "planning_started" || detail.events[0].Kind === "created"),
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
    busy = goal.ID;
    try {
      await runGoalReview(goal.ID);
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : "Couldn't start the review.";
    } finally {
      busy = "";
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
        return { title: "Acknowledge — credential", body: `Podiom never stores secrets, and this request never contained the value. Approving acknowledges it; set the variable in your environment and ${agent} picks it up at the next review.`, warn: false, confirmLabel: "Acknowledge" };
    }
  }

  async function confirmDialog() {
    if (!dialog) return;
    dialog = { ...dialog, busy: true };
    try {
      if (dialog.action === "approve") {
        await approveAccessRequest(dialog.req.ID, dialog.note);
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

  // ---- create -------------------------------------------------------------------

  async function openCreate() {
    view = "create";
    cAgent = cAgent || agents[0]?.Name || "";
    scrollTop();
    try {
      projects = await listProjects();
    } catch {
      projects = [];
    }
  }

  const canSubmit = $derived(cTitle.trim() !== "" && cAgent !== "");

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
      });
      cTitle = "";
      cDesc = "";
      cCriteria = "";
      cMetrics = [{ name: "", target: "", unit: "" }];
      cProject = "";
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
                    {@const failed = pend.some((r) => r.Status === "failed")}
                    {@const primary = g.Metrics[0]}
                    <button
                      class="card"
                      class:review-ring={g.Status === "review"}
                      class:attn-ring={g.Status !== "review" && pend.length > 0}
                      class:dim={g.Status === "done" || g.Status === "abandoned"}
                      onclick={() => openGoal(g.ID)}>
                      <div class="card-top">
                        <div class="card-meta">
                          <span class="pill mono" style="background:{bg};border-color:{bd};color:{tc}">
                            {#if pulse}<span class="pill-dot" style="background:{tc};box-shadow:0 0 0 3px {bg}"></span>{/if}
                            {lbl}
                          </span>
                          <span class="agent-chip">
                            <span class="avatar" style="background:{agentGradient(g.LeadAgent)}">{initial(g.LeadAgent)}</span>
                            {g.LeadAgent}
                          </span>
                          {#if g.ProjectID}<span class="proj mono">◆ {g.ProjectID}</span>{/if}
                        </div>
                        <svg class="chev" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#cdbfad" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6"/></svg>
                      </div>
                      <div class="card-title">{g.Title}</div>

                      {#if g.Status === "review" || pend.length > 0}
                        <div class="card-attn" class:hot={g.Status === "review"}>
                          <span class="attn-dot" class:hot={g.Status === "review"}></span>
                          {#if g.Status === "review"}
                            {g.LeadAgent} proposed completion — confirm or reopen
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
                        {#if pend.length > 0}
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
              <span class="avatar lg" style="background:{agentGradient(g.LeadAgent)}">{initial(g.LeadAgent)}</span>
              {g.LeadAgent}
            </span>
            {#if g.ProjectID}<span class="proj mono">◆ {g.ProjectID}</span>{/if}
          </div>
          <div class="detail-title">{g.Title}</div>
          <div class="detail-meta">
            <span class="meta-item">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--faint)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>
              reviews {cadenceLabel(g)}
            </span>
            <span class="meta-item next">next review <b>{nextLabel(g)}</b></span>
          </div>
        </div>
        <div class="detail-actions">
          {#if g.Status === "active"}
            <button class="btn teal" disabled={busy === g.ID} onclick={() => reviewNow(g)}>Review now</button>
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
                        <button class="btn-approve" onclick={() => (dialog = { req: r, action: "approve", note: "", busy: false })}>Approve</button>
                        <button class="btn-deny" onclick={() => (dialog = { req: r, action: "deny", note: "", busy: false })}>Deny</button>
                      </div>
                    {/if}
                  </div>
                </div>
              {/each}
            </div>
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
          </div>
          <div class="timeline">
            <div class="timeline-rule"></div>
            <div class="events">
              {#each detail.events as ev (ev.ID)}
                {@const k = EK[ev.Kind] ?? EK.progress}
                <div class="event">
                  <span class="event-dot" style="background:{k.t};border-color:{k.c}22">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke={k.c} stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{@html k.ic}</svg>
                  </span>
                  <div class="event-main">
                    <div class="event-top">
                      <span class="event-kind mono" style="color:{k.c}">{k.label}</span>
                      <span class="event-time mono">{relTime(ev.CreatedAt)}</span>
                    </div>
                    <div class="event-title">{eventTitle(ev)}</div>
                    {#if ev.Kind === "metric_update" && metricDelta(ev)}
                      <div class="event-delta mono">{metricDelta(ev)}</div>
                    {/if}
                    {#if eventBody(ev)}
                      <div class="event-body">{eventBody(ev)}</div>
                    {/if}
                    {#if ev.SessionID}
                      <button class="event-session mono" onclick={() => onOpenChat({ sessionId: ev.SessionID })}>
                        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
                        open session <span class="arrow">↗</span>
                      </button>
                    {/if}
                  </div>
                </div>
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

        <div class="field-label mono">Description</div>
        <textarea class="field" rows="3" bind:value={cDesc} placeholder="What is the outcome, and why does it matter? Give the agent the context a teammate would need."></textarea>

        <div class="field-label mono">Success criteria</div>
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
              <span class="avatar" style="background:{agentGradient(a.Name)}">{initial(a.Name)}</span>{a.Name}
            </button>
          {/each}
          {#if agents.length === 0}
            <span class="muted-note">Hire an agent first — a goal needs a lead.</span>
          {/if}
        </div>

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

        <button class="btn-primary submit" disabled={!canSubmit || cBusy} onclick={submitCreate}>
          {cBusy ? "Creating…" : "Create goal & start planning"}
        </button>
        <div class="submit-note">The agent begins planning in the background the moment you create it.</div>
      </div>
    </div>
  {/if}
</div>

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
    opacity: 0.86;
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
  .avatar {
    width: 20px;
    height: 20px;
    flex: none;
    border-radius: 6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 9px;
    font-weight: 800;
    color: #fff;
  }
  .avatar.lg {
    width: 22px;
    height: 22px;
    border-radius: 7px;
    font-size: 10px;
  }
  .proj {
    font-size: 11px;
    color: var(--faint);
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
  .banner-sub {
    font-size: 13px;
    line-height: 1.55;
    margin-top: 3px;
  }
  .violet-sub {
    color: #6e63a0;
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
    white-space: pre-wrap;
    overflow-wrap: anywhere;
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
  .agent-pick .avatar {
    width: 22px;
    height: 22px;
    border-radius: 7px;
    font-size: 10px;
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
