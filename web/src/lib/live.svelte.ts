// live.svelte.ts — the single, app-wide WebSocket connection and the reactive
// state derived from it. It is deliberately owned above any page so it survives
// route changes: attention signalling (toasts, the session red dot, the Chat
// nav badge) must work no matter where the user is in the dashboard. Chat.svelte
// consumes this store rather than opening its own socket.
//
// In-app attention state here is live-derived (no persistence). Out-of-app
// notifications (Web Push, future native) are handled by the daemon; this module
// only registers the browser for push and routes taps back to a session.

import { getUsage, getVapidKey, subscribePush } from "./api";
import { auth, WS_PROTOCOL, wsTokenProtocol } from "./auth.svelte";
import { appBase, wsUrl } from "./base";
import { request } from "./http";
import { randomID } from "./id";
import type {
  ActiveTurnSummary,
  ClientMessage,
  ServerMessage,
  Session,
  UsageSnapshot,
} from "./types";

export type ConnStatus = "connecting" | "live" | "offline";
export type PushState = "idle" | "enabling" | "enabled" | "denied" | "unsupported";

export interface Toast {
  id: string;
  title: string;
  body: string;
  kind: "permission" | "question" | "plan";
  sessionId: string;
}

type Pending = "permission" | "question" | "assistant" | "";

const TOAST_TTL_MS = 8000;

class LiveStore {
  status = $state<ConnStatus>("connecting");
  sessions = $state<Session[]>([]);
  activeTurns = $state<Record<string, ActiveTurnSummary>>({});
  usage = $state<UsageSnapshot[]>([]);
  usageRefreshing = $state(false);
  usageRefreshError = $state<string | null>(null);
  toasts = $state<Toast[]>([]);

  // Per-profile usage snapshots keyed by profile key ("claude"/"codex" for the
  // implicit defaults, else the profile name). Drives the composer usage chip.
  usageByProfile = $derived(
    new Map(this.usage.map((snap) => [snap.profile, snap])),
  );

  // Session IDs currently blocked on the user (permission or question). Drives
  // the session-row red dot and the Chat nav badge.
  attention = $derived(
    new Set(
      [
        ...Object.values(this.activeTurns)
          .filter((t) => t.pending === "permission" || t.pending === "question")
          .map((t) => t.session_id),
        ...this.sessions.filter((s) => s.PlanState === "awaiting_approval").map((s) => s.ID),
      ],
    ),
  );

  private ws: WebSocket | null = null;
  private started = false;
  private reconnect: number | undefined;
  // Rising-edge tracking so a toast fires once per transition into a pending
  // state, regardless of which message (direct event vs. list state) reveals it.
  private lastPending: Record<string, Pending> = {};
  private subscribers = new Set<(msg: ServerMessage) => void>();
  private navigator: ((sessionId: string) => void) | null = null;
  private usageRefreshPromise: Promise<void> | null = null;

  // connect is idempotent: the first caller (App.svelte, once the token gate
  // has passed) opens the socket; later callers just ensure it is open again —
  // needed after a token rotation dropped the app back to the gate.
  connect() {
    if (this.started) {
      if (!this.ws || this.ws.readyState === WebSocket.CLOSED) this.open();
      return;
    }
    this.started = true;
    this.open();
    window.setInterval(() => this.send({ type: "list" }), 4000);
    this.listenForServiceWorker();
  }

  private open() {
    if (!auth.token) return; // gated: the token screen is up, nothing to connect
    this.status = "connecting";
    // The WS URL derives from the app's base (sub-path safe under Ingress) and
    // the token rides the subprotocol list — the browser WebSocket API cannot
    // set headers. The server echoes only the non-secret protocol.
    const ws = new WebSocket(wsUrl(), [WS_PROTOCOL, wsTokenProtocol(auth.token)]);
    this.ws = ws;
    ws.onopen = () => {
      this.status = "live";
      this.send({ type: "list" });
    };
    ws.onclose = (event) => {
      this.status = "offline";
      if (event.code === 4401) {
        // Token rotated (HA12): drop to the token screen instead of retrying.
        auth.invalidate();
        return;
      }
      this.scheduleReconnect();
    };
    ws.onerror = () => {
      this.status = "offline";
    };
    ws.onmessage = (event) => this.handle(JSON.parse(event.data) as ServerMessage);
  }

  private scheduleReconnect() {
    if (this.reconnect) return;
    this.reconnect = window.setTimeout(() => {
      this.reconnect = undefined;
      void this.retryOrGate();
    }, 2000);
  }

