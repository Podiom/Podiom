<!--
  The Notification Center: a slide-over list of what Podiom did while nobody was
  watching.

  A panel rather than a page, because notifications are cross-cutting — they are about
  goals, schedules, tasks and sessions alike — so they need to be reachable from
  wherever the user already is, and a nav slot would make them a destination instead.

  Read and resolved are shown as separate things: a dot for "not seen yet", an accent
  for "still waiting on you". Reading an ask does not answer it, and the UI should not
  imply otherwise.
-->
<script lang="ts">
  import { notifications, type ActionOutcome } from "./notifications.svelte";
  import { targetFromNotification, type Target } from "./deeplink";
  import type { NotificationView } from "./types";

  let { onNavigate = (_t: Target) => {} }: { onNavigate?: (t: Target) => void } = $props();

  // conflicts holds the explanation for a notification whose action was rejected,
  // keyed by notification id, so a stale tap says what happened instead of silently
  // rewriting the buttons.
  let conflicts = $state<Record<string, string>>({});

  const groups = $derived.by(() => {
    const byDay = new Map<string, NotificationView[]>();
    for (const item of notifications.items) {
      const label = dayLabel(item.CreatedAt);
      const bucket = byDay.get(label);
      if (bucket) bucket.push(item);
      else byDay.set(label, [item]);
    }
    return [...byDay.entries()];
  });

  // parseStamp reads the daemon's "YYYY-MM-DD HH:MM:SS" UTC timestamps. They carry no
  // zone marker, so the Z is added explicitly rather than letting the browser guess
  // local time and shift everything by the offset.
  function parseStamp(raw: string): Date | null {
    if (!raw) return null;
    const iso = raw.includes("T") ? raw : raw.replace(" ", "T") + "Z";
    const date = new Date(iso);
    return Number.isNaN(date.getTime()) ? null : date;
  }

  function dayLabel(raw: string): string {
    const date = parseStamp(raw);
    if (!date) return "Earlier";
    const now = new Date();
    const sameDay = date.toDateString() === now.toDateString();
    if (sameDay) return "Today";
    const yesterday = new Date(now);
    yesterday.setDate(now.getDate() - 1);
    if (date.toDateString() === yesterday.toDateString()) return "Yesterday";
    return date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  }

  function relTime(raw: string): string {
    const date = parseStamp(raw);
    if (!date) return "";
    const seconds = Math.max(0, Math.round((Date.now() - date.getTime()) / 1000));
    if (seconds < 60) return "just now";
    const minutes = Math.round(seconds / 60);
    if (minutes < 60) return `${minutes} min ago`;
    const hours = Math.round(minutes / 60);
    if (hours < 24) return `${hours} h ago`;
    return `${Math.round(hours / 24)} d ago`;
  }

  async function open(item: NotificationView) {
    onNavigate(targetFromNotification(item));
    if (!item.ReadAt) await notifications.markRead(item.ID);
    notifications.close();
  }

  async function act(item: NotificationView, actionID: string) {
    // "open" is navigation, never a domain write, so it does not go to the server.
    if (actionID === "open" || actionID === "review") {
      await open(item);
      return;
    }
    const outcome: ActionOutcome = await notifications.act(item.ID, actionID);
    if (outcome.status === "stale") {
      conflicts = {
        ...conflicts,
        [item.ID]: `Already ${outcome.resourceState} — handled somewhere else.`,
      };
      return;
    }
    if (outcome.status === "error") {
      conflicts = { ...conflicts, [item.ID]: outcome.message };
      return;
    }
    const { [item.ID]: _cleared, ...rest } = conflicts;
    conflicts = rest;
  }
</script>

