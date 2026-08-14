<script lang="ts">
  // GoalActionItems — the carousel of work a goal's agent handed back to the
  // user because only a human can do it (post from a personal account, sign
  // something, make a call). One card per item: the agent's instructions above,
  // the user's verdict and note below.
  //
  // A goal can accumulate many of these, so they slide horizontally rather than
  // stacking into a wall on the goal page. The track is plain overflow-x with
  // scroll-snap — that gives native swipe on touch and a real scrollbar gesture
  // on desktop, with no touch handlers to fight the page's own scrolling. The
  // card width leaves the next card peeking, which is the affordance that says
  // "there is more this way".
  import { untrack } from "svelte";
  import { slide } from "svelte/transition";
  import AgentAvatar from "./AgentAvatar.svelte";
  import AgentMarkdown from "./AgentMarkdown.svelte";
  import VoiceButton from "./VoiceButton.svelte";
  import { appendTranscript } from "./voice";
  import type { GoalActionItem, GoalActionItemStatus } from "./types";

  let {
    items,
    onRespond,
  }: {
    items: GoalActionItem[];
    onRespond: (id: string, status: GoalActionItemStatus, response: string) => Promise<void>;
  } = $props();

  // Draft notes are keyed by item id so scrolling between cards never carries
  // one card's text onto another.
  let drafts = $state<Record<string, string>>({});
  let busy = $state("");
  let active = $state(0);
  let track = $state<HTMLDivElement | null>(null);

  const openCount = $derived(items.filter((i) => i.Status === "open").length);
  const complete = $derived(openCount === 0);
  let collapsed = $state(untrack(() => !items.some((i) => i.Status === "open")));
  let previousOpenCount = $state(untrack(() => openCount));

  // A completed panel starts collapsed and closes as soon as its final open
  // item is answered. If more work arrives later, make it visible immediately.
  $effect(() => {
    const currentOpenCount = openCount;
    const previous = untrack(() => previousOpenCount);
    if (currentOpenCount > 0) collapsed = false;
    else if (previous > 0) collapsed = true;
    previousOpenCount = currentOpenCount;
  });

  // The verdicts, in the order they escalate: did it / tried and couldn't /
  // chose not to. The agent branches on these, so the labels here and the
  // wording in the timeline entry come from the same three values.
  const VERDICTS: { status: GoalActionItemStatus; label: string; hint: string }[] = [
    { status: "done", label: "Done", hint: "You carried this out" },
    { status: "blocked", label: "Couldn't do", hint: "You tried; the agent should find another route" },
    { status: "declined", label: "Not doing", hint: "The agent should drop this approach" },
  ];

  const VERDICT_LABEL: Record<GoalActionItemStatus, string> = {
    open: "Waiting on you",
    done: "Done",
    blocked: "Couldn't do",
    declined: "Not doing",
  };

  function cardStride(): number {
    if (!track) return 0;
    const card = track.querySelector<HTMLElement>(".action-card");
    if (!card) return 0;
    // Card width plus the flex gap, so a step lands exactly on the next snap point.
    const gap = parseFloat(getComputedStyle(track).columnGap || "0") || 0;
    return card.offsetWidth + gap;
  }

  function onScroll() {
    const stride = cardStride();
    if (!track || stride <= 0) return;
    active = Math.min(items.length - 1, Math.max(0, Math.round(track.scrollLeft / stride)));
  }

  function goTo(index: number) {
    const stride = cardStride();
    if (!track || stride <= 0) return;
    track.scrollTo({ left: index * stride, behavior: "smooth" });
  }

  function step(delta: number) {
    goTo(Math.min(items.length - 1, Math.max(0, active + delta)));
  }

  async function respond(item: GoalActionItem, status: GoalActionItemStatus) {
    if (busy) return;
    busy = item.ID;
    try {
      await onRespond(item.ID, status, (drafts[item.ID] ?? "").trim());
      delete drafts[item.ID];
    } finally {
      busy = "";
    }
  }

  function filedLabel(item: GoalActionItem): string {
    const d = new Date(item.CreatedAt.includes("Z") ? item.CreatedAt : item.CreatedAt + "Z");
    if (Number.isNaN(d.getTime())) return "";
    const days = Math.floor((Date.now() - d.getTime()) / 86_400_000);
    if (days <= 0) return "filed today";
    if (days === 1) return "filed yesterday";
    return `filed ${days} days ago`;
  }
