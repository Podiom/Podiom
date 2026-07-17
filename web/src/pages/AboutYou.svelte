<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { deleteSession, getUserProfile, listProfiles, saveUserProfile } from "../lib/api";
  import { live } from "../lib/live.svelte";
  import { renderMarkdown } from "../lib/markdown";
  import RunTargetPicker from "../lib/RunTargetPicker.svelte";
  import type { RunTargetValue } from "../lib/RunTargetPicker.svelte";
  import type {
    Agent,
    ProfileInfo,
    ServerMessage,
    UserInputQuestion,
    UserInputRequest,
  } from "../lib/types";

  let {
    agents,
    onSaved = () => {},
  }: {
    agents: Agent[];
    onSaved?: () => void;
  } = $props();

  const PROFILE_HEADING = "# About the user";
  const MAX_PROGRESS_DOTS = 8;

  type Phase = "loading" | "view" | "intro" | "interviewing" | "review";

  let phase = $state<Phase>("loading");
  let profile = $state("");
  let error = $state<string | null>(null);

  // Intro: who conducts the interview.
  let profiles = $state<ProfileInfo[]>([]);
  let agentName = $state("");
  let runTarget = $state<RunTargetValue>({});
  const selectedAgent = $derived(agents.find((a) => a.Name === agentName) ?? null);

  // Interview session state.
  let requestId = "";
  let sessionId = $state("");
  let sessionProvider = $state("");
  let pendingInput = $state<UserInputRequest | null>(null);
  let answers = $state<Record<string, string[]>>({});
  let answered = $state(0);
  let thinking = $state(false);
  let stalled = $state(false);
  let assistantBuf = "";
  let lastReply = $state("");
  let deleteAfterTurn = false;

  // Review / edit.
  let draft = $state("");
  let showPreview = $state(false);
  let saving = $state(false);
  let editing = $state(false);
  let editDraft = $state("");

  let unsubscribe: (() => void) | null = null;
  let wasOffline = false;

  onMount(async () => {
    unsubscribe = live.subscribe(handleMessage);
    try {
      const [info, profs] = await Promise.all([getUserProfile(), listProfiles()]);
      profile = info.profile;
      profiles = profs ?? [];
      phase = info.exists ? "view" : "intro";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      phase = "intro";
    }
    if (!agentName && agents.length > 0) agentName = agents[0].Name;
  });

  onDestroy(() => {
    unsubscribe?.();
  });

  // Restore a pending question after a reconnect mid-interview.
  $effect(() => {
    if (live.status !== "live") {
      wasOffline = true;
      return;
    }
    if (wasOffline && sessionId && phase === "interviewing") {
      live.send({ type: "attach_session", session_id: sessionId });
    }
    wasOffline = false;
  });

  function startInterview() {
    if (!agentName) return;
    error = null;
    stalled = false;
    pendingInput = null;
    answered = 0;
    assistantBuf = "";
    lastReply = "";
    sessionId = "";
    thinking = true;
    requestId = crypto.randomUUID();
    phase = "interviewing";
    live.send({
      type: "start_interview",
      request_id: requestId,
      agent_name: agentName,
      provider: runTarget.provider || undefined,
      profile: runTarget.profile || undefined,
      model: runTarget.model || undefined,
      effort: runTarget.effort || undefined,
    });
  }

  function handleMessage(msg: ServerMessage) {
    if (phase !== "interviewing") return;
    if (msg.type === "session" && msg.request_id === requestId && msg.session && !sessionId) {
      sessionId = msg.session.ID;
      sessionProvider = msg.session.Provider;
      return;
    }
    if (!sessionId || msg.session_id !== sessionId) return;
    switch (msg.type) {
      case "delta":
        thinking = true;
        assistantBuf += msg.delta ?? "";
        break;
      case "assistant":
        if (msg.delta) assistantBuf = msg.delta;
        break;
      case "user_input_request":
        pendingInput = msg.input ?? null;
        answers = initialAnswers(pendingInput);
        thinking = false;
        stalled = false;
        break;
      case "permission_request":
        // The interview needs no tools; keep the agent moving without user noise.
        if (msg.request) {
          live.send({
            type: "permission_decision",
            request_id: msg.request.id,
            decision: {
              behavior: "deny",
              message:
                "This interview doesn't use tools — continue asking questions with your question tool, then output the final USER.md.",
            },
          });
        }
        break;
      case "turn_state":
        if (msg.turn_state?.pending_user_input) {
          pendingInput = msg.turn_state.pending_user_input;
          answers = initialAnswers(pendingInput);
          thinking = false;
        }
        break;
      case "done":
        onTurnDone();
        break;
      case "error":
        thinking = false;
        if (deleteAfterTurn) {
          void discardSession();
          break;
        }
        error = msg.error ?? "The interview failed.";
        stalled = true;
        break;
    }
  }

  function onTurnDone() {
    thinking = false;
    if (deleteAfterTurn) {
      void discardSession();
      return;
    }
    const text = assistantBuf.trim();
    if (text) lastReply = text;
    assistantBuf = "";
    if (text.includes(PROFILE_HEADING)) {
      draft = cleanDraft(text);
      showPreview = false;
      phase = "review";
      return;
    }
    // Claude's question tool ends the turn; the pending question card keeps the
    // interview alive. Anything else means the agent stalled.
    if (!pendingInput) stalled = true;
  }

  // cleanDraft strips code fences and any preamble before the profile heading
  // (the server cleans again on save).
  function cleanDraft(raw: string): string {
    let s = raw.trim();
    const idx = s.indexOf(PROFILE_HEADING);
    if (idx > 0) s = s.slice(idx);
    return s.replaceAll(/^```[a-z]*\n|\n```$/g, "").trim();
  }

  function initialAnswers(req: UserInputRequest | null): Record<string, string[]> {
    const out: Record<string, string[]> = {};
    for (const q of req?.questions ?? []) out[q.id] = [];
    return out;
  }

  function toggleAnswer(q: UserInputQuestion, value: string) {
    const current = answers[q.id] ?? [];
    if (q.multi_select) {
      answers = {
        ...answers,
        [q.id]: current.includes(value) ? current.filter((v) => v !== value) : [...current, value],
      };
      return;
    }
    answers = { ...answers, [q.id]: [value] };
  }

  function setFreeAnswer(q: UserInputQuestion, value: string) {
    answers = { ...answers, [q.id]: value.trim() ? [value] : [] };
  }

  function answerSelected(q: UserInputQuestion, value: string) {
    return (answers[q.id] ?? []).includes(value);
  }

  const answersReady = $derived(
    !!pendingInput && pendingInput.questions.every((q) => (answers[q.id] ?? []).some((v) => v.trim())),
  );

  function submitAnswers() {
    const req = pendingInput;
    if (!req || !answersReady) return;
    live.send({ type: "user_input_decision", request_id: req.id, input: { answers } });
    answered += 1;
    pendingInput = null;
    assistantBuf = "";
    thinking = true;
    if ((req.provider ?? sessionProvider) === "claude") {
      // Claude's question ended the turn; the answer arrives as a follow-up turn.
      requestId = crypto.randomUUID();
      live.send({
        type: "send_turn",
        request_id: requestId,
        session_id: sessionId,
        message: formatFollowup(req, answers),
      });
    }
  }

  function formatFollowup(req: UserInputRequest, a: Record<string, string[]>): string {
    if (req.questions.length === 1) {
      const q = req.questions[0];
      return `Answer to "${q.question}": ${(a[q.id] ?? []).join(", ")}`;
    }
    return ["Answers:", ...req.questions.map((q) => `- ${q.question}: ${(a[q.id] ?? []).join(", ")}`)].join("\n");
  }

  function useLastReply() {
    if (!lastReply) return;
    draft = cleanDraft(lastReply);
    showPreview = false;
    phase = "review";
  }

  function cancelInterview() {
    if (sessionId) {
      if (thinking) {
        // A turn is running: stop it first, delete once it winds down.
        deleteAfterTurn = true;
        live.send({ type: "stop_turn", session_id: sessionId });
        window.setTimeout(() => {
          if (deleteAfterTurn) void discardSession();
        }, 2000);
      } else {
        void discardSession();
      }
    }
    resetInterview();
    phase = profile.trim() ? "view" : "intro";
  }

  async function discardSession() {
    const id = sessionId;
    deleteAfterTurn = false;
    sessionId = "";
    if (!id) return;
    try {
      await deleteSession(id);
    } catch {
      // The interview session is disposable; a failed cleanup is harmless.
    }
  }

  function resetInterview() {
    pendingInput = null;
    answers = {};
    answered = 0;
    thinking = false;
    stalled = false;
    assistantBuf = "";
    lastReply = "";
    error = null;
  }

  async function saveDraft() {
    if (!draft.trim()) return;
    saving = true;
    error = null;
    try {
      const info = await saveUserProfile(draft);
      profile = info.profile;
      await discardSession();
      resetInterview();
      phase = "view";
      onSaved();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  function discardDraft() {
    void discardSession();
    resetInterview();
    draft = "";
    phase = profile.trim() ? "view" : "intro";
  }

  function startEdit() {
    editDraft = profile;
    editing = true;
  }

  async function saveEdit() {
    if (!editDraft.trim()) return;
    saving = true;
    error = null;
    try {
      const info = await saveUserProfile(editDraft);
      profile = info.profile;
      editing = false;
      onSaved();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }
</script>

<section class="card">
  <div class="card-head">
    <div class="card-icon">
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="8" r="4"/><path d="M4 21v-1a7 7 0 0 1 14 0v1"/></svg>
    </div>
    <div>
      <div class="card-title">About you</div>
      <div class="card-sub">
        A short profile of you — USER.md — shared with every agent so they know who they're talking to.
      </div>
    </div>
  </div>

  {#if error && phase !== "interviewing"}
    <div class="err">{error}</div>
  {/if}

  {#if phase === "loading"}
    <div class="note">Loading…</div>
  {:else if phase === "intro"}
    <p class="why">
      When agents understand who you are — your role, how you like to be spoken to, how much detail you
      want — every conversation starts calibrated instead of generic. A short interview (5–8 quick
      questions, mostly clicking options) produces a profile you can review and edit before anything is
      saved. It lands in <code>USER.md</code> and is shared with all your agents.
    </p>
    <div class="field">
      <div class="field-label">Interviewer</div>
      <select class="agent-select" bind:value={agentName}>
        {#each agents as a}
          <option value={a.Name}>{a.Name}</option>
        {/each}
      </select>
    </div>
    <div class="field">
      <RunTargetPicker
        agent={selectedAgent}
        {profiles}
        value={runTarget}
        onChange={(v) => (runTarget = v)}
        variant="stacked" />
    </div>
    <div class="actions">
      <button class="primary" disabled={!agentName} onclick={startInterview}>Start the interview</button>
      {#if profile.trim()}
        <button class="ghost" onclick={() => (phase = "view")}>Back</button>
      {/if}
    </div>
  {:else if phase === "interviewing"}
    <div class="progress">
      {#each Array(MAX_PROGRESS_DOTS) as _, i}
        <span class="dot" class:on={i < Math.min(answered, MAX_PROGRESS_DOTS)}></span>
      {/each}
      <span class="progress-text">
        {answered === 0 ? "Warming up…" : `${answered} answered`}
      </span>
    </div>

    {#if pendingInput}
      {#each pendingInput.questions as q}
        <div class="question">
          {#if q.header}<div class="q-header">{q.header}</div>{/if}
          <div class="q-text">{q.question}</div>
          {#if q.options && q.options.length > 0}
            <div class="q-options">
              {#each q.options as option}
                <button class="q-option" class:sel={answerSelected(q, option.label)} onclick={() => toggleAnswer(q, option.label)}>
                  <span class="q-mark">{answerSelected(q, option.label) ? "✓" : ""}</span>
                  <span class="q-option-text">
                    <span>{option.label}</span>
                    {#if option.description}<small>{option.description}</small>{/if}
                  </span>
                </button>
              {/each}
            </div>
          {:else}
            <input
              class="q-free"
              type={q.is_secret ? "password" : "text"}
              placeholder="Answer"
              value={(answers[q.id] ?? [])[0] ?? ""}
              oninput={(e) => setFreeAnswer(q, e.currentTarget.value)} />
          {/if}
        </div>
      {/each}
      <div class="actions">
        <button class="primary" disabled={!answersReady} onclick={submitAnswers}>Send answer</button>
        <button class="ghost" onclick={cancelInterview}>Cancel</button>
      </div>
    {:else if stalled}
      <div class="err">
        {error ?? "The agent stopped without asking a question or finishing the profile."}
      </div>
      <div class="actions">
        {#if lastReply}
          <button class="primary" onclick={useLastReply}>Use last reply as draft</button>
        {/if}
        <button class="ghost" onclick={startInterview}>Retry</button>
        <button class="ghost" onclick={cancelInterview}>Cancel</button>
      </div>
    {:else}
      <div class="note thinking">
        <span class="pulse"></span>
        {answered === 0 ? "The interviewer is preparing the first question…" : "Thinking about the next question…"}
      </div>
      <div class="actions">
        <button class="ghost" onclick={cancelInterview}>Cancel</button>
      </div>
    {/if}
  {:else if phase === "review"}
    <p class="why">
      Here's the draft profile. Edit anything that feels off — this is exactly what every agent will read.
    </p>
    <div class="review-toggle">
      <button class:on={!showPreview} onclick={() => (showPreview = false)}>Edit</button>
      <button class:on={showPreview} onclick={() => (showPreview = true)}>Preview</button>
    </div>
    {#if showPreview}
      <div class="preview md">{@html renderMarkdown(draft)}</div>
    {:else}
      <textarea class="editor mono" rows="18" bind:value={draft}></textarea>
    {/if}
    <div class="actions">
      <button class="primary" disabled={saving || !draft.trim()} onclick={saveDraft}>
        {saving ? "Saving…" : "Save profile"}
      </button>
      <button class="ghost" onclick={startInterview}>Redo interview</button>
      <button class="ghost" onclick={discardDraft}>Discard</button>
    </div>
  {:else if phase === "view"}
    {#if editing}
      <textarea class="editor mono" rows="18" bind:value={editDraft}></textarea>
      <div class="actions">
        <button class="primary" disabled={saving || !editDraft.trim()} onclick={saveEdit}>
          {saving ? "Saving…" : "Save"}
        </button>
        <button class="ghost" onclick={() => (editing = false)}>Cancel</button>
      </div>
    {:else}
      <div class="preview md">{@html renderMarkdown(profile)}</div>
      <div class="actions">
        <button class="primary" onclick={startEdit}>Edit</button>
        <button class="ghost" onclick={() => (phase = "intro")}>Redo interview</button>
      </div>
    {/if}
  {/if}
</section>

<style>
  .card {
    border: 1px solid #eee3d4;
    border-radius: 18px;
    background: #fffdf9;
    padding: 22px;
    margin-bottom: 18px;
  }

  .card-head {
    display: flex;
    align-items: center;
    gap: 13px;
    margin-bottom: 16px;
  }

  .card-icon {
    width: 40px;
    height: 40px;
    flex: none;
    border-radius: 12px;
    background: #e8f4ef;
    color: #2f6e60;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .card-title {
    font: 800 15.5px "Hanken Grotesk";
    color: #2b2520;
  }

  .card-sub {
    font: 500 12.5px "Hanken Grotesk";
    color: #8a7f73;
  }

  .why {
    font: 500 13.5px/1.55 "Hanken Grotesk";
    color: #5c534a;
    margin: 0 0 16px;
  }

  .why code {
    font: 600 12px "JetBrains Mono", monospace;
    background: #f6efe6;
    border-radius: 5px;
    padding: 1px 5px;
  }

  .note {
    font: 500 13px "Hanken Grotesk";
    color: #8a7f73;
    padding: 10px 0;
  }

  .note.thinking {
    display: flex;
    align-items: center;
    gap: 9px;
  }

  .pulse {
    width: 9px;
    height: 9px;
    flex: none;
    border-radius: 999px;
    background: #2f6e60;
    animation: aboutPulse 1.2s ease-in-out infinite;
  }

  @keyframes aboutPulse {
    0%,
    100% {
      opacity: 0.35;
      transform: scale(0.85);
    }
    50% {
      opacity: 1;
      transform: none;
    }
  }

  .err {
    border: 1px solid #ecc9b8;
    background: #fbeee6;
    color: #9c4a21;
    border-radius: 11px;
    padding: 10px 13px;
    font: 600 12.5px "Hanken Grotesk";
    margin-bottom: 13px;
  }

  .field {
    margin-bottom: 14px;
  }

  .field-label {
    font: 700 10.5px "JetBrains Mono", monospace;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: #a89c8e;
    margin-bottom: 7px;
  }

  .agent-select {
    width: 100%;
    max-width: 320px;
    min-height: 44px;
    border: 1.5px solid #e9ded0;
    border-radius: 12px;
    background: #fffaf4;
    padding: 8px 11px;
    font: 700 13px "Hanken Grotesk";
    color: #4f473f;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 9px;
    margin-top: 15px;
  }

  .primary {
    border: none;
    border-radius: 11px;
    background: #2f6e60;
    color: #fff;
    font: 700 13px "Hanken Grotesk";
    padding: 10px 17px;
    cursor: pointer;
  }

  .primary:disabled {
    opacity: 0.45;
    cursor: default;
  }

  .ghost {
    border: 1px solid #e9ded0;
    border-radius: 11px;
    background: #fffaf4;
    color: #6b6157;
    font: 700 13px "Hanken Grotesk";
    padding: 10px 15px;
    cursor: pointer;
  }

  .progress {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 15px;
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 999px;
    background: #eadfce;
  }

  .dot.on {
    background: #2f6e60;
  }

  .progress-text {
    margin-left: 6px;
    font: 600 11.5px "JetBrains Mono", monospace;
    color: #a89c8e;
  }

  .question {
    border: 1px solid #eadfce;
    border-radius: 14px;
    background: #fffaf4;
    padding: 15px;
    margin-bottom: 11px;
  }

  .q-header {
    font: 700 10.5px "JetBrains Mono", monospace;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: #2a6452;
    margin-bottom: 5px;
  }

  .q-text {
    font: 700 14px "Hanken Grotesk";
    color: #2b2520;
    margin-bottom: 11px;
  }

  .q-options {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }

  .q-option {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    text-align: left;
    border: 1.5px solid #e9ded0;
    border-radius: 11px;
    background: #fffdf9;
    padding: 10px 12px;
    cursor: pointer;
    transition: border-color 0.12s ease, background 0.12s ease;
  }

  .q-option:hover {
    border-color: #cbe1d8;
  }

  .q-option.sel {
    border-color: #2f6e60;
    background: #edf5f1;
  }

  .q-mark {
    width: 16px;
    flex: none;
    color: #2f6e60;
    font-weight: 800;
    font-size: 12px;
    line-height: 1.4;
  }

  .q-option-text span {
    display: block;
    font: 700 13px "Hanken Grotesk";
    color: #4f473f;
  }

  .q-option-text small {
    display: block;
    font: 500 11.5px "Hanken Grotesk";
    color: #8a7f73;
    margin-top: 1px;
  }

  .q-free {
    width: 100%;
    border: 1.5px solid #e9ded0;
    border-radius: 11px;
    background: #fff;
    padding: 9px 11px;
    font: 500 13px "Hanken Grotesk";
    color: #3f3933;
  }

  .review-toggle {
    display: inline-flex;
    gap: 4px;
    border: 1px solid #e9ded0;
    border-radius: 10px;
    background: #fbf6ef;
    padding: 3px;
    margin-bottom: 11px;
  }

  .review-toggle button {
    border: none;
    border-radius: 8px;
    background: transparent;
    color: #6b6157;
    font: 700 12px "Hanken Grotesk";
    padding: 6px 13px;
    cursor: pointer;
  }

  .review-toggle button.on {
    background: #fff;
    color: #2f6e60;
    box-shadow: 0 1px 4px rgba(43, 37, 32, 0.1);
  }

  .editor {
    width: 100%;
    min-height: 300px;
    resize: vertical;
    border: 1.5px solid #e9ded0;
    border-radius: 13px;
    background: #fff;
    padding: 13px;
    color: #3f3933;
    line-height: 1.5;
  }

  .mono {
    font: 500 12.5px "JetBrains Mono", monospace;
  }

  .preview {
    border: 1px solid #eee3d4;
    border-radius: 13px;
    background: #fffaf4;
    padding: 4px 17px;
    overflow-wrap: anywhere;
  }

  .md :global(h1) {
    font: 800 17px "Hanken Grotesk";
    color: #2b2520;
  }

  .md :global(h2) {
    font: 800 14px "Hanken Grotesk";
    color: #3f3933;
  }

  .md :global(p),
  .md :global(li) {
    font: 500 13.5px/1.55 "Hanken Grotesk";
    color: #5c534a;
  }
</style>
