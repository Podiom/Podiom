<script lang="ts">
  import { onMount } from "svelte";
  import {
    analyzeProject,
    connectProjectRepo,
    createProject,
    createProjectFromGitHub,
    deleteProject,
    describeProject,
    disconnectProjectRepo,
    githubDevicePoll,
    githubDeviceStart,
    githubRepos,
    githubStatus,
    getProjectInstructions,
    listProjects,
    listTasks,
    syncProjectRepo,
    updateProject,
    updateProjectInstructions,
  } from "../lib/api";
  import ConfirmModal from "../lib/ConfirmModal.svelte";
  import { PROJECT_COLORS, projectColor } from "../lib/theme";
  import type { Agent, GitHubDeviceStart, GitHubRepo, GitHubStatus, Project, Task } from "../lib/types";

  interface ChatTarget {
    sessionId?: string;
    agentName?: string;
    seed?: string;
  }

  let {
    agents = [],
    onOpenChat = (_t: ChatTarget) => {},
  }: { agents?: Agent[]; onOpenChat?: (t: ChatTarget) => void } = $props();

  let projects = $state<Project[]>([]);
  let tasks = $state<Task[]>([]);
  let error = $state<string | null>(null);
  // Per-project description drafts (edited locally, saved on demand).
  let drafts = $state<Record<string, string>>({});
  let instructionDrafts = $state<Record<string, string>>({});
  let instructionDraftsSaved = $state<Record<string, string>>({});
  let instructionPaths = $state<Record<string, string>>({});
  let busyDescribe = $state<string>("");
  let savingDesc = $state<string>("");
  let savingInstructions = $state<string>("");
  // Which agent's engine drafts descriptions.
  let writerAgent = $state("");
  // Delete-project confirmation.
  let deleteTarget = $state<Project | null>(null);
  let deleting = $state(false);

  const deleteMessage = $derived(
    deleteTarget
      ? (() => {
          const n = taskCount(deleteTarget.id);
          const tail =
            n > 0
              ? `Its ${n} task${n === 1 ? "" : "s"} and any sessions are kept but orphaned (they stay in history without a project).`
              : "Any tasks or sessions are kept but orphaned (they stay in history without a project).";
          return `Removes “${deleteTarget.name}” from the project list. Files on disk are left untouched. ${tail}`;
        })()
      : "",
  );

  $effect(() => {
    if (!agents.length) {
      writerAgent = "";
      return;
    }
    if (!writerAgent || !agents.some((a) => a.Name === writerAgent)) {
      writerAgent = agents[0].Name;
    }
  });

  // New-project modal.
  let creating = $state(false);
  let npName = $state("");
  let npDescription = $state("");
  let npStack = $state("");
  let npNotes = $state("");
  let npConnectGitHub = $state(false);

  // GitHub connection modal.
  let ghMode = $state<"connect" | "create">("connect");
  let ghModalOpen = $state(false);
  let ghOpen = $state<Project | null>(null);
  let ghStatus = $state<GitHubStatus | null>(null);
  let ghRepos = $state<GitHubRepo[]>([]);
  let ghSelected = $state("");
  let ghBusy = $state("");
  let ghDevice = $state<GitHubDeviceStart | null>(null);
  let ghReplacePending = $state(false);
  let ghInstallOpened = $state(false);
  let ghJustConnected = $state(false);
  let ghAnalyzing = $state(false);
  let ghAnalyzeWarning = $state("");
  let ghCreated = $state<Project | null>(null);
  let ghAuthWindow: Window | null = null;
  let ghPollTimer: number | undefined;

  onMount(() => {
    void load();
    const refreshAfterGitHub = () => {
      if (ghModalOpen && ghStatus?.authed && ghInstallOpened && !ghBusy) {
        void refreshGitHub();
      }
    };
    window.addEventListener("focus", refreshAfterGitHub);
    return () => {
      window.removeEventListener("focus", refreshAfterGitHub);
      clearGitHubPolling();
    };
  });

  async function load() {
    try {
      [projects, tasks] = await Promise.all([listProjects(), listTasks()]);
      drafts = Object.fromEntries(projects.map((p) => [p.id, p.description]));
      const instructionInfos = await Promise.all(projects.map((p) => getProjectInstructions(p.id)));
      instructionDrafts = Object.fromEntries(instructionInfos.map((info) => [info.project_id, info.instructions]));
      instructionDraftsSaved = Object.fromEntries(instructionInfos.map((info) => [info.project_id, info.instructions]));
      instructionPaths = Object.fromEntries(instructionInfos.map((info) => [info.project_id, info.path]));
      if (!writerAgent && agents.length) writerAgent = agents[0].Name;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  function color(p: Project): string {
    return p.color || projectColor(p.id);
  }

  function taskCount(id: string): number {
    return tasks.filter((t) => t.ProjectID === id).length;
  }

  function meta(p: Project): string {
    const t = taskCount(p.id);
    const r = p.roadmap?.length ?? 0;
    return `${t} task${t === 1 ? "" : "s"} · ${r} in roadmap`;
  }

  function dirty(p: Project): boolean {
    return (drafts[p.id] ?? "") !== p.description;
  }

  function instructionsDirty(p: Project): boolean {
    const path = instructionPaths[p.id];
    return Boolean(path) && (instructionDrafts[p.id] ?? "") !== (instructionDraftsSaved[p.id] ?? "");
  }

  async function setColor(p: Project, c: string) {
    try {
      const updated = await updateProject(p.id, { color: c });
      projects = projects.map((x) => (x.id === p.id ? updated : x));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function saveDesc(p: Project) {
    savingDesc = p.id;
    error = null;
    try {
      const updated = await updateProject(p.id, { description: drafts[p.id] ?? "" });
      projects = projects.map((x) => (x.id === p.id ? updated : x));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      savingDesc = "";
    }
  }

  async function saveInstructions(p: Project) {
    savingInstructions = p.id;
    error = null;
    try {
      const info = await updateProjectInstructions(p.id, instructionDrafts[p.id] ?? "");
      instructionDrafts = { ...instructionDrafts, [p.id]: info.instructions };
      instructionDraftsSaved = { ...instructionDraftsSaved, [p.id]: info.instructions };
      instructionPaths = { ...instructionPaths, [p.id]: info.path };
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      savingInstructions = "";
    }
  }

  async function describe(p: Project) {
    if (!writerAgent) {
      error = "Hire an agent first — descriptions are drafted by an agent's engine.";
      return;
    }
    busyDescribe = p.id;
    error = null;
    try {
      const text = await describeProject(p.id, writerAgent);
      drafts = { ...drafts, [p.id]: text };
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busyDescribe = "";
    }
  }

  async function submit() {
    error = null;
    const name = npName.trim();
    if (!name) return;
    const id = name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
    try {
      const created = await createProject({
        id,
        name,
        description: npDescription.trim(),
        stack: npStack.split(",").map((s) => s.trim()).filter(Boolean),
        notes: npNotes.trim(),
      });
      const connectAfterCreate = npConnectGitHub;
      creating = false;
      npName = npDescription = npStack = npNotes = "";
      npConnectGitHub = false;
      await load();
      if (connectAfterCreate) await openGitHub(created);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function openGitHub(p: Project) {
    ghMode = "connect";
    ghModalOpen = true;
    ghOpen = p;
    ghSelected = p.repo?.full_name ?? "";
    ghDevice = null;
    ghReplacePending = false;
    ghInstallOpened = false;
    ghJustConnected = false;
    ghAnalyzing = false;
    ghAnalyzeWarning = "";
    ghCreated = null;
    clearGitHubPolling();
    error = null;
    await refreshGitHub();
  }

  async function openGitHubCreate() {
    ghMode = "create";
    ghModalOpen = true;
    ghOpen = null;
    ghSelected = "";
    ghDevice = null;
    ghReplacePending = false;
    ghInstallOpened = false;
    ghJustConnected = false;
    ghAnalyzing = false;
    ghAnalyzeWarning = "";
    ghCreated = null;
    clearGitHubPolling();
    error = null;
    await refreshGitHub();
  }

  function closeGitHubModal() {
    ghModalOpen = false;
    ghOpen = null;
    ghDevice = null;
    ghReplacePending = false;
    ghAnalyzing = false;
    clearGitHubPolling();
  }

  async function refreshGitHub() {
    ghBusy = "status";
    try {
      ghStatus = await githubStatus();
      if (ghStatus.authed) {
        ghRepos = await githubRepos();
        if (!ghSelected && ghRepos.length) ghSelected = ghRepos[0].full_name;
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      ghBusy = "";
    }
  }

  async function startGitHubDevice() {
    ghBusy = "device";
    error = null;
    try {
      ghDevice = await githubDeviceStart();
      ghAuthWindow = window.open(ghDevice.verification_uri, "podiom-github-auth", "popup,width=760,height=860");
      scheduleGitHubPoll(1200);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      ghBusy = "";
    }
  }

  function clearGitHubPolling() {
    if (ghPollTimer) window.clearTimeout(ghPollTimer);
    ghPollTimer = undefined;
  }

  function scheduleGitHubPoll(ms?: number) {
    clearGitHubPolling();
    if (!ghDevice) return;
    ghPollTimer = window.setTimeout(() => void pollGitHubDevice(false), ms ?? Math.max(1500, (ghDevice.interval || 5) * 1000));
  }

  async function pollGitHubDevice(showPending = true) {
    if (!ghDevice) return;
    ghBusy = "poll";
    if (showPending) error = null;
    try {
      const res = await githubDevicePoll(ghDevice.device_code);
      if (res.status === "authorized") {
        clearGitHubPolling();
        ghDevice = null;
        try {
          ghAuthWindow?.close();
          window.focus();
        } catch {
          // Some browsers ignore cross-tab focus/close requests.
        }
        ghAuthWindow = null;
        await refreshGitHub();
      } else if (res.status === "authorization_pending" || res.status === "slow_down") {
        if (showPending && res.status !== "authorization_pending") {
          error = res.error || res.status;
        }
        scheduleGitHubPoll(res.status === "slow_down" ? 6500 : undefined);
      } else {
        clearGitHubPolling();
        error = res.error || res.status;
      }
    } catch (e) {
      if (showPending) {
        error = e instanceof Error ? e.message : String(e);
      } else {
        scheduleGitHubPoll(3000);
      }
    } finally {
      ghBusy = "";
    }
  }

  function selectedRepo(): GitHubRepo | undefined {
    return ghRepos.find((r) => r.full_name === ghSelected);
  }

  function openGitHubInstall() {
    if (!ghStatus?.install_url) return;
    ghInstallOpened = true;
    window.open(ghStatus.install_url, "_blank", "noopener,noreferrer");
  }

  async function connectSelectedRepo(force = false) {
    if (!ghOpen) return;
    const repo = selectedRepo();
    if (!repo) return;
    ghBusy = "connect";
    error = null;
    try {
      const updated = await connectProjectRepo(ghOpen.id, {
        owner: repo.owner,
        name: repo.name,
        full_name: repo.full_name,
        html_url: repo.html_url,
        default_branch: repo.default_branch,
        ref: repo.default_branch,
        force,
      });
      projects = projects.map((x) => (x.id === updated.id ? updated : x));
      ghOpen = updated;
      ghReplacePending = false;
      ghJustConnected = true;
    } catch (e) {
      if (e instanceof Error && e.message === "CONFIRM_REPLACE") {
        ghReplacePending = true;
      } else {
        error = e instanceof Error ? e.message : String(e);
      }
    } finally {
      ghBusy = "";
    }
  }

  async function createFromSelectedRepo(force = false) {
    const repo = selectedRepo();
    if (!repo) return;
    ghBusy = "connect";
    error = null;
    ghAnalyzeWarning = "";
    try {
      const created = await createProjectFromGitHub({
        owner: repo.owner,
        name: repo.name,
        full_name: repo.full_name,
        html_url: repo.html_url,
        default_branch: repo.default_branch,
        ref: repo.default_branch,
        description: repo.description,
        force,
      });
      ghCreated = created;
      ghReplacePending = false;
      ghJustConnected = true;
      ghBusy = "";
      await load();
      if (agents.length === 0) {
        ghAnalyzeWarning = "Project created, but automatic analysis needs an agent. Edit details on the project card.";
        return;
      }
      ghAnalyzing = true;
      try {
        ghCreated = await analyzeProject(created.id);
      } catch {
        ghAnalyzeWarning = "Project created, but automatic analysis failed. Edit details on the project card.";
      } finally {
        ghAnalyzing = false;
        await load();
      }
    } catch (e) {
      if (e instanceof Error && e.message === "CONFIRM_REPLACE") {
        ghReplacePending = true;
      } else {
        error = e instanceof Error ? e.message : String(e);
      }
    } finally {
      ghBusy = "";
    }
  }

  async function syncRepo(p: Project, force = false) {
    ghBusy = "sync:" + p.id;
    error = null;
    try {
      const updated = await syncProjectRepo(p.id, force);
      projects = projects.map((x) => (x.id === p.id ? updated : x));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      ghBusy = "";
    }
  }

  async function disconnectRepo(p: Project) {
    ghBusy = "disconnect:" + p.id;
    error = null;
    try {
      const updated = await disconnectProjectRepo(p.id);
      projects = projects.map((x) => (x.id === p.id ? updated : x));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      ghBusy = "";
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    deleting = true;
    error = null;
    try {
      const id = deleteTarget.id;
      await deleteProject(id);
      projects = projects.filter((x) => x.id !== id);
      deleteTarget = null;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      deleting = false;
    }
  }
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1>Projects</h1>
      <p>Name your projects, give each a colour, and write a short description. Colours show up everywhere the project appears.</p>
    </div>
    <span class="spacer"></span>
    <button class="head-cta secondary" onclick={openGitHubCreate}>+ From GitHub</button>
    <button class="head-cta" onclick={() => (creating = true)}>+ New project</button>
  </header>

  {#if error}<div class="error-banner" style="margin-bottom:14px">{error}</div>{/if}

  <div class="proj-grid">
    {#each projects as p (p.id)}
      <article class="proj-card">
        <div class="pc-head">
          <span class="pc-bigdot" style="background:{color(p)}"></span>
          <span class="pc-name">{p.name}</span>
        </div>
        <div class="pc-id mono">{p.id}</div>

        <div class="label-mono" style="margin:16px 0 8px">colour</div>
        <div class="pc-swatches">
          {#each PROJECT_COLORS as c}
            <button
              class="swatch"
              style="background:{c};box-shadow:{c === color(p) ? '0 0 0 2px #FFFDFB,0 0 0 4px ' + c : 'inset 0 0 0 1px rgba(0,0,0,.06)'}"
              aria-label="Set colour"
              onclick={() => setColor(p, c)}
            ></button>
          {/each}
        </div>

        <div class="pc-desc-head">
          <span class="label-mono" style="flex:1">description</span>
          <div class="ai-combo">
            <button class="ai-btn" disabled={busyDescribe === p.id} onclick={() => describe(p)}>
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l1.9 4.6L18.5 9.5 13.9 11.4 12 16l-1.9-4.6L5.5 9.5l4.6-1.9z" /></svg>
              {busyDescribe === p.id ? "Writing…" : writerAgent ? "Help me write using" : "Help me write"}
            </button>
            {#if agents.length > 1}
              <select class="ai-writer-select" bind:value={writerAgent} disabled={busyDescribe === p.id} aria-label="Choose writer agent">
                {#each agents as a}<option value={a.Name}>{a.Name}</option>{/each}
              </select>
            {:else if writerAgent}
              <span class="ai-writer-name">{writerAgent}</span>
            {/if}
          </div>
        </div>
        <textarea
          class="field-area"
          rows="3"
          value={drafts[p.id] ?? ""}
          oninput={(e) => (drafts = { ...drafts, [p.id]: (e.currentTarget as HTMLTextAreaElement).value })}
          placeholder="What is this project? One or two sentences."
          style="min-height:74px"
        ></textarea>
        {#if dirty(p)}
          <div class="pc-save-row">
            <button class="pc-cancel" onclick={() => (drafts = { ...drafts, [p.id]: p.description })}>Reset</button>
            <button class="pc-save" disabled={savingDesc === p.id} onclick={() => saveDesc(p)}>{savingDesc === p.id ? "Saving…" : "Save description"}</button>
          </div>
        {/if}

        <div class="pc-desc-head" style="margin-top:14px">
          <span class="label-mono" style="flex:1">instructions</span>
          {#if instructionPaths[p.id]}<span class="repo-meta mono instr-path">{instructionPaths[p.id]}</span>{/if}
        </div>
        <textarea
          class="field-area mono"
          rows="5"
          value={instructionDrafts[p.id] ?? ""}
          oninput={(e) => (instructionDrafts = { ...instructionDrafts, [p.id]: (e.currentTarget as HTMLTextAreaElement).value })}
          placeholder="Standing project-specific instructions for agents."
          style="min-height:110px"
        ></textarea>
        {#if instructionsDirty(p)}
          <div class="pc-save-row">
            <button class="pc-cancel" onclick={() => (instructionDrafts = { ...instructionDrafts, [p.id]: instructionDraftsSaved[p.id] ?? "" })}>Reset</button>
            <button class="pc-save" disabled={savingInstructions === p.id} onclick={() => saveInstructions(p)}>{savingInstructions === p.id ? "Saving…" : "Save instructions"}</button>
          </div>
        {/if}

        {#if p.stack && p.stack.length}
          <div class="pc-chips">
            {#each p.stack as tech}<span class="pc-tech mono">{tech}</span>{/each}
          </div>
        {/if}

        <div class="pc-repo">
          <div>
            <div class="label-mono">github repo</div>
            {#if p.repo}
              <div class="repo-name">{p.repo.full_name}</div>
              <div class="repo-meta mono">{p.repo.ref || p.repo.default_branch} · synced {p.repo.synced_at || "never"}</div>
            {:else}
              <div class="repo-meta mono">not connected</div>
            {/if}
          </div>
          <div class="repo-actions">
            {#if p.repo}
              <button class="mini-action" disabled={ghBusy === "sync:" + p.id} onclick={() => syncRepo(p)}>{ghBusy === "sync:" + p.id ? "Syncing…" : "Sync"}</button>
              <button class="mini-action danger" disabled={ghBusy === "disconnect:" + p.id} onclick={() => disconnectRepo(p)}>Disconnect</button>
            {:else}
              <button class="mini-action" onclick={() => openGitHub(p)}>Connect</button>
            {/if}
          </div>
        </div>

        <div class="pc-foot">
          <span class="pc-meta mono">{meta(p)}</span>
          <button class="pc-delete" onclick={() => (deleteTarget = p)}>Delete</button>
          <button class="pc-view" onclick={() => onOpenChat({})}>View sessions →</button>
        </div>
      </article>
    {/each}
    {#if projects.length === 0}
      <p class="empty-note">No projects yet. Create one, or let an agent add it to the ledger.</p>
    {/if}
  </div>
</div>

{#if deleteTarget}
  <ConfirmModal
    title="Delete project?"
    message={deleteMessage}
    confirmLabel="Delete project"
    busy={deleting}
    onConfirm={confirmDelete}
    onCancel={() => (deleteTarget = null)}
  />
{/if}

{#if creating}
  <div class="modal-backdrop" role="presentation" onclick={() => (creating = false)}>
    <div class="modal-card np-modal" role="dialog" aria-modal="true" aria-label="New project" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <div class="modal-head">
        <div class="modal-title">New project</div>
        <div class="modal-sub">Tracked in <span class="mono">~/.podiom/projects/projects.yaml</span>. Any agent can pick it up.</div>
      </div>
      <div class="modal-body">
        <div class="label-mono" style="margin-bottom:8px">name</div>
        <input class="field-input" bind:value={npName} placeholder="Mission Control" />

        <div class="label-mono" style="margin:18px 0 8px">description</div>
        <textarea class="field-area" rows="2" bind:value={npDescription} placeholder="What is this project? One or two sentences." style="min-height:60px"></textarea>

        <div class="label-mono" style="margin:18px 0 8px">stack (comma-separated)</div>
        <input class="field-input" bind:value={npStack} placeholder="Next.js, TypeScript, Tailwind" />

        <div class="label-mono" style="margin:18px 0 8px">notes</div>
        <textarea class="field-area" rows="2" bind:value={npNotes} placeholder="Anything agents should know." style="min-height:56px"></textarea>

        <label class="repo-check">
          <input type="checkbox" bind:checked={npConnectGitHub} />
          <span>Connect GitHub after creating</span>
        </label>

        <button class="modal-cta" disabled={!npName.trim()} onclick={submit}>Create project</button>
      </div>
    </div>
  </div>
{/if}

{#if ghModalOpen}
  <div class="modal-backdrop" role="presentation" onclick={closeGitHubModal}>
    <div class="modal-card np-modal" role="dialog" aria-modal="true" aria-label={ghMode === "create" ? "New project from GitHub" : "Connect GitHub"} tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <div class="modal-head">
        <div class="modal-title">{ghMode === "create" ? "New project from GitHub" : "Connect GitHub"}</div>
        <div class="modal-sub">
          {#if ghMode === "create"}
            Pick a repository. Podiom will create the project, sync the snapshot, then fill in the details.
          {:else if ghOpen}
            Project source will be downloaded into <span class="mono">~/.podiom/projects/{ghOpen.path}/repo/</span>.
          {/if}
        </div>
      </div>
      <div class="modal-body">
        <div class="connect-steps">
          <div class="step-row" class:done={ghStatus?.authed}>
            <span class="step-dot">{ghStatus?.authed ? "✓" : "1"}</span>
            <span>Authorize Podiom with your GitHub account</span>
          </div>
          <div class="step-row" class:done={ghRepos.length > 0}>
            <span class="step-dot">{ghRepos.length > 0 ? "✓" : "2"}</span>
            <span>Choose which repositories Podiom may read</span>
          </div>
          <div class="step-row" class:done={ghMode === "create" ? !!ghCreated : !!ghOpen?.repo}>
            <span class="step-dot">{(ghMode === "create" ? !!ghCreated : !!ghOpen?.repo) ? "✓" : "3"}</span>
            <span>{ghMode === "create" ? "Create a project from one repository" : "Connect one repository to this project"}</span>
          </div>
        </div>

        {#if ghStatus && !ghStatus.configured}
          <div class="error-banner">{ghStatus.message}</div>
        {:else if ghStatus && !ghStatus.authed}
          <div class="label-mono" style="margin-bottom:8px">authorize account</div>
          <p class="repo-help">GitHub will ask you to authorize the Podiom app. Repository access is chosen in the next step.</p>
          {#if ghDevice}
            <div class="device-code mono">{ghDevice.user_code}</div>
            <p class="repo-help">Enter this code on GitHub. Podiom will bring you back here when authorization completes.</p>
            <button class="modal-cta" disabled={ghBusy === "poll"} onclick={() => pollGitHubDevice(true)}>{ghBusy === "poll" ? "Checking…" : "Check now"}</button>
          {:else}
            <button class="modal-cta" disabled={ghBusy === "device"} onclick={startGitHubDevice}>{ghBusy === "device" ? "Opening…" : "Authorize GitHub"}</button>
          {/if}
        {:else}
          {#if ghMode === "create" && ghCreated}
            {#if ghAnalyzing}
              <div class="analysis-panel">
                <div class="analysis-spinner" aria-hidden="true"></div>
                <div>
                  <div class="analysis-title">Analyzing repository…</div>
                  <p class="repo-help">Reading README files and top-level manifests to fill in name, description, stack, and notes.</p>
                </div>
              </div>
            {:else}
              <div class="success-panel">
                <div class="confetti" aria-hidden="true">
                  {#each Array.from({ length: 34 }) as _, i}
                    <span style={`--i:${i};--dx:${((i % 11) - 5) * 20}px;--dy:${-(92 + (i % 7) * 19)}px;--rot:${160 + i * 37}deg`}></span>
                  {/each}
                </div>
                <div class="success-mark">✓</div>
                <div class="success-title">{ghCreated.name}</div>
                {#if ghCreated.description}
                  <p class="repo-help">{ghCreated.description}</p>
                {/if}
                {#if ghCreated.stack && ghCreated.stack.length}
                  <div class="created-stack">
                    {#each ghCreated.stack as tech}<span class="pc-tech mono">{tech}</span>{/each}
                  </div>
                {/if}
                {#if ghAnalyzeWarning}
                  <div class="soft-notice">{ghAnalyzeWarning}</div>
                {/if}
                <button class="modal-cta" onclick={closeGitHubModal}>Done</button>
              </div>
            {/if}
          {:else if ghMode === "connect" && ghJustConnected && ghOpen?.repo}
            <div class="success-panel">
              <div class="confetti" aria-hidden="true">
                {#each Array.from({ length: 34 }) as _, i}
                  <span style={`--i:${i};--dx:${((i % 11) - 5) * 20}px;--dy:${-(92 + (i % 7) * 19)}px;--rot:${160 + i * 37}deg`}></span>
                {/each}
              </div>
              <div class="success-mark">✓</div>
              <div class="success-title">Repository connected</div>
              <p class="repo-help">
                {ghOpen?.repo?.full_name} is synced into <span class="mono">~/.podiom/projects/{ghOpen?.path}/repo/</span>.
              </p>
              <button class="modal-cta" onclick={closeGitHubModal}>Done</button>
            </div>
          {:else if ghRepos.length === 0}
            <div class="label-mono" style="margin-bottom:8px">choose repositories</div>
            <p class="repo-help">GitHub may open an existing installation settings page. Select repository access there, save, then return here.</p>
            <button class="modal-cta" disabled={!ghStatus?.install_url} onclick={openGitHubInstall}>{ghInstallOpened ? "Manage repository access on GitHub" : "Choose repositories on GitHub"}</button>
          {:else}
            <div class="label-mono" style="margin-bottom:8px">repository</div>
            <select class="field-input" bind:value={ghSelected}>
              {#each ghRepos as r}
                <option value={r.full_name}>{r.full_name}{r.private ? " · private" : ""}</option>
              {/each}
            </select>
            <div class="repo-row-note">
              <button class="link-btn" onclick={openGitHubInstall}>Change repository access</button>
              <button class="link-btn" disabled={ghBusy === "status"} onclick={refreshGitHub}>Refresh list</button>
            </div>
            {#if ghReplacePending}
              <div class="error-banner" style="margin-top:12px">This project repo folder already has files. Podiom will back them up before syncing the snapshot.</div>
              <button class="modal-cta" disabled={ghBusy === "connect"} onclick={() => (ghMode === "create" ? createFromSelectedRepo(true) : connectSelectedRepo(true))}>
                {ghBusy === "connect" ? (ghMode === "create" ? "Creating…" : "Connecting…") : ghMode === "create" ? "Back up and create" : "Back up and connect"}
              </button>
            {:else}
              <button class="modal-cta" disabled={!ghSelected || ghBusy === "connect"} onclick={() => (ghMode === "create" ? createFromSelectedRepo(false) : connectSelectedRepo(false))}>
                {ghBusy === "connect" ? (ghMode === "create" ? "Creating…" : "Connecting…") : ghMode === "create" ? "Create project from repo" : "Connect repo"}
              </button>
            {/if}
          {/if}
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .head-cta.secondary {
    background: #fff;
    color: var(--teal-deep);
    border: 1px solid var(--field-line);
    box-shadow: none;
  }

  .proj-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 380px), 1fr));
    gap: 18px;
    max-width: 1180px;
  }

  .proj-card {
    background: var(--surface);
    border: 1px solid var(--line-2);
    border-radius: 20px;
    padding: 20px;
    box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04), 0 16px 40px -28px rgba(43, 37, 32, 0.22);
  }

  .pc-head {
    display: flex;
    align-items: center;
    gap: 11px;
  }

  .pc-bigdot {
    width: 14px;
    height: 14px;
    border-radius: 99px;
    flex: none;
  }

  .pc-name {
    font: 800 19px "Hanken Grotesk";
    color: var(--ink);
  }

  .pc-id {
    font: 500 11px "JetBrains Mono", monospace;
    color: var(--faint);
    margin: 2px 0 0 25px;
  }

  .pc-swatches {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  .swatch {
    width: 24px;
    height: 24px;
    border-radius: 8px;
    cursor: pointer;
    border: none;
  }

  .pc-desc-head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 16px 0 8px;
  }

  .ai-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 5px;
    border: none;
    background: transparent;
    color: #6b53a8;
    border-radius: 0;
    padding: 5px 8px 5px 10px;
    font: 600 11.5px "Hanken Grotesk";
    cursor: pointer;
    white-space: nowrap;
  }

  .ai-btn:disabled {
    opacity: 0.65;
    cursor: wait;
  }

  .ai-combo {
    display: inline-flex;
    align-items: stretch;
    max-width: 100%;
    border: 1px solid #e2d7e9;
    background: #f4eff8;
    border-radius: 9px;
    overflow: hidden;
    flex: none;
  }

  .ai-writer-select,
  .ai-writer-name {
    border: none;
    border-left: 1px solid #e2d7e9;
    background: #fff;
    color: #6b53a8;
    font: 600 11.5px "Hanken Grotesk";
  }

  .ai-writer-select {
    min-width: 0;
    max-width: 128px;
    padding: 0 24px 0 8px;
    outline: none;
    cursor: pointer;
  }

  .ai-writer-select:disabled {
    cursor: wait;
  }

  .ai-writer-name {
    display: inline-flex;
    align-items: center;
    max-width: 128px;
    padding: 0 10px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .pc-save-row {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 9px;
  }

  .pc-cancel {
    border: 1px solid var(--field-line);
    background: #fff;
    border-radius: 9px;
    padding: 6px 12px;
    font: 600 12px "Hanken Grotesk";
    color: var(--muted-2);
    cursor: pointer;
  }

  .pc-save {
    border: none;
    background: var(--teal);
    color: #fff;
    border-radius: 9px;
    padding: 6px 14px;
    font: 600 12px "Hanken Grotesk";
    cursor: pointer;
    box-shadow: 0 6px 14px -6px rgba(63, 143, 126, 0.7);
  }

  .pc-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 12px;
  }

  .pc-tech {
    padding: 3px 9px;
    border-radius: 999px;
    background: var(--surface-3);
    border: 1px solid var(--line-3);
    font: 500 11px "JetBrains Mono", monospace;
    color: #8a7560;
  }

  .pc-repo {
    display: flex;
    gap: 12px;
    align-items: center;
    justify-content: space-between;
    margin-top: 14px;
    padding: 12px;
    border: 1px solid #eee4d8;
    border-radius: 10px;
    background: #fffaf4;
  }

  .repo-name {
    margin-top: 5px;
    font: 700 14px "Hanken Grotesk";
    color: var(--ink);
    overflow-wrap: anywhere;
  }

  .repo-meta,
  .repo-help {
    margin: 4px 0 0;
    font: 500 11.5px "JetBrains Mono", monospace;
    color: #9a8e80;
  }

  .instr-path {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .repo-actions {
    display: flex;
    gap: 7px;
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .mini-action {
    border: 1px solid var(--field-line);
    background: #fff;
    border-radius: 9px;
    padding: 6px 10px;
    font: 700 11.5px "Hanken Grotesk";
    color: var(--teal-deep);
    cursor: pointer;
  }

  .mini-action.danger {
    color: #a05252;
  }

  .device-code {
    display: inline-flex;
    margin: 10px 0 12px;
    padding: 10px 13px;
    border: 1px solid #d6cbe3;
    border-radius: 10px;
    background: #f8f3fc;
    color: #6b53a8;
    font: 800 18px "JetBrains Mono", monospace;
    letter-spacing: 0;
  }

  .repo-check {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 16px 0 2px;
    font: 600 13px "Hanken Grotesk";
    color: var(--muted-2);
  }

  .connect-steps {
    display: grid;
    gap: 8px;
    margin-bottom: 18px;
    padding: 12px;
    border: 1px solid #eee4d8;
    border-radius: 10px;
    background: #fffaf4;
  }

  .step-row {
    display: flex;
    align-items: center;
    gap: 9px;
    font: 650 12.5px "Hanken Grotesk";
    color: #7d7166;
  }

  .step-row.done {
    color: var(--teal-deep);
  }

  .step-dot {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    border-radius: 999px;
    background: #f1e8dc;
    color: #8a7560;
    font: 800 11px "Hanken Grotesk";
    flex: none;
  }

  .step-row.done .step-dot {
    background: #e4f2eb;
    color: var(--teal-deep);
  }

  .repo-row-note {
    display: flex;
    gap: 10px;
    flex-wrap: wrap;
    margin: 9px 0 13px;
  }

  .link-btn {
    border: none;
    background: transparent;
    padding: 0;
    color: var(--teal-deep);
    font: 700 12px "Hanken Grotesk";
    cursor: pointer;
    text-decoration: underline;
    text-underline-offset: 3px;
  }

  .analysis-panel {
    display: flex;
    align-items: center;
    gap: 14px;
    margin-top: 4px;
    padding: 15px;
    border: 1px solid #d9ebe5;
    border-radius: 10px;
    background: #f6fbf8;
  }

  .analysis-spinner {
    width: 28px;
    height: 28px;
    border-radius: 999px;
    border: 3px solid #d9ebe5;
    border-top-color: var(--teal);
    animation: analysis-spin 900ms linear infinite;
    flex: none;
  }

  .analysis-title {
    font: 800 15px "Hanken Grotesk";
    color: var(--teal-deep);
  }

  @keyframes analysis-spin {
    to {
      transform: rotate(360deg);
    }
  }

  .created-stack {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 6px;
    margin: 12px 0 4px;
  }

  .soft-notice {
    margin: 13px 0 2px;
    padding: 10px 12px;
    border: 1px solid #eee4d8;
    border-radius: 10px;
    background: #fffaf4;
    color: #7d7166;
    font: 650 12.5px "Hanken Grotesk";
    text-align: left;
  }

  .link-btn:disabled {
    color: var(--faint);
    cursor: default;
  }

  .success-panel {
    position: relative;
    overflow: visible;
    text-align: center;
    padding: 22px 8px 4px;
  }

  .success-mark {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 46px;
    height: 46px;
    border-radius: 999px;
    background: #e4f2eb;
    color: var(--teal-deep);
    font: 900 24px "Hanken Grotesk";
    box-shadow: 0 10px 24px -16px rgba(63, 143, 126, 0.8);
  }

  .success-title {
    margin-top: 12px;
    font: 800 20px "Hanken Grotesk";
    color: var(--ink);
  }

  .confetti {
    position: absolute;
    left: 50%;
    bottom: 20px;
    width: 1px;
    height: 1px;
    z-index: 2;
    pointer-events: none;
  }

  .confetti span {
    position: absolute;
    left: 0;
    bottom: 0;
    width: 7px;
    height: 13px;
    border-radius: 2px;
    background: hsl(calc(24 + var(--i) * 29), 72%, 58%);
    box-shadow: 0 1px 2px rgba(43, 37, 32, .14);
    transform: translate(-50%, 0) scale(.6) rotate(0deg);
    animation: confetti-pop 1250ms cubic-bezier(.13, .92, .2, 1) both;
    animation-delay: calc(var(--i) * 13ms);
  }

  .confetti span:nth-child(3n) {
    width: 9px;
    height: 9px;
    border-radius: 999px;
  }

  .confetti span:nth-child(4n) {
    width: 12px;
    height: 5px;
  }

  @keyframes confetti-pop {
    0% {
      opacity: 0;
      transform: translate(-50%, 0) scale(.5) rotate(0deg);
    }
    12% {
      opacity: 1;
      transform: translate(calc(var(--dx) * .32), calc(var(--dy) * .45)) scale(1) rotate(calc(var(--rot) * .35));
    }
    100% {
      opacity: 0;
      transform: translate(var(--dx), calc(var(--dy) + 72px)) scale(.9) rotate(var(--rot));
    }
  }

  .pc-foot {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 14px;
    padding-top: 13px;
    border-top: 1px solid #f1eae0;
  }

  .pc-meta {
    font: 500 11.5px "JetBrains Mono", monospace;
    color: #9a8e80;
    flex: 1;
  }

  .pc-view {
    border: 1px solid var(--field-line);
    background: #fff;
    border-radius: 9px;
    padding: 6px 11px;
    font: 600 11.5px "Hanken Grotesk";
    color: var(--teal-deep);
    cursor: pointer;
  }

  .pc-delete {
    border: 1px solid var(--field-line);
    background: #fff;
    border-radius: 9px;
    padding: 6px 11px;
    font: 600 11.5px "Hanken Grotesk";
    color: #b4472f;
    cursor: pointer;
  }

  .pc-delete:hover {
    border-color: #d9663d;
    background: #fdf2ee;
  }

  .np-modal {
    width: 460px;
    max-width: 92vw;
  }

  @media (max-width: 768px) {
    .proj-card {
      padding: 16px;
    }

    .pc-head {
      align-items: flex-start;
    }

    .pc-name {
      min-width: 0;
      overflow-wrap: anywhere;
    }

    .pc-id {
      margin-left: 25px;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .pc-desc-head,
    .pc-repo,
    .pc-foot,
    .pc-save-row {
      align-items: stretch;
      flex-direction: column;
    }

    .ai-combo,
    .pc-view,
    .pc-save,
    .pc-cancel,
    .mini-action {
      justify-content: center;
      width: 100%;
    }

    .ai-btn {
      flex: 1;
      min-width: 0;
    }

    .ai-writer-select,
    .ai-writer-name {
      max-width: 45%;
    }

    .repo-actions {
      justify-content: stretch;
    }
  }
</style>
