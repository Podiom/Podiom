// The Notification Center's client state.
//
// The daemon is authoritative: this store holds a page of history and keeps it
// current from the WebSocket rather than polling. Read and resolved state, and which
// actions are valid, are all decided server-side — acting on one device has to be
// visible on the others, and an action's validity depends on domain state that keeps
// moving after the notification was recorded.

import {
  listNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  resolveNotification,
  runNotificationAction,
} from "./api";
import { live } from "./live.svelte";
import type { NotificationListResponse, NotificationView, ServerMessage } from "./types";

// PAGE_SIZE is how many notifications one fetch brings back.
const PAGE_SIZE = 30;

// ActionOutcome tells the caller what happened, so the UI can explain a conflict
// rather than just failing.
export type ActionOutcome =
  | { status: "ok" }
  | { status: "stale"; reason: string; resourceState: string }
  | { status: "error"; message: string };

class NotificationStore {
  items = $state<NotificationView[]>([]);
  unread = $state(0);
  // attention drives the badge: unread notifications that actually need the user.
  attention = $state(0);
  total = $state(0);
  open = $state(false);
  loading = $state(false);
  error = $state<string | null>(null);
  // busy holds the notification id an action is running for, so one row can show
  // progress without disabling the whole panel.
  busy = $state<string | null>(null);

  private started = false;
  private unsubscribe: (() => void) | null = null;

  // unresolved counts actionable notifications still waiting on the user. It is
  // tracked separately from unread because reading an ask does not answer it.
  unresolved = $derived(this.items.filter((n) => n.Actionable && !n.ResolvedAt).length);

  // start subscribes to live updates and loads the first page. Idempotent, so the
  // shell can call it on boot without guarding.
  start() {
    if (this.started) return;
    this.started = true;
    this.unsubscribe = live.subscribe((msg) => this.apply(msg));
    void this.refresh();
  }

  stop() {
    this.unsubscribe?.();
    this.unsubscribe = null;
    this.started = false;
  }

  async refresh(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      this.adopt(await listNotifications({ limit: PAGE_SIZE }));
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  // loadMore appends the next page. The server orders by recorded time with a stable
  // tiebreak, so paging cannot repeat or skip a row even when several land in the
  // same second.
  async loadMore(): Promise<void> {
    if (this.loading || this.items.length >= this.total) return;
    this.loading = true;
    try {
      const page = await listNotifications({ limit: PAGE_SIZE, offset: this.items.length });
      const seen = new Set(this.items.map((n) => n.ID));
      this.items = [...this.items, ...page.notifications.filter((n) => !seen.has(n.ID))];
      this.unread = page.unread;
      this.attention = page.attention;
      this.total = page.total;
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  private adopt(page: NotificationListResponse) {
    this.items = page.notifications;
    this.unread = page.unread;
    this.attention = page.attention;
    this.total = page.total;
  }

  // apply folds a live message into the current page.
  private apply(msg: ServerMessage) {
    switch (msg.type) {
      case "notification": {
        if (!msg.notification) return;
        const incoming = msg.notification;
        // Arrives without its action set, because which actions are valid is decided
        // per read. Actions are filled in when the row is opened or refreshed.
        const view: NotificationView = { ...incoming, actions: [] };
        this.items = [view, ...this.items.filter((n) => n.ID !== incoming.ID)];
        this.total += 1;
        if (!incoming.ReadAt) {
          this.unread += 1;
          if (needsAttention(incoming)) this.attention += 1;
        }
        break;
      }
      case "notification_update": {
        if (!msg.notification) return;
        const updated = msg.notification;
        const previous = this.items.find((n) => n.ID === updated.ID);
        this.items = this.items.map((n) =>
          n.ID === updated.ID ? { ...n, ...updated } : n,
        );
        // A row read on another device changes the badge here too.
        if (previous && !previous.ReadAt && updated.ReadAt) {
          this.unread = Math.max(0, this.unread - 1);
          if (needsAttention(updated)) this.attention = Math.max(0, this.attention - 1);
        }
        if (previous && previous.ReadAt && !updated.ReadAt) {
          this.unread += 1;
          if (needsAttention(updated)) this.attention += 1;
        }
        break;
      }
      case "notifications_read_all":
        // Deliberately re-read rather than being sent every row: marking everything
        // read can touch hundreds, and the daemon sends no payload for that reason.
        void this.refresh();
        break;
    }
  }

  async markRead(id: string, read = true): Promise<void> {
    const previous = this.items;
    try {
      const updated = await markNotificationRead(id, read);
      this.replace(updated);
    } catch {
      // Already in that state on the server (the guarded update returns 404), which is
      // not worth surfacing — just restore what we had.
      this.items = previous;
    }
  }

  async markAllRead(): Promise<void> {
    try {
      await markAllNotificationsRead();
      await this.refresh();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  // dismiss records that the user has dealt with an actionable notification without
  // performing its operation. It does not touch the domain object.
  async dismiss(id: string): Promise<void> {
    this.busy = id;
    try {
      this.replace(await resolveNotification(id));
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.busy = null;
    }
  }

  // act performs one of a notification's actions.
  //
  // A conflict is reported rather than thrown: the notification may have outlived the
  // thing it was about — denied at a desk, then approved from a phone — and the server
  // returns the actions that are still valid plus the resource's current state so the
  // panel can correct itself instead of showing a bare failure.
  async act(id: string, actionID: string): Promise<ActionOutcome> {
    this.busy = id;
    try {
      const result = await runNotificationAction(id, actionID);
      if (result.status === "stale") {
        this.items = this.items.map((n) =>
          n.ID === id ? { ...n, ...result.notification, actions: result.actions } : n,
        );
        return {
          status: "stale",
          reason: result.reason,
          resourceState: result.resource.state,
        };
      }
      this.replace(result.notification);
      return { status: "ok" };
    } catch (e) {
      return { status: "error", message: e instanceof Error ? e.message : String(e) };
    } finally {
      this.busy = null;
    }
  }

  private replace(updated: NotificationView) {
    const previous = this.items.find((n) => n.ID === updated.ID);
    this.items = this.items.map((n) => (n.ID === updated.ID ? updated : n));
    if (previous && !previous.ReadAt && updated.ReadAt) {
      this.unread = Math.max(0, this.unread - 1);
      if (needsAttention(updated)) this.attention = Math.max(0, this.attention - 1);
    }
    if (previous && previous.ReadAt && !updated.ReadAt) {
      this.unread += 1;
      if (needsAttention(updated)) this.attention += 1;
    }
  }

  toggle() {
    this.open = !this.open;
    if (this.open) void this.refresh();
  }

  close() {
    this.open = false;
  }
}

// needsAttention mirrors the daemon's own rule for the badge, so an optimistic local
// adjustment between refreshes cannot disagree with the next server count.
function needsAttention(n: { Importance: string }): boolean {
  return n.Importance === "important" || n.Importance === "critical";
}

export const notifications = new NotificationStore();
