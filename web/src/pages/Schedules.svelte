<script lang="ts">
  import { onMount } from "svelte";
  import { answerAgentQuestion, createSchedule, deleteSchedule, listGoals, listProfiles, listSchedules, runSchedule } from "../lib/api";
  import AgentAvatar from "../lib/AgentAvatar.svelte";
  import AgentMarkdown from "../lib/AgentMarkdown.svelte";
  import RunTargetPicker from "../lib/RunTargetPicker.svelte";
  import WorkspaceFileLinks from "../lib/WorkspaceFileLinks.svelte";
  import type { RunTargetValue } from "../lib/RunTargetPicker.svelte";
  import { goalGroupedEntries, goalGroupOpen } from "../lib/goalGrouping";
  import { modeChip } from "../lib/theme";
  import type { AgentQuestion, Agent, Goal, ProfileInfo, RunStatus, ScheduleRun, ScheduleStatus } from "../lib/types";
  import ConfirmModal from "../lib/ConfirmModal.svelte";

  interface ChatTarget {
    sessionId?: string;
    agentName?: string;
    seed?: string;
  }

  let {
    agents = [],
    onOpenChat = (_t: ChatTarget) => {},
    onOpenGoal = (_id: string) => {},
  }: { agents?: Agent[]; onOpenChat?: (t: ChatTarget) => void; onOpenGoal?: (goalId: string) => void } = $props();

  let schedules = $state<ScheduleStatus[]>([]);
  let goals = $state<Goal[]>([]);
  let profiles = $state<ProfileInfo[]>([]);
  let error = $state<string | null>(null);
  let busy = $state<string>("");
  let hoverRun = $state<string>("");
  let goalGroupsOpen = $state<Record<string, boolean>>({});
  let inspectingSchedule = $state<ScheduleStatus | null>(null);

  // Deferred-question answering (podiom_ask_user), keyed by question item id.
  let questionAnswers = $state<Record<string, string[]>>({});
  let questionBusy = $state<string>("");

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
    questionBusy = pq.ID;
    error = null;
    try {
      await answerAgentQuestion(pq.ID, questionAnswers);
      questionAnswers = {};
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      questionBusy = "";
    }
  }

  // Delete confirmation.
  let pendingDelete = $state<ScheduleStatus | null>(null);
  let deleteBusy = $state(false);
  let deleteError = $state<string | null>(null);

  // New-schedule modal.
  let creating = $state(false);
  let nsName = $state("");
  let nsCron = $state("0 7 * * *");
  let nsAgent = $state("");
  let nsProvider = $state<RunTargetValue["provider"]>("");
  let nsProfile = $state("");
  let nsModel = $state("");
  let nsEffort = $state("");
  let nsMode = $state("preapproved");
  let nsWebhook = $state(false);
  let nsBody = $state("");
  let nsBusy = $state(false);
  let copiedWebhook = $state("");

  const CRON_PRESETS = [
    { label: "Daily 07:00", v: "0 7 * * *" },
    { label: "Every 6 hours", v: "0 */6 * * *" },
    { label: "Hourly", v: "0 * * * *" },
    { label: "Weekdays 09:00", v: "0 9 * * 1-5" },
  ];
  const selectedScheduleAgent = $derived(agents.find((a) => a.Name === nsAgent) ?? null);
  const scheduleEntries = $derived(goalGroupedEntries(schedules, (s) => s.goal_id, goals));

  const nsSlug = $derived(
    (nsName.trim() || "untitled-job").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, ""),
  );
  const nsPreview = $derived(
    `---\nagent: ${nsAgent || "—"}\n` +
      (nsProvider ? `provider: ${nsProvider}\n` : "") +
      (nsProfile ? `profile: ${nsProfile}\n` : "") +
      (nsModel ? `model: ${nsModel}\n` : "") +
      (nsEffort ? `effort: ${nsEffort}\n` : "") +
      (nsCron.trim() ? `cron: ${nsCron.trim()}\n` : "") +
      (nsWebhook ? "webhook: true\nwebhook_secret: <generated>\n" : "") +
      `run_permission: ${nsMode}\nenabled: true\n---\n\n` +
      (nsBody.trim() || "<your prompt here>"),
  );

  onMount(load);

  function openNew() {
    nsName = "";
    nsCron = "0 7 * * *";
    nsAgent = agents.length ? agents[0].Name : "";
    nsProvider = "";
    nsProfile = "";
    nsModel = nsEffort = "";
    nsMode = "preapproved";
    nsWebhook = false;
    nsBody = "";
    error = null;
    creating = true;
  }

  async function submitSchedule() {
    nsBusy = true;
    error = null;
    try {
      await createSchedule({
        name: nsName.trim(),
        agent: nsAgent,
        provider: nsProvider,
        profile: nsProfile,
        model: nsModel,
        effort: nsEffort,
        cron: nsCron.trim(),
        webhook: nsWebhook,
        run_permission: nsMode,
        body: nsBody.trim(),
      });
      creating = false;
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      nsBusy = false;
    }
  }

  function chip(on: boolean): string {
    return (
      "padding:6px 12px;border-radius:9px;cursor:pointer;font:600 12px 'JetBrains Mono',monospace;" +
      (on
        ? "border:1px solid #BFE0D6;background:#E3F1EC;color:#2F6E60"
        : "border:1px solid #EAE0D4;background:#fff;color:#6F6459")
    );
  }

  function agentChip(on: boolean): string {
    return (
      "display:inline-flex;align-items:center;gap:8px;padding:5px 13px 5px 5px;border-radius:11px;cursor:pointer;font:600 12.5px 'Hanken Grotesk';" +
      (on
        ? "border:1px solid #BFE0D6;background:#E3F1EC;color:#2F6E60"
        : "border:1px solid #EAE0D4;background:#fff;color:#6F6459")
    );
  }

  async function load() {
    try {
      const [scheduleList, goalList, profileList] = await Promise.all([listSchedules(), listGoals(), listProfiles()]);
      schedules = scheduleList;
      goals = goalList;
      profiles = profileList;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  function toggleGoalGroup(key: string) {
    goalGroupsOpen = { ...goalGroupsOpen, [key]: !goalGroupOpen(goalGroupsOpen, key) };
  }

  function groupCountLabel(count: number, noun: string): string {
    return `${count} ${noun}${count === 1 ? "" : "s"}`;
  }

  async function runNow(name: string) {
    busy = name;
    error = null;
    try {
      await runSchedule(name);
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  async function confirmDeleteSchedule() {
    if (!pendingDelete) return;
    deleteBusy = true;
    deleteError = null;
    try {
      await deleteSchedule(pendingDelete.name);
      pendingDelete = null;
      await load();
    } catch (e) {
      deleteError = e instanceof Error ? e.message : String(e);
    } finally {
      deleteBusy = false;
    }
  }

  function timing(s: ScheduleStatus) {
    const cadence = s.every ? `every ${s.every}` : s.cron;
    if (cadence) return cadence;
    // A webhook-only schedule has no cadence: it fires when its URL is called.
    return s.webhook ? "on webhook" : "—";
  }

  // The daemon does not know the URL it is reached on, so the address the user
  // must give the sender is composed from the one the browser is already using.
  function webhookURL(s: ScheduleStatus) {
    return `${location.origin}/api/schedules/${encodeURIComponent(s.name)}/webhook?secret=${s.webhook_secret ?? ""}`;
  }

  async function copyWebhookURL(s: ScheduleStatus) {
    try {
      await navigator.clipboard.writeText(webhookURL(s));
      copiedWebhook = s.name;
      setTimeout(() => copiedWebhook === s.name && (copiedWebhook = ""), 1600);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  function nextLabel(s: ScheduleStatus) {
    if (!s.next_run) return "—";
    return new Date(s.next_run).toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" });
  }

  function runWhen(r: ScheduleRun) {
    const t = r.StartedAt || r.FinishedAt;
    if (!t) return "—";
    return new Date(t).toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" });
  }

  function runColor(status: RunStatus): string {
    if (status === "error") return "#C0492A";
    if (status === "running") return "#D88A2C";
    return "#4F9E78";
  }

  function frontmatter(s: ScheduleStatus): { k: string; v: string }[] {
    const cadenceKey = s.every ? "every" : s.cron ? "cron" : "trigger";
    const fm = [
      { k: cadenceKey, v: timing(s) },
      { k: "agent", v: s.agent },
    ];
    // Only worth its own chip when the cadence chip did not already say it.
    if (s.webhook && cadenceKey !== "trigger") fm.push({ k: "webhook", v: "true" });
    if (s.model) fm.push({ k: "model", v: s.model });
    if (s.effort) fm.push({ k: "effort", v: s.effort });
    if (s.provider) fm.push({ k: "provider", v: s.provider });
    if (s.profile) fm.push({ k: "profile", v: s.profile });
    fm.push({ k: "mode", v: s.run_permission });
    if (s.goal_id) fm.push({ k: "origin", v: "goal plan" });
    return fm;
  }

  function runsSummary(s: ScheduleStatus): string {
    const runs = s.runs || [];
    if (!runs.length) return "no runs yet";
    const errs = runs.filter((r) => r.Status === "error").length;
    return `${runs.length} run${runs.length === 1 ? "" : "s"} · ${errs ? errs + " failed" : "all clean"}`;
  }

  function scheduleClickIgnored(event: Event): boolean {
    const target = event.target;
    return target instanceof Element && Boolean(target.closest("button,input,textarea,select,a"));
  }

  function openScheduleInstructions(s: ScheduleStatus, event: Event) {
    if (s.parse_error || scheduleClickIgnored(event)) return;
    inspectingSchedule = s;
  }

  function openScheduleInstructionsWithKeyboard(s: ScheduleStatus, event: KeyboardEvent) {
    if (event.key !== "Enter" && event.key !== " ") return;
    if (s.parse_error || scheduleClickIgnored(event)) return;
    event.preventDefault();
    inspectingSchedule = s;
  }
</script>

{#snippet scheduleCardContent(s: ScheduleStatus)}
  <div class="sched-top">
    <AgentAvatar name={s.agent} size={34} radius={11} fontSize={14} />
    <div class="sched-id">
      <div class="sched-title">{s.name}</div>
      <div class="sched-file mono">{s.path}</div>
    </div>
    {#if s.parse_error}
      <span style={modeChip("yolo")}>parse error</span>
    {:else}
      <span style={modeChip(s.run_permission === "yolo" ? "yolo" : "approve")}>{s.run_permission}</span>
      <span class="sched-next">next {nextLabel(s)}</span>
      <button class="sched-run" disabled={busy === s.name} onclick={() => runNow(s.name)}>{busy === s.name ? "Running…" : "Run now"}</button>
    {/if}
    <button class="sched-x" title="Delete schedule" aria-label="Delete schedule" onclick={() => (pendingDelete = s)}>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
    </button>
  </div>

  {#if s.parse_error}
    <div class="error-banner" style="margin-top:14px">{s.parse_error}</div>
  {:else}
    <div class="sched-fm">
      <div class="sched-fm-row">
        {#each frontmatter(s) as f}
          <span class="fm-chip mono"><span class="fm-k">{f.k}</span><span class="fm-v">{f.v}</span></span>
        {/each}
        <span class="fm-chip mono"><span class="fm-k">enabled</span><span class="fm-v">{s.enabled ? "true" : "false"}</span></span>
        {#if s.created_by_agent}
          <button
            class="fm-chip fm-chip-link mono"
            disabled={!s.created_by_session}
            title={s.created_by_session ? "Open the conversation this schedule was created in" : "Created by an agent"}
            onclick={() => s.created_by_session && onOpenChat({ sessionId: s.created_by_session })}
          >
            <span class="fm-k">created by</span><span class="fm-v">{s.created_by_agent}</span>
          </button>
        {/if}
      </div>
    </div>

    {#if s.webhook && s.webhook_secret}
      <div class="sched-webhook">
        <span class="label-mono" style="font-size:10px">webhook url</span>
        <code class="sched-webhook-url mono">{webhookURL(s)}</code>
        <button class="sched-webhook-copy" onclick={() => copyWebhookURL(s)}>{copiedWebhook === s.name ? "Copied" : "Copy"}</button>
      </div>
    {/if}

    {#if s.pending_question}
      {@const pq = s.pending_question}
      <div class="sched-question">
        <div class="sq-head">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="#9a6e1e" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9.1 9a3 3 0 1 1 4 2.8c-.8.4-1.1 1-1.1 2"/><path d="M12 17h.01"/></svg>
          <span>{s.agent} asked a question — answer it to guide the next run</span>
        </div>
        {#each pq.Questions as item}
          <div class="sq-block">
            {#if item.header}<div class="sq-header">{item.header}</div>{/if}
            <div class="sq-text"><AgentMarkdown content={item.question} /></div>
            {#if item.options && item.options.length > 0}
              <div class="sq-options">
                {#each item.options as option}
                  <button class="sq-option" class:sel={qSelected(item.id, option.label)} onclick={() => qToggle(item, option.label)}>
                    <span class="sq-dot">{item.multi_select ? (qSelected(item.id, option.label) ? "✓" : "") : ""}</span>
                    <span class="sq-option-text">
                      <span>{option.label}</span>
                      {#if option.description}<small><AgentMarkdown content={option.description} /></small>{/if}
                    </span>
                  </button>
                {/each}
              </div>
            {:else}
              <input
                class="sq-free"
                type={item.is_secret ? "password" : "text"}
                placeholder="Your answer"
                value={(questionAnswers[item.id] ?? [])[0] ?? ""}
                oninput={(e) => qSetFree(item.id, e.currentTarget.value)}
              />
            {/if}
          </div>
        {/each}
        <button class="sq-send" disabled={questionBusy === pq.ID || !qReady(pq)} onclick={() => submitQuestionAnswer(pq)}>
          {questionBusy === pq.ID ? "Sending…" : "Send answer"}
        </button>
      </div>
    {/if}

    {#if s.runs && s.runs.length > 0}
      <div class="sched-runs">
        <div class="label-mono" style="font-size:10px;margin-bottom:9px">runs · {runsSummary(s)} · open any to see that session</div>
        <div class="run-chips">
          {#each s.runs as r (r.ID)}
            {@const open = hoverRun === r.ID}
            <button
              class="run-chip"
              title={runWhen(r)}
              style="gap:{open ? '7px' : '0'};padding:5px {open ? '11px' : '7px'};border-color:{open ? '#E4D9CB' : '#EFE6DB'}"
              onmouseenter={() => (hoverRun = r.ID)}
              onmouseleave={() => (hoverRun = "")}
              onclick={() => r.SessionID && onOpenChat({ sessionId: r.SessionID })}
            >
              <span class="run-dot" style="background:{runColor(r.Status)}"></span>
              <span class="run-label" style="max-width:{open ? '160px' : '0'};opacity:{open ? '1' : '0'}">{runWhen(r)}</span>
            </button>
          {/each}
        </div>
      </div>
    {/if}
  {/if}
{/snippet}

{#snippet scheduleCard(s: ScheduleStatus)}
  {#if s.parse_error}
    <article class="sched-card">
      {@render scheduleCardContent(s)}
    </article>
  {:else}
    <div class="sched-card clickable" tabindex="0" role="button" aria-label={`View instructions for ${s.name}`} onclick={(e) => openScheduleInstructions(s, e)} onkeydown={(e) => openScheduleInstructionsWithKeyboard(s, e)}>
      {@render scheduleCardContent(s)}
    </div>
  {/if}
{/snippet}

<div class="page">
  <header class="page-head" style="max-width:820px">
    <div>
      <h1>Schedules</h1>
      <p>Every recurring job on this machine, one markdown file per job under <span class="mono" style="color:#8A7560">~/.podiom/schedules</span>. Some are part of a goal's plan — the agent created them on its own to work toward an outcome — others are schedules you set up directly.</p>
    </div>
    <span class="spacer"></span>
    <button class="head-cta" onclick={openNew}>+ New schedule</button>
  </header>

  {#if error}<div class="error-banner" style="margin-bottom:14px;max-width:820px">{error}</div>{/if}

  <div class="sched-stack">
    {#each scheduleEntries as entry}
      {#if entry.kind === "group"}
        <section class="schedule-goal-group">
          <div class="schedule-goal-head">
            <button class="schedule-goal-toggle" onclick={() => toggleGoalGroup(entry.goalId)} title={goalGroupOpen(goalGroupsOpen, entry.goalId) ? "Collapse goal group" : "Expand goal group"}>
              <span class="goal-chevron" class:closed={!goalGroupOpen(goalGroupsOpen, entry.goalId)}>⌄</span>
              <span class="schedule-goal-text">
                <span class="schedule-goal-title">{entry.label}</span>
                <span class="schedule-goal-sub mono">{groupCountLabel(entry.items.length, "schedule")}{entry.goal ? ` · ${entry.goal.Status}` : ""}</span>
              </span>
            </button>
            {#if entry.goal}
              <button class="schedule-goal-open" onclick={() => onOpenGoal(entry.goalId)} title="Open goal" aria-label="Open goal">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9" /><circle cx="12" cy="12" r="4.5" /><circle cx="12" cy="12" r="0.5" fill="currentColor" /></svg>
              </button>
            {/if}
          </div>
          {#if goalGroupOpen(goalGroupsOpen, entry.goalId)}
            <div class="schedule-goal-items">
              {#each entry.items as s (s.name)}
                {@render scheduleCard(s)}
              {/each}
            </div>
          {/if}
        </section>
      {:else}
        {@render scheduleCard(entry.item)}
      {/if}
    {/each}
    {#if schedules.length === 0}
      <p class="empty-note">No schedules. Drop a <span class="mono">*.md</span> file in <span class="mono">~/.podiom/schedules/</span>.</p>
    {/if}
  </div>
</div>

{#if creating}
  <div class="modal-backdrop" role="presentation" onclick={() => (creating = false)}>
    <div class="modal-card ns-modal" role="dialog" aria-modal="true" aria-label="New schedule" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <div class="modal-head">
        <div class="modal-title">New schedule</div>
        <div class="modal-sub">Creates a markdown file under <span class="mono">~/.podiom/schedules</span>. The frontmatter sets the engine; the body is the prompt the agent runs on each tick.</div>
      </div>
      <div class="modal-body">
        {#if error}<div class="error-banner" style="margin-bottom:14px">{error}</div>{/if}

        <div class="label-mono" style="margin-bottom:8px">name</div>
        <input class="field-input" bind:value={nsName} placeholder="e.g. nightly-dependency-audit" />

        <div class="label-mono" style="margin:18px 0 8px">schedule (cron)</div>
        <div class="ns-chips" style="margin-bottom:8px">
          {#each CRON_PRESETS as c}
            <button style={chip(c.v === nsCron)} onclick={() => (nsCron = c.v)}>{c.label}</button>
          {/each}
          {#if nsWebhook}
            <button style={chip(nsCron === "")} onclick={() => (nsCron = "")}>No cron</button>
          {/if}
        </div>
        <input class="field-input mono" bind:value={nsCron} placeholder="0 7 * * *" style="font:500 13px 'JetBrains Mono',monospace" />

        <div class="ns-row">
          <span class="ns-key">webhook</span>
          <div class="ns-chips">
            <button style={chip(nsWebhook)} onclick={() => (nsWebhook = !nsWebhook)}>{nsWebhook ? "on" : "off"}</button>
            <span class="ns-hint">
              {#if nsWebhook}
                Also fires when an outside service POSTs to this schedule's URL. Podiom generates the secret; copy the URL from the card after creating. Leave the cron blank for a webhook-only job.
              {:else}
                Let an outside service fire this schedule by calling a URL.
              {/if}
            </span>
          </div>
        </div>

        <div class="label-mono" style="margin:18px 0 8px">agent</div>
        <div class="ns-chips">
          {#each agents as a}
            <button style={agentChip(nsAgent === a.Name)} onclick={() => (nsAgent = a.Name)}>
              <AgentAvatar name={a.Name} size={20} radius={6} fontSize={9} />{a.Name}
            </button>
          {/each}
        </div>

        <div class="ns-row target-row">
          <span class="ns-key">run</span>
          <RunTargetPicker
            agent={selectedScheduleAgent}
            {profiles}
            variant="stacked"
            value={{ provider: nsProvider, profile: nsProfile, model: nsModel, effort: nsEffort }}
            onChange={(next) => {
              nsProvider = next.provider || "";
              nsProfile = next.profile || "";
              nsModel = next.model || "";
              nsEffort = next.effort || "";
            }}
          />
        </div>
        <div class="ns-row">
          <span class="ns-key">mode</span>
          <div class="ns-chips">
            {#each ["preapproved", "yolo"] as m}<button style={chip(m === nsMode)} onclick={() => (nsMode = m)}>{m}</button>{/each}
          </div>
        </div>

        <div class="label-mono" style="margin:18px 0 8px">prompt</div>
        <textarea class="field-area" rows="4" bind:value={nsBody} placeholder="What should the agent do on every run? This becomes the body of the markdown file." style="min-height:96px"></textarea>
        <WorkspaceFileLinks content={nsBody} />

        <div style="display:flex;align-items:center;gap:8px;margin:18px 0 7px">
          <span class="label-mono" style="flex:1">file preview</span>
          <span class="mono" style="font-size:11px;color:#8A7560">~/.podiom/schedules/{nsSlug}.md</span>
        </div>
        <pre class="ns-preview mono">{nsPreview}</pre>

        <button class="modal-cta" disabled={nsBusy || !nsName.trim() || !nsAgent || !nsBody.trim() || (!nsCron.trim() && !nsWebhook)} onclick={submitSchedule}>{nsBusy ? "Creating…" : "Create schedule file"}</button>
      </div>
    </div>
  </div>
{/if}

{#if inspectingSchedule}
  <div class="modal-backdrop" role="presentation" onclick={() => (inspectingSchedule = null)}>
    <div class="modal-card instruction-modal" role="dialog" aria-modal="true" aria-label="Schedule instructions" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <div class="modal-head">
        <div class="modal-title">{inspectingSchedule.name}</div>
        <div class="modal-sub">
          {timing(inspectingSchedule)} · {inspectingSchedule.agent} · {inspectingSchedule.path}
        </div>
      </div>
      <div class="modal-body">
        <div class="instruction-content"><AgentMarkdown content={inspectingSchedule.body || ""} /></div>
        <button class="modal-cta instruction-close" onclick={() => (inspectingSchedule = null)}>Close</button>
      </div>
    </div>
  </div>
{/if}

{#if pendingDelete}
  <ConfirmModal
    title="Delete {pendingDelete.name}"
    message="This deletes the schedule file and its run history. Sessions produced by past runs are kept."
    confirmLabel="Delete schedule"
    busy={deleteBusy}
    error={deleteError}
    onConfirm={confirmDeleteSchedule}
    onCancel={() => (pendingDelete = null)}
  />
{/if}

<style>
  .ns-modal {
    width: 560px;
    max-width: 94vw;
  }

  .ns-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .ns-row {
    display: flex;
    align-items: center;
    gap: 9px;
    margin-top: 11px;
  }

  .ns-row.target-row {
    align-items: flex-start;
  }

  .ns-key {
    font: 500 11px "Hanken Grotesk";
    color: #9a8e80;
    width: 46px;
    flex: none;
  }

  .ns-hint {
    font: 400 11.5px/1.5 "Hanken Grotesk";
    color: #9a8e80;
    flex: 1;
    min-width: 180px;
  }

  .ns-preview {
    margin: 0;
    background: #2b2520;
    border-radius: 12px;
    padding: 14px 16px;
    font: 400 12px/1.65 "JetBrains Mono", monospace;
    color: #e4d9c9;
    white-space: pre-wrap;
    word-break: break-word;
    overflow: auto;
    max-height: 200px;
  }

  .sched-stack {
    display: flex;
    flex-direction: column;
    gap: 14px;
    max-width: 820px;
  }

  .schedule-goal-group {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 12px;
    border: 1px solid #ddd4ef;
    border-radius: 18px;
    background: #f4f1fb;
  }

  .schedule-goal-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .schedule-goal-toggle {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 7px 8px;
    border: none;
    border-radius: 11px;
    background: transparent;
    color: #5847b8;
    cursor: pointer;
    text-align: left;
  }

  .schedule-goal-toggle:hover {
    background: rgba(255, 255, 255, 0.62);
  }

  .goal-chevron {
    flex: none;
    font: 800 15px "Hanken Grotesk";
    transition: transform 0.12s ease;
  }

  .goal-chevron.closed {
    transform: rotate(-90deg);
  }

  .schedule-goal-text {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }

  .schedule-goal-title {
    font: 800 14px "Hanken Grotesk";
    color: #5847b8;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .schedule-goal-sub {
    font-size: 10.5px;
    color: #8172c8;
    margin-top: 1px;
  }

  .schedule-goal-open {
    flex: none;
    width: 31px;
    height: 31px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 10px;
    border: 1px solid #d8cff3;
    background: #fff;
    color: #5847b8;
    cursor: pointer;
  }

  .schedule-goal-open:hover {
    background: #eeeafb;
  }

  .schedule-goal-items {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .sched-card {
    position: relative;
    overflow: hidden;
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 18px;
    padding: 20px;
    box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04), 0 14px 36px -26px rgba(43, 37, 32, 0.2);
  }

  .sched-card.clickable {
    cursor: pointer;
  }

  .sched-card.clickable:hover {
    border-color: #cfe3dd;
  }

  .sched-card.clickable:focus-visible {
    outline: 2px solid #8bc8b8;
    outline-offset: 3px;
  }

  .sched-top {
    display: flex;
    align-items: center;
    gap: 13px;
  }

  .sched-id {
    flex: 1;
    min-width: 0;
  }

  .sched-title {
    font: 700 16px "Hanken Grotesk";
  }

  .sched-file {
    font: 400 12px "JetBrains Mono", monospace;
    color: #9a8e80;
    margin-top: 2px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .sched-next {
    font: 500 12.5px "Hanken Grotesk";
    color: #a8825e;
    min-width: 64px;
    text-align: right;
  }

  .sched-run {
    padding: 8px 14px;
    border: 1px solid var(--field-line);
    border-radius: 10px;
    background: #fff;
    color: var(--teal-deep);
    font: 600 12.5px "Hanken Grotesk";
    cursor: pointer;
  }

  .sched-x {
    flex: none;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    border: 1px solid #e7c3b5;
    border-radius: 9px;
    background: #fff;
    color: #a23e22;
    cursor: pointer;
  }

  .sched-x:hover {
    background: #fbeeea;
  }

  .sched-fm {
    margin-top: 15px;
    background: var(--surface-3);
    border: 1px solid var(--line-3);
    border-radius: 13px;
    overflow: hidden;
  }

  .sched-fm-row {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
    padding: 13px 15px;
  }

  .fm-chip {
    display: inline-flex;
    align-items: baseline;
    gap: 6px;
    padding: 4px 10px;
    border-radius: 8px;
    background: #fff;
    border: 1px solid var(--field-line);
    font: 500 11.5px "JetBrains Mono", monospace;
  }

  /* The one chip that is a link: it opens the conversation the agent created
     this schedule in. Styled as the other chips so the row stays even. */
  .fm-chip-link {
    cursor: pointer;
    color: inherit;
    transition: border-color 0.15s ease;
  }

  .fm-chip-link:hover:not(:disabled) {
    border-color: #e4d9cb;
  }

  .fm-chip-link:disabled {
    cursor: default;
  }

  /* The address an outside service POSTs to. It carries the schedule's secret,
     so it is shown to copy rather than to read: the URL is allowed to overflow
     into its own scroll instead of wrapping the card. */
  .sched-webhook {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 11px;
    padding: 10px 13px;
    background: var(--surface-3);
    border: 1px solid var(--line-3);
    border-radius: 11px;
  }

  .sched-webhook-url {
    flex: 1;
    min-width: 0;
    overflow-x: auto;
    white-space: nowrap;
    font: 500 11.5px "JetBrains Mono", monospace;
    color: #6f6459;
  }

  .sched-webhook-copy {
    flex: none;
    padding: 5px 12px;
    border-radius: 8px;
    border: 1px solid var(--field-line);
    background: #fff;
    cursor: pointer;
    font: 600 11.5px "JetBrains Mono", monospace;
    color: #6f6459;
    transition: border-color 0.15s ease;
  }

  .sched-webhook-copy:hover {
    border-color: #e4d9cb;
  }

  .fm-k {
    color: var(--faint);
  }

  .fm-v {
    color: #5a5048;
    font-weight: 600;
  }

  .sched-question {
    margin-top: 15px;
    background: #fdf6e7;
    border: 1px solid #ecd9ae;
    border-radius: 13px;
    padding: 14px 16px;
    display: flex;
    flex-direction: column;
    gap: 11px;
    align-items: flex-start;
  }

  .sq-head {
    display: flex;
    align-items: center;
    gap: 8px;
    font: 600 12.5px "Hanken Grotesk";
    color: #9a6e1e;
  }

  .sq-block {
    display: flex;
    flex-direction: column;
    gap: 6px;
    width: 100%;
  }

  .sq-header {
    font: 700 10.5px "JetBrains Mono", monospace;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: #9a6e1e;
  }

  .sq-text {
    font: 600 13px "Hanken Grotesk";
    color: #4a3f30;
  }

  .sq-options {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .sq-option {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    text-align: left;
    padding: 8px 11px;
    border-radius: 10px;
    border: 1px solid #e6dcc4;
    background: #fff;
    cursor: pointer;
  }

  .sq-option:hover {
    border-color: #d9c69a;
  }

  .sq-option.sel {
    border-color: #c69a3f;
    background: #fbf1dd;
  }

  .sq-dot {
    width: 15px;
    flex: none;
    color: #9a6e1e;
    font-weight: 700;
  }

  .sq-option-text {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font: 500 13px "Hanken Grotesk";
  }

  .sq-option-text small {
    color: #8a7f6a;
    font-size: 11.5px;
  }

  .sq-free {
    width: 100%;
    max-width: 420px;
    padding: 8px 11px;
    border-radius: 10px;
    border: 1px solid #e6dcc4;
    background: #fff;
    font: inherit;
  }

  .sq-send {
    padding: 8px 16px;
    border-radius: 10px;
    border: 1px solid #c69a3f;
    background: #9a6e1e;
    color: #fff;
    font: 600 12.5px "Hanken Grotesk";
    cursor: pointer;
  }

  .sq-send:disabled {
    opacity: 0.55;
    cursor: default;
  }

  .sched-runs {
    margin-top: 15px;
  }

  .run-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
  }

  .run-chip {
    display: inline-flex;
    align-items: center;
    border-radius: 9px;
    border: 1px solid var(--line-3);
    background: #fff;
    cursor: pointer;
    font: 500 11.5px "JetBrains Mono", monospace;
    color: #6f5b45;
    transition:
      gap 0.14s ease,
      padding 0.14s ease;
  }

  .run-dot {
    width: 9px;
    height: 9px;
    border-radius: 99px;
    flex: none;
  }

  .run-label {
    overflow: hidden;
    white-space: nowrap;
    transition:
      max-width 0.16s ease,
      opacity 0.14s ease;
  }

  .instruction-modal {
    width: 620px;
    max-width: 94vw;
  }

  .instruction-content {
    border: 1px solid var(--line-3);
    border-radius: 13px;
    background: var(--surface-3);
    padding: 16px 18px;
    color: #3d342c;
    font: 400 14px/1.55 "Hanken Grotesk";
  }

  .instruction-content :global(:first-child) {
    margin-top: 0;
  }

  .instruction-content :global(:last-child) {
    margin-bottom: 0;
  }

  .instruction-content :global(pre) {
    overflow: auto;
    border-radius: 10px;
    padding: 12px;
    background: #2b2520;
    color: #e4d9c9;
  }

  .instruction-content :global(code) {
    font-family: "JetBrains Mono", monospace;
    font-size: 12.5px;
  }

  .instruction-close {
    margin-top: 16px;
  }

  @media (max-width: 768px) {
    .sched-stack {
      max-width: none;
    }

    .sched-card {
      padding: 16px;
    }

    .sched-top {
      align-items: flex-start;
      flex-wrap: wrap;
      gap: 10px;
    }

    .sched-id {
      flex-basis: calc(100% - 48px);
    }

    .sched-file {
      max-width: 100%;
    }

    .sched-next {
      min-width: 0;
      text-align: left;
    }

    .sched-run {
      flex: 1 1 100%;
      justify-content: center;
    }

    .ns-row {
      align-items: stretch;
      flex-direction: column;
      gap: 7px;
    }

    .ns-key {
      width: auto;
    }

    .ns-preview {
      max-height: 180px;
    }
  }
</style>