{#if notifications.open}
  <!-- The scrim closes the panel; it is a button so keyboard and screen-reader users
       get the same dismissal the pointer does. -->
  <button class="scrim" aria-label="Close notifications" onclick={() => notifications.close()}
  ></button>

  <aside class="panel" aria-label="Notifications">
    <header class="head">
      <div class="title-row">
        <span class="title">Notifications</span>
        {#if notifications.unread > 0}
          <span class="count mono">{notifications.unread}</span>
        {/if}
      </div>
      <div class="head-actions">
        {#if notifications.unread > 0}
          <button class="link" onclick={() => notifications.markAllRead()}>Mark all read</button>
        {/if}
        <button class="close" aria-label="Close" onclick={() => notifications.close()}>✕</button>
      </div>
    </header>

    <div class="body">
      {#if notifications.error}
        <div class="empty error">{notifications.error}</div>
      {:else if notifications.items.length === 0}
        <div class="empty">
          {notifications.loading ? "Loading…" : "Nothing yet. Podiom will tell you when something needs you."}
        </div>
      {/if}

      {#each groups as [label, items] (label)}
        <div class="day mono">{label}</div>
        {#each items as item (item.ID)}
          <div
            class="row"
            class:unread={!item.ReadAt}
            class:waiting={item.Actionable && !item.ResolvedAt}>
            <button class="row-main" onclick={() => open(item)}>
              <span class="dot" class:on={!item.ReadAt}></span>
              <span class="row-text">
                <span class="row-title">{item.Title}</span>
                {#if item.Body}<span class="row-body">{item.Body}</span>{/if}
                <span class="row-meta mono">{relTime(item.CreatedAt)}</span>
              </span>
            </button>

            {#if conflicts[item.ID]}
              <div class="conflict">{conflicts[item.ID]}</div>
            {/if}

            <!-- Actions come from the server per read, so a notification that has been
                 handled elsewhere already shows only its way in. -->
            {#if item.actions.length > 1 && !item.ResolvedAt}
              <div class="row-actions">
                {#each item.actions as action (action.id)}
                  {#if action.id !== "open" && action.id !== "review"}
                    <button
                      class="act"
                      disabled={notifications.busy === item.ID}
                      onclick={() => act(item, action.id)}>{action.label}</button>
                  {/if}
                {/each}
                <button
                  class="act ghost"
                  disabled={notifications.busy === item.ID}
                  onclick={() => notifications.dismiss(item.ID)}>Dismiss</button>
              </div>
            {/if}
          </div>
        {/each}
      {/each}

      {#if notifications.items.length < notifications.total}
        <button class="more" disabled={notifications.loading} onclick={() => notifications.loadMore()}>
          {notifications.loading ? "Loading…" : "Show older"}
        </button>
      {/if}
    </div>
  </aside>
{/if}

<style>
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 60;
    border: 0;
    padding: 0;
    background: rgba(24, 22, 20, 0.28);
    cursor: default;
  }

  .panel {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    z-index: 61;
    display: flex;
    flex-direction: column;
    width: min(26rem, 100vw);
    background: var(--surface, #fff);
    border-left: 1px solid var(--line, #e7e2da);
    box-shadow: -12px 0 32px rgba(24, 22, 20, 0.12);
  }

  /* On a phone the panel is the whole screen: a 26rem drawer beside nothing reads as a
     broken layout rather than a drawer. */
  @media (max-width: 640px) {
    .panel {
      width: 100vw;
      border-left: 0;
      padding-bottom: env(safe-area-inset-bottom);
    }
  }

  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    padding: 1rem 1rem 0.75rem;
    border-bottom: 1px solid var(--line, #e7e2da);
    padding-top: calc(1rem + env(safe-area-inset-top));
  }

  .title-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .title {
    font-weight: 650;
    font-size: 0.98rem;
  }

  .count {
    font-size: 0.7rem;
    padding: 0.1rem 0.4rem;
    border-radius: 999px;
    background: var(--teal-deep, #2f7f6f);
    color: #fff;
  }

  .head-actions {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .link {
    border: 0;
    background: none;
    padding: 0;
    font-size: 0.78rem;
    color: var(--teal-deep, #2f7f6f);
    cursor: pointer;
  }

  .close {
    border: 0;
    background: none;
    font-size: 0.85rem;
    line-height: 1;
    padding: 0.25rem;
    cursor: pointer;
    color: var(--ink-soft, #6b625a);
  }

  .body {
    flex: 1;
    overflow-y: auto;
    padding: 0.5rem 0 1.5rem;
  }

  .day {
    padding: 0.75rem 1rem 0.35rem;
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--ink-soft, #6b625a);
  }

  .row {
    border-bottom: 1px solid var(--line-soft, #f0ece5);
    padding-bottom: 0.35rem;
  }

  /* An unresolved actionable notification is the one thing here that is still asking
     for something, so it gets the only accent in the list. */
  .row.waiting {
    border-left: 3px solid var(--teal-deep, #2f7f6f);
  }

  .row-main {
    display: flex;
    gap: 0.6rem;
    width: 100%;
    text-align: left;
    border: 0;
    background: none;
    padding: 0.6rem 1rem 0.4rem;
    cursor: pointer;
  }

  .row-main:hover {
    background: var(--surface-soft, #faf8f5);
  }

  .dot {
    flex: 0 0 auto;
    width: 7px;
    height: 7px;
    margin-top: 0.42rem;
    border-radius: 50%;
    background: transparent;
  }

  .dot.on {
    background: var(--teal-deep, #2f7f6f);
  }

  .row-text {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    min-width: 0;
  }

  .row-title {
    font-size: 0.86rem;
    font-weight: 560;
  }

  .row.unread .row-title {
    font-weight: 660;
  }

  .row-body {
    font-size: 0.79rem;
    color: var(--ink-soft, #6b625a);
    overflow-wrap: anywhere;
  }

  .row-meta {
    font-size: 0.68rem;
    color: var(--ink-soft, #6b625a);
    opacity: 0.75;
  }

  .row-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    padding: 0 1rem 0.5rem 2.1rem;
  }

  .act {
    border: 1px solid var(--line, #e7e2da);
    background: var(--surface, #fff);
    border-radius: 7px;
    padding: 0.25rem 0.6rem;
    font-size: 0.76rem;
    cursor: pointer;
  }

  .act:hover:not(:disabled) {
    border-color: var(--teal-deep, #2f7f6f);
  }

  .act:disabled {
    opacity: 0.55;
    cursor: default;
  }

  .act.ghost {
    color: var(--ink-soft, #6b625a);
  }

  .conflict {
    margin: 0 1rem 0.4rem 2.1rem;
    font-size: 0.74rem;
    color: #a0522d;
  }

  .empty {
    padding: 2rem 1.25rem;
    text-align: center;
    font-size: 0.82rem;
    color: var(--ink-soft, #6b625a);
  }

  .empty.error {
    color: #a0522d;
  }

  .more {
    display: block;
    margin: 0.75rem auto 0;
    border: 1px solid var(--line, #e7e2da);
    background: var(--surface, #fff);
    border-radius: 8px;
    padding: 0.35rem 0.8rem;
    font-size: 0.78rem;
    cursor: pointer;
  }
</style>