  // retryOrGate disambiguates "daemon down" from "token rejected": a rejected
  // WS handshake surfaces as a generic close in browsers, so probe the API —
  // request() drops the stored token on 401, which gates the app.
  private async retryOrGate() {
    if (!auth.token) return;
    try {
      await request("api/auth/check");
    } catch {
      // Network error: daemon down — keep retrying below.
    }
    if (auth.token) this.open();
  }

  send(msg: ClientMessage) {
    if (this.ws?.readyState !== WebSocket.OPEN) return;
    this.ws.send(JSON.stringify(msg));
  }

  refreshUsage(): Promise<void> {
    if (this.usageRefreshPromise) return this.usageRefreshPromise;

    this.usageRefreshing = true;
    this.usageRefreshError = null;
    this.usageRefreshPromise = getUsage(true)
      .then((usage) => {
        this.usage = usage;
      })
      .catch((err: unknown) => {
        const message = err instanceof Error ? err.message.trim() : "";
        this.usageRefreshError = message || "Couldn't refresh usage.";
      })
      .finally(() => {
        this.usageRefreshing = false;
        this.usageRefreshPromise = null;
      });

    return this.usageRefreshPromise;
  }

  // dreamConnected reports whether the live socket can carry a streamed dream.
  // Callers fall back to the REST dream endpoint (no animation) when offline.
  dreamConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  // dream asks the daemon to consolidate an agent's memory now, streaming phases
  // back as "dream_state" messages page components observe via subscribe().
  // Returns the request_id so a component can match its own dream's events.
  dream(agentName: string): string {
    const requestId = randomID();
    this.send({ type: "dream", request_id: requestId, agent_name: agentName });
    return requestId;
  }

  // subscribe registers a raw-message handler (Chat.svelte uses it for its own
  // rendering). Returns an unsubscribe function.
  subscribe(fn: (msg: ServerMessage) => void): () => void {
    this.subscribers.add(fn);
    return () => this.subscribers.delete(fn);
  }

  // setNavigator lets App.svelte wire "open this session" so toast taps and
  // Web Push notification clicks can route to the right chat.
  setNavigator(fn: (sessionId: string) => void) {
    this.navigator = fn;
  }

  navigateToSession(sessionId: string) {
    this.navigator?.(sessionId);
  }

  dismissToast(id: string) {
    this.toasts = this.toasts.filter((t) => t.id !== id);
  }

  private handle(msg: ServerMessage) {
    // Store-owned state first (sessions, turns, attention, toasts)…
    switch (msg.type) {
      case "state":
        this.sessions = msg.sessions ?? [];
        this.applyTurnSummaries(msg.active_turns ?? []);
        if (msg.usage) this.usage = msg.usage;
        this.edgePlanAttention();
        break;
      case "session":
        if (msg.session) {
          const s = msg.session;
          this.sessions = [s, ...this.sessions.filter((e) => e.ID !== s.ID)];
          this.edgePlanAttention();
        }
        break;
      case "turn_state":
        if (msg.turn_state) {
          const ts = msg.turn_state;
          if (ts.status === "running") {
            const pending: Pending = ts.pending_permission
              ? "permission"
              : ts.pending_user_input
                ? "question"
                : ts.pending_assistant
                  ? "assistant"
                  : "";
            this.setTurn({ session_id: ts.session_id, turn_id: ts.turn_id, status: ts.status, pending });
          } else {
            this.clearTurn(ts.session_id);
          }
        }
        break;
      case "permission_request":
        if (msg.session_id) this.markPending(msg.session_id, "permission");
        break;
      case "user_input_request":
        if (msg.session_id) this.markPending(msg.session_id, "question");
        break;
      case "done":
      case "error":
        if (msg.session_id) this.clearTurn(msg.session_id);
        break;
    }
    // …then hand the raw message to page-level subscribers (chat rendering).
    for (const fn of this.subscribers) fn(msg);
  }

  private applyTurnSummaries(turns: ActiveTurnSummary[]) {
    const next: Record<string, ActiveTurnSummary> = {};
    for (const t of turns) next[t.session_id] = t;
    this.activeTurns = next;
    // Reconcile rising edges: any session now pending that wasn't fires a toast;
    // sessions no longer present reset their edge so a future request re-alerts.
    const seen = new Set<string>();
    for (const t of turns) {
      seen.add(t.session_id);
      this.edge(t.session_id, (t.pending ?? "") as Pending);
    }
    for (const id of Object.keys(this.lastPending)) {
      if (id.includes(":plan")) continue;
      if (!seen.has(id)) this.lastPending[id] = "";
    }
  }

  private setTurn(summary: ActiveTurnSummary) {
    this.activeTurns = { ...this.activeTurns, [summary.session_id]: summary };
    this.edge(summary.session_id, (summary.pending ?? "") as Pending);
  }