</script>

<div class="actions" class:complete>
  <div class="actions-head">
    {#if complete}
      <button
        class="actions-summary toggle"
        type="button"
        aria-expanded={!collapsed}
        aria-controls="goal-action-items-content"
        onclick={() => (collapsed = !collapsed)}
      >
        <svg class="actions-icon" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 11l3 3 8-8"/><path d="M20 12v6a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h9"/></svg>
        <span class="actions-title">Action items handled</span>
        <span class="actions-chevron" class:collapsed aria-hidden="true">⌄</span>
      </button>
    {:else}
      <div class="actions-summary">
        <svg class="actions-icon" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 11l3 3 8-8"/><path d="M20 12v6a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h9"/></svg>
        <span class="actions-title">Action items for you</span>
        <span class="action-count mono">{openCount}</span>
      </div>
    {/if}
    {#if !collapsed && items.length > 1}
      <span class="actions-nav">
        <button class="arrow" disabled={active === 0} onclick={() => step(-1)} aria-label="Previous action item">‹</button>
        <button class="arrow" disabled={active === items.length - 1} onclick={() => step(1)} aria-label="Next action item">›</button>
      </span>
    {/if}
  </div>

  {#if !collapsed}
    <div id="goal-action-items-content" class="actions-body" transition:slide={{ duration: 200 }}>
      <div class="actions-sub">
        {complete
          ? "Work the agent couldn't do itself. These items are handled and kept here for reference."
          : "Work the agent can't do itself. It keeps working while these wait — your answer reaches it at the next review."}
      </div>

      <div class="actions-track" bind:this={track} onscroll={onScroll}>
        {#each items as item (item.ID)}
          <div class="action-card" class:answered={item.Status !== "open"}>
            <div class="action-top">
              <AgentAvatar name={item.AgentName} size={19} radius={6} fontSize={9} />
              <span class="action-agent">{item.AgentName}</span>
              <span class="action-filed mono">{filedLabel(item)}</span>
              <span class="action-status mono" class:open={item.Status === "open"}>{VERDICT_LABEL[item.Status]}</span>
            </div>

            <div class="action-title">{item.Title}</div>
            {#if item.Why}
              <div class="action-why"><AgentMarkdown content={item.Why} /></div>
            {/if}

            {#if item.Instructions}
              <div class="action-label mono">Instructions</div>
              <div class="action-instructions"><AgentMarkdown content={item.Instructions} /></div>
            {/if}

            {#if item.Status === "open"}
              <div class="action-label-row">
                <div class="action-label mono">Your response</div>
                <VoiceButton
                  size="sm"
                  onText={(t) => (drafts[item.ID] = appendTranscript(drafts[item.ID] ?? "", t))} />
              </div>
              <textarea
                class="field-area action-field"
                rows="3"
                bind:value={drafts[item.ID]}
                placeholder="What happened — links, outcome, or why you couldn't."></textarea>
              <div class="action-verdicts">
                {#each VERDICTS as v}
                  <button
                    class="verdict"
                    class:primary={v.status === "done"}
                    title={v.hint}
                    disabled={busy === item.ID}
                    onclick={() => respond(item, v.status)}>{v.label}</button>
                {/each}
              </div>
            {:else}
              <div class="action-label mono">Your response</div>
              <div class="action-answer">
                {#if item.Response}
                  {item.Response}
                {:else}
                  <span class="muted">No note — you answered “{VERDICT_LABEL[item.Status]}”.</span>
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      </div>

      {#if items.length > 1}
        <div class="actions-pager">
          <span class="dots">
            {#each items as item, i (item.ID)}
              <button
                class="dot"
                class:on={i === active}
                aria-label={`Go to action item ${i + 1}`}
                onclick={() => goTo(i)}></button>
            {/each}
          </span>
          <span class="pager-count mono">{active + 1} of {items.length}</span>
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  /* Terracotta, deliberately not the amber of access requests and questions:
     those block the goal, these do not. */
  .actions {
    background: #fdf3ee;
    border: 1px solid #f0d5c7;
    border-radius: 18px;
    padding: 18px 0 16px;
    transition: background 0.2s ease, border-color 0.2s ease;
  }
  .actions.complete {
    background: #eaf1ed;
    border-color: #cfe3d8;
  }
  .actions-head {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 0 20px;
  }
  .actions-summary {
    min-width: 0;
    flex: 1;
    display: flex;
    align-items: center;
    gap: 9px;
  }
  .actions-summary.toggle {
    border: 0;
    padding: 0;
    background: transparent;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .actions-icon {
    flex: none;
    color: #b14e2a;
    transition: color 0.2s ease;
  }
  .actions-title {
    font-size: 14px;
    font-weight: 600;
    color: #8f3f1e;
    transition: color 0.2s ease;
  }
  .complete .actions-icon,
  .complete .actions-title {
    color: #3f7a5f;
  }
  .action-count {
    font-size: 11px;
    font-weight: 600;
    color: #b14e2a;
    background: #f8e1d5;
    border: 1px solid #eec7b3;
    border-radius: 999px;
    padding: 1px 8px;
  }
  .actions-nav {
    display: flex;
    gap: 5px;
  }
  .actions-chevron {
    margin-left: auto;
    color: #3f7a5f;
    font-size: 16px;
    line-height: 1;
    transition: transform 0.18s ease;
  }
  .actions-chevron.collapsed {
    transform: rotate(-90deg);
  }
  .arrow {
    width: 26px;
    height: 26px;
    border-radius: 8px;
    border: 1px solid #eec7b3;
    background: #fffdfb;
    color: #b14e2a;
    font-size: 16px;
    line-height: 1;
    cursor: pointer;
  }
  .arrow:disabled {
    opacity: 0.35;
    cursor: default;
  }
  .complete .arrow {
    border-color: #cfe3d8;
    color: #3f7a5f;
  }
  .actions-sub {
    padding: 5px 20px 0;
    font-size: 12.5px;
    color: #a8674a;
  }
  .complete .actions-sub,
  .complete .pager-count {
    color: #5f846f;
  }

  .actions-track {
    display: flex;
    gap: 12px;
    margin-top: 14px;
    padding: 0 20px 2px;
    overflow-x: auto;
    overscroll-behavior-x: contain;
    scroll-snap-type: x mandatory;
    scrollbar-width: none;
  }
  .actions-track::-webkit-scrollbar {
    display: none;
  }
  .action-card {
    flex: none;
    width: min(420px, 86%);
    scroll-snap-align: start;
    background: var(--surface);
    border: 1px solid #f0d5c7;
    border-radius: 14px;
    padding: 14px 15px 15px;
  }
  .action-card.answered {
    background: var(--surface-3);
    border-color: var(--line-2);
  }

  .action-top {
    display: flex;
    align-items: center;
    gap: 7px;
    margin-bottom: 9px;
  }
  .action-agent {
    font-size: 12.5px;
    font-weight: 600;
    color: var(--ink-soft);
  }
  .action-filed {
    font-size: 10.5px;
    color: var(--faint);
  }
  .action-status {
    margin-left: auto;
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--muted-2);
    background: var(--surface-3);
    border: 1px solid var(--line-2);
    border-radius: 999px;
    padding: 2px 8px;
    white-space: nowrap;
  }
  .action-status.open {
    color: #b14e2a;
    background: #f8e1d5;
    border-color: #eec7b3;
  }

  .action-title {
    font-size: 15px;
    font-weight: 600;
    color: var(--ink);
    line-height: 1.35;
  }
  .action-why {
    margin-top: 5px;
    font-size: 12.5px;
    color: var(--muted);
    line-height: 1.5;
  }

  .action-label {
    margin-top: 13px;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--faint);
  }
  .action-label-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .action-label-row .action-label {
    margin-bottom: 0;
  }

  .action-instructions {
    margin-top: 6px;
    font-size: 13px;
    color: var(--ink-soft);
    line-height: 1.6;
    /* Instructions are agent-written markdown of unknown length; keep the card a
       predictable size and let long ones scroll inside it. */
    max-height: 190px;
    overflow-y: auto;
    overscroll-behavior-y: contain;
  }
  .action-instructions :global(p) {
    margin: 0 0 8px;
  }
  .action-instructions :global(p:last-child) {
    margin-bottom: 0;
  }
  .action-instructions :global(ul),
  .action-instructions :global(ol) {
    margin: 0 0 8px;
    padding-left: 20px;
  }
  .action-instructions :global(pre) {
    background: var(--surface-3);
    border: 1px solid var(--line-2);
    border-radius: 8px;
    padding: 8px 10px;
    overflow-x: auto;
  }
  .action-instructions :global(code) {
    font-size: 12px;
  }

  .action-field {
    margin-top: 6px;
  }
  .action-verdicts {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
    margin-top: 10px;
  }
  .verdict {
    flex: 1 1 auto;
    border: 1px solid var(--field-line);
    background: var(--surface);
    color: var(--ink-soft);
    border-radius: 10px;
    padding: 8px 10px;
    font-size: 12.5px;
    font-weight: 600;
    cursor: pointer;
  }
  .verdict:hover:not(:disabled) {
    border-color: #eec7b3;
  }
  .verdict.primary {
    background: #b14e2a;
    border-color: #b14e2a;
    color: #fffdfb;
  }
  .verdict:disabled {
    opacity: 0.55;
    cursor: default;
  }

  .action-answer {
    margin-top: 6px;
    font-size: 13px;
    color: var(--ink-soft);
    line-height: 1.6;
    white-space: pre-wrap;
  }
  .muted {
    color: var(--muted-2);
  }

  .actions-pager {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px 20px 0;
  }
  .dots {
    display: flex;
    gap: 6px;
  }
  .dot {
    width: 7px;
    height: 7px;
    padding: 0;
    border-radius: 999px;
    border: 0;
    background: #e6cbbb;
    cursor: pointer;
  }
  .dot.on {
    background: #b14e2a;
  }
  .complete .dot {
    background: #c5ddd2;
  }
  .complete .dot.on {
    background: #3f7a5f;
  }
  .pager-count {
    margin-left: auto;
    font-size: 10.5px;
    color: #a8674a;
  }

  @media (max-width: 768px) {
    .actions {
      padding-top: 16px;
    }
    .actions-head,
    .actions-sub {
      padding-right: 14px;
      padding-left: 14px;
    }
    .actions-track {
      padding-right: 14px;
      padding-left: 14px;
      scroll-padding-inline: 14px;
    }
    .action-card {
      width: calc(100% - 12px);
      padding: 13px 14px 14px;
    }
    .action-top {
      flex-wrap: wrap;
    }
    .action-status {
      margin-left: 0;
    }
    .action-agent,
    .action-filed,
    .action-title,
    .action-why,
    .action-answer {
      min-width: 0;
      overflow-wrap: anywhere;
    }
    .action-verdicts .verdict {
      min-height: 44px;
      flex-basis: 100px;
    }
    .actions-pager {
      padding-right: 14px;
      padding-left: 14px;
    }
    .actions-nav {
      display: none;
    }
  }
</style>
