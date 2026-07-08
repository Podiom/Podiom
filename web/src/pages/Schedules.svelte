<script lang="ts">
  import { onMount } from "svelte";
  import { createSchedule, deleteSchedule, listGoals, listSchedules, runSchedule } from "../lib/api";
  import {
    capabilityKey,
    effortOptions as capabilityEffortOptions,
    loadProviderCapabilities,
  } from "../lib/capabilities";
  import { agentGradient, avatarStyle, initial, modeChip } from "../lib/theme";
  import type { Agent, Goal, ProviderCapabilities, RunStatus, ScheduleRun, ScheduleStatus } from "../lib/types";
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
  let error = $state<string | null>(null);
  let busy = $state<string>("");
  let hoverRun = $state<string>("");

  // Delete confirmation.
  let pendingDelete = $state<ScheduleStatus | null>(null);
  let deleteBusy = $state(false);
  let deleteError = $state<string | null>(null);

  // New-schedule modal.
  let creating = $state(false);
  let nsName = $state("");
  let nsCron = $state("0 7 * * *");
  let nsAgent = $state("");
  let nsModel = $state("");
  let nsEffort = $state("");
  let nsMode = $state("preapproved");
  let nsBody = $state("");
  let nsBusy = $state(false);

  const CRON_PRESETS = [
    { label: "Daily 07:00", v: "0 7 * * *" },
    { label: "Every 6 hours", v: "0 */6 * * *" },
    { label: "Hourly", v: "0 * * * *" },
    { label: "Weekdays 09:00", v: "0 9 * * 1-5" },
  ];
  let capabilitiesByKey = $state<Record<string, ProviderCapabilities>>({});
  let loadingCapabilities = new Set<string>();
  const selectedScheduleAgent = $derived(agents.find((a) => a.Name === nsAgent) ?? null);
  const scheduleCapabilityKey = $derived(
    selectedScheduleAgent ? capabilityKey(selectedScheduleAgent.Provider, selectedScheduleAgent.Profile) : "",
  );
  const scheduleCapabilities = $derived(scheduleCapabilityKey ? capabilitiesByKey[scheduleCapabilityKey] ?? null : null);
  const effortOptions = $derived(capabilityEffortOptions(scheduleCapabilities, nsModel || selectedScheduleAgent?.Model || "", nsEffort));

  $effect(() => {
    if (creating && selectedScheduleAgent) {
      void ensureCapabilities(selectedScheduleAgent.Provider, selectedScheduleAgent.Profile);
    }
  });

  async function ensureCapabilities(provider: Agent["Provider"], profile = "") {
    const key = capabilityKey(provider, profile);
    if (capabilitiesByKey[key] || loadingCapabilities.has(key)) return;
    loadingCapabilities.add(key);
    try {
      const caps = await loadProviderCapabilities(provider, profile);
      capabilitiesByKey = { ...capabilitiesByKey, [key]: caps };
    } catch {
      // Keep the free-form schedule model/effort inputs usable if offline.
    } finally {
      loadingCapabilities.delete(key);
    }
  }

  const nsSlug = $derived(
    (nsName.trim() || "untitled-job").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, ""),
  );
  const nsPreview = $derived(
    `---\nagent: ${nsAgent || "—"}\n` +
      (nsModel ? `model: ${nsModel}\n` : "") +
      (nsEffort ? `effort: ${nsEffort}\n` : "") +
      `cron: ${nsCron.trim() || "0 9 * * *"}\nrun_permission: ${nsMode}\nenabled: true\n---\n\n` +
      (nsBody.trim() || "<your prompt here>"),
  );

  onMount(load);

  function openNew() {
    nsName = "";
    nsCron = "0 7 * * *";
    nsAgent = agents.length ? agents[0].Name : "";
    nsModel = nsEffort = "";
    nsMode = "preapproved";
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
        model: nsModel,
        effort: nsEffort,
        cron: nsCron.trim(),
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
      const [scheduleList, goalList] = await Promise.all([listSchedules(), listGoals()]);
      schedules = scheduleList;
      goals = goalList;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  function goalFor(s: ScheduleStatus): Goal | null {
    if (!s.goal_id) return null;
    return goals.find((g) => g.ID === s.goal_id) ?? null;
  }

  const hasGoalLinkedSchedule = $derived(schedules.some((s) => !!goalFor(s)));

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
    return s.every ? `every ${s.every}` : s.cron;
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
    const fm = [
      { k: s.every ? "every" : "cron", v: timing(s) },
      { k: "agent", v: s.agent },
    ];
    if (s.model) fm.push({ k: "model", v: s.model });
    if (s.effort) fm.push({ k: "effort", v: s.effort });
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
</script>

<div class="page">
  <header class="page-head" style="max-width:820px">
    <div>
      <h1>Schedules</h1>
      <p>Every recurring job on this machine, one markdown file per job under <span class="mono" style="color:#8A7560">~/.podiom/schedules</span>. Some are part of a goal's plan — the agent created them on its own to work toward an outcome — others are schedules you set up directly.</p>
    </div>
    <span class="spacer"></span>
    <button class="head-cta" onclick={openNew}>+ New schedule</button>
  </header>

  {#if hasGoalLinkedSchedule}
    <div class="goal-legend mono" style="max-width:820px">
      <span class="goal-legend-dot"></span>goal marker
    </div>
  {/if}

  {#if error}<div class="error-banner" style="margin-bottom:14px;max-width:820px">{error}</div>{/if}

  <div class="sched-stack">
    {#each schedules as s}
      {@const goal = goalFor(s)}
      <article class="sched-card" class:goal-linked={!!goal}>
        {#if goal}<span class="goal-accent"></span>{/if}
        <div class="sched-top">
          <span style={avatarStyle(agentGradient(s.agent), 34, 11, 14)}>{initial(s.agent)}</span>
          <div class="sched-id">
            <div class="sched-title">{s.name}</div>
            {#if goal}
              <button class="goal-chip" onclick={() => onOpenGoal(goal.ID)} title="Open goal">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9" /><circle cx="12" cy="12" r="4.5" /><circle cx="12" cy="12" r="0.5" fill="currentColor" /></svg>
                {goal.Title}
              </button>
            {/if}
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
            </div>
          </div>

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
      </article>
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
      <div class="modal-body" style="max-height:76vh;overflow-y:auto">
        {#if error}<div class="error-banner" style="margin-bottom:14px">{error}</div>{/if}

        <div class="label-mono" style="margin-bottom:8px">name</div>
        <input class="field-input" bind:value={nsName} placeholder="e.g. nightly-dependency-audit" />

        <div class="label-mono" style="margin:18px 0 8px">schedule (cron)</div>
        <div class="ns-chips" style="margin-bottom:8px">
          {#each CRON_PRESETS as c}
            <button style={chip(c.v === nsCron)} onclick={() => (nsCron = c.v)}>{c.label}</button>
          {/each}
        </div>
        <input class="field-input mono" bind:value={nsCron} placeholder="0 7 * * *" style="font:500 13px 'JetBrains Mono',monospace" />

        <div class="label-mono" style="margin:18px 0 8px">agent</div>
        <div class="ns-chips">
          {#each agents as a}
            <button style={agentChip(nsAgent === a.Name)} onclick={() => (nsAgent = a.Name)}>
              <span style={avatarStyle(agentGradient(a.Name), 20, 6, 9)}>{initial(a.Name)}</span>{a.Name}
            </button>
          {/each}
        </div>

        <div class="ns-row">
          <span class="ns-key">model</span>
          <input class="field-input" style="flex:1" bind:value={nsModel} placeholder="agent default" />
        </div>
        <div class="ns-row">
          <span class="ns-key">effort</span>
          <div class="ns-chips">
            {#each effortOptions as e}<button style={chip(e === nsEffort)} onclick={() => (nsEffort = e)}>{e}</button>{/each}
          </div>
        </div>
        <div class="ns-row">
          <span class="ns-key">mode</span>
          <div class="ns-chips">
            {#each ["preapproved", "yolo"] as m}<button style={chip(m === nsMode)} onclick={() => (nsMode = m)}>{m}</button>{/each}
          </div>
        </div>

        <div class="label-mono" style="margin:18px 0 8px">prompt</div>
        <textarea class="field-area" rows="4" bind:value={nsBody} placeholder="What should the agent do on every run? This becomes the body of the markdown file." style="min-height:96px"></textarea>

        <div style="display:flex;align-items:center;gap:8px;margin:18px 0 7px">
          <span class="label-mono" style="flex:1">file preview</span>
          <span class="mono" style="font-size:11px;color:#8A7560">~/.podiom/schedules/{nsSlug}.md</span>
        </div>
        <pre class="ns-preview mono">{nsPreview}</pre>

        <button class="modal-cta" disabled={nsBusy || !nsName.trim() || !nsAgent || !nsBody.trim()} onclick={submitSchedule}>{nsBusy ? "Creating…" : "Create schedule file"}</button>
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

  .ns-key {
    font: 500 11px "Hanken Grotesk";
    color: #9a8e80;
    width: 46px;
    flex: none;
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

  .goal-legend {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    padding: 7px 13px;
    margin-bottom: 14px;
    border-radius: 999px;
    border: 1px solid #D8CFF3;
    background: #EEEAFB;
    color: #5847B8;
    font-size: 11.5px;
    font-weight: 600;
  }

  .goal-legend-dot {
    width: 8px;
    height: 8px;
    border-radius: 999px;
    background: linear-gradient(180deg, #7C6FE0, #5847B8);
    flex: none;
  }

  .sched-stack {
    display: flex;
    flex-direction: column;
    gap: 14px;
    max-width: 820px;
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

  .sched-card.goal-linked {
    border-color: #D8CFF3;
    padding-left: 23px;
  }

  .goal-accent {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 4px;
    background: linear-gradient(180deg, #7C6FE0, #5847B8);
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

  .goal-chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 11px 3px 9px;
    margin-top: 5px;
    border-radius: 999px;
    border: 1px solid #D8CFF3;
    background: #EEEAFB;
    color: #5847B8;
    font: 600 11.5px "Hanken Grotesk";
    cursor: pointer;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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

  .fm-k {
    color: var(--faint);
  }

  .fm-v {
    color: #5a5048;
    font-weight: 600;
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