  private markPending(sessionId: string, pending: "permission" | "question") {
    const existing = this.activeTurns[sessionId];
    this.activeTurns = {
      ...this.activeTurns,
      [sessionId]: {
        session_id: sessionId,
        turn_id: existing?.turn_id ?? "",
        status: "running",
        pending,
      },
    };
    this.edge(sessionId, pending);
  }

  private clearTurn(sessionId: string) {
    const { [sessionId]: _gone, ...rest } = this.activeTurns;
    this.activeTurns = rest;
    this.lastPending[sessionId] = "";
  }

  // edge fires a toast on a rising transition into a blocked state.
  private edge(sessionId: string, pending: Pending) {
    const prev = this.lastPending[sessionId] ?? "";
    this.lastPending[sessionId] = pending;
    if (prev === pending) return;
    if (pending === "permission" || pending === "question") {
      this.pushToast(sessionId, pending);
    }
  }

  private pushToast(sessionId: string, kind: "permission" | "question") {
    const agent = this.sessions.find((s) => s.ID === sessionId)?.AgentName ?? "An agent";
    const toast: Toast = {
      id: randomID(),
      kind,
      sessionId,
      title: kind === "permission" ? `${agent} needs approval` : `${agent} has a question`,
      body: kind === "permission" ? "A tool action is waiting for your decision." : "Answer to let the agent continue.",
    };
    this.toasts = [...this.toasts, toast];
    window.setTimeout(() => this.dismissToast(toast.id), TOAST_TTL_MS);
  }

  private edgePlanAttention() {
    for (const session of this.sessions) {
      const key = `${session.ID}:plan`;
      if (session.PlanState !== "awaiting_approval") {
        this.lastPending[key] = "";
        continue;
      }
      if (this.lastPending[key] === "question") continue;
      this.lastPending[key] = "question";
      const toast: Toast = {
        id: randomID(),
        kind: "plan",
        sessionId: session.ID,
        title: `${session.AgentName} submitted a plan`,
        body: "Review it to approve, revise, or reject.",
      };
      this.toasts = [...this.toasts, toast];
      window.setTimeout(() => this.dismissToast(toast.id), TOAST_TTL_MS);
    }
  }

  // ---- Web Push ---------------------------------------------------------

  // refreshPushStatus checks the browser/daemon push state without prompting.
  // If permission is already granted, keep the browser subscribed and registered
  // with the daemon so approved notifications stay effectively on.
  async refreshPushStatus(): Promise<PushState> {
    if (!this.pushSupported()) return "unsupported";
    if (Notification.permission === "denied") return "denied";

    const { public_key } = await getVapidKey();
    if (!public_key) return "unsupported";
    if (Notification.permission !== "granted") return "idle";

    await this.ensurePushSubscription(public_key);
    return "enabled";
  }

  // enablePush registers the service worker, requests OS notification
  // permission, and subscribes this browser with the daemon. Must be invoked
  // from a user gesture (browsers gate the permission prompt on one).
  async enablePush(): Promise<PushState> {
    if (!this.pushSupported()) return "unsupported";
    // Confirm the daemon actually has push configured before prompting the user.
    const { public_key } = await getVapidKey();
    if (!public_key) return "unsupported";

    const permission = await Notification.requestPermission();
    if (permission !== "granted") return "denied";

    await this.ensurePushSubscription(public_key);
    return "enabled";
  }

  private pushSupported(): boolean {
    return "Notification" in window && "serviceWorker" in navigator && "PushManager" in window;
  }

  private async ensurePushSubscription(publicKey: string): Promise<void> {
    // Register relative to the app's base so the worker's scope matches the
    // Ingress sub-path (HA14).
    const reg = await navigator.serviceWorker.register(new URL("sw.js", appBase));
    const ready = await navigator.serviceWorker.ready.catch(() => reg);

    const existing = await ready.pushManager.getSubscription();
    const sub =
      existing ??
      (await ready.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(publicKey) as BufferSource,
      }));
    await subscribePush(sub.toJSON());
  }

  private listenForServiceWorker() {
    if (!("serviceWorker" in navigator)) return;
    navigator.serviceWorker.addEventListener("message", (event) => {
      const data = event.data as { type?: string; session_id?: string } | undefined;
      if (data?.type === "notification-click" && data.session_id) {
        this.navigateToSession(data.session_id);
      }
    });
  }
}

// urlBase64ToUint8Array converts a VAPID public key (URL-safe base64) into the
// byte array PushManager.subscribe expects.
function urlBase64ToUint8Array(base64: string): Uint8Array {
  const padding = "=".repeat((4 - (base64.length % 4)) % 4);
  const normalized = (base64 + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(normalized);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

export const live = new LiveStore();
