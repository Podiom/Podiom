// reachability.svelte.ts — whether the native apps can still reach their
// gateway, and the state the offline screen renders from.
//
// A browser gets this question answered for it: the daemon served the page, so
// if it is gone there is no page. The native apps load the SPA from the app
// bundle and talk to an address on the network, so they can outlive the thing
// they are a client of — sitting on stale data, silently failing every action.
// This store is what notices, and OfflineGate.svelte is what it puts on screen.
//
// It is deliberately idle while the app is healthy. live.svelte.ts already holds
// an open socket, and an open socket is proof of reachability, so nothing here
// polls until that socket drops. What it adds is the part the socket cannot do:
// telling apart the reasons it dropped, and not believing the first failure.

import { auth } from "./auth.svelte";
import { apiUrl, endpoint } from "./base";
import { live, type ConnStatus } from "./live.svelte";
import { isNative } from "./native";
import { classify, reachable, type OfflineReason, type Probe } from "./reachability";

// PROBE_TIMEOUT_MS matches connection.ts: long enough for a slow LAN, short
// enough that a hung daemon is called hung rather than left spinning.
const PROBE_TIMEOUT_MS = 6000;

// GRACE_MS is the pause between the first failed probe and the confirming one.
// The screen is a takeover, so it has to be right: a socket that drops and comes
// straight back — a Wi-Fi roam, a daemon restart, the phone waking — must never
// produce it. Two failures spanning this window is the bar.
const GRACE_MS = 3000;

// RETRY_SECONDS is the visible countdown between automatic attempts once the
// screen is up. Slow enough not to hammer an address that is not answering,
// short enough that a user watching it does not reach for the button.
const RETRY_SECONDS = 15;

class ReachabilityStore {
  // visible drives the takeover. False until a failure has been confirmed.
  visible = $state(false);
  reason = $state<OfflineReason | null>(null);
  code = $state("");
  phase = $state<"waiting" | "retrying">("waiting");
  countdown = $state(RETRY_SECONDS);
  attempt = $state(0);

  // endpoint is the gateway as the diagnostics row shows it. A getter rather
  // than $derived: base.ts holds the address in a plain module variable, so
  // there is nothing for a derivation to depend on and it would cache the first
  // value it ever saw — showing the old address after the user re-points the app.
  get endpoint(): string {
    return endpoint()?.host ?? "";
  }

  // offline mirrors what observe() last saw, and is what decides whether an
  // external nudge means anything. Deliberately not the same question as
  // "is work already scheduled", which arm() asks instead — conflating the two
  // is how a probe stops being scheduled at all.
  private offline = false;
  private timer: number | undefined;
  private probing = false;
  private listening = false;

  // observe is called from App.svelte with live.status inside an $effect, so it
  // re-runs whenever the socket changes state. An open socket disarms; anything
  // else starts the confirmation sequence.
  observe(status: ConnStatus) {
    if (!isNative) return;
    this.listen();
    this.offline = status !== "live";
    if (!this.offline) {
      this.recovered();
      return;
    }
    this.arm();
  }

  // retry is the "Try again" button. It skips the grace window: the user asking
  // is not a signal to be cautious about.
  retry() {
    void this.attemptProbe(true);
  }

  // openSettings sends the user back to the connection screen. auth.invalidate
  // clears only the token, so the screen comes up with the address pre-filled
  // and just the token field to fix — the same path a rotated token takes.
  openSettings() {
    this.hide();
    auth.invalidate();
  }

  // arm begins the sequence: probe now, and if that fails probe again after the
  // grace window. Idempotent against live.status churning while offline, which
  // it does — but the guard is "is there already work pending", not "have we
  // ever armed": a latch that outlives its own timer stops scheduling anything.
  private arm() {
    if (this.timer !== undefined || this.probing) return;
    // Deferred rather than called straight through. observe() runs inside an
    // $effect, and probing reads auth.token and visible — doing that on the
    // synchronous path would make the effect depend on state the probe itself
    // writes.
    this.timer = window.setTimeout(() => void this.attemptProbe(false), 0);
  }

  private listen() {
    if (this.listening) return;
    this.listening = true;
    // A network returning is the most reliable good news available, and it
    // arrives long before the next countdown tick would.
    window.addEventListener("online", () => this.nudge());
    // iOS suspends timers for a backgrounded WebView, so a phone coming back
    // would otherwise show a countdown frozen at whatever second it was
    // suspended on — a screen claiming it will retry when it will not.
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "visible") this.nudge();
    });
  }

  // nudge is an external hint that now is a good moment to look again. Unlike
  // the button it keeps the grace window, because the events that trigger it —
  // notably a Wi-Fi roam, which fires offline then online — are exactly the
  // transient churn the window exists to absorb.
  private nudge() {
    if (!this.offline) return;
    void this.attemptProbe(false);
  }

  private async attemptProbe(manual: boolean) {
    if (!isNative) return;
    // The token screen owns the display once there is no token, and the
    // reachability question is moot until there is one again.
    if (!auth.token) {
      this.hide();
      return;
    }
    if (this.probing) return;
    this.probing = true;
    this.clearTimer();
    this.phase = "retrying";
    this.attempt += 1;

    const result = await this.probe();
    this.probing = false;

    if (reachable(result)) {
      this.recovered();
      // The socket is what the app actually runs on, so bring it back rather
      // than waiting for its own retry. connect() is idempotent.
      live.connect();
      return;
    }

    const verdict = classify(navigator.onLine, result);
    this.reason = verdict.reason;
    this.code = verdict.code;

    if (this.visible) {
      this.startCountdown();
      return;
    }
    // First failure: nothing shown yet. Wait out the grace window and let a
    // second failure decide. A manual press is the user asking to skip that.
    if (this.attempt < 2 && !manual) {
      this.timer = window.setTimeout(() => void this.attemptProbe(false), GRACE_MS);
      return;
    }
    this.visible = true;
    this.startCountdown();
  }

  private async probe(): Promise<Probe> {
    try {
      // healthz is unauthenticated, and this is a bare fetch rather than
      // http.ts's request() on purpose: request() invalidates the stored token
      // on a 401, which would turn a probe into a sign-out.
      const res = await fetch(apiUrl("healthz"), {
        signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
        cache: "no-store",
      });
      return { status: res.status };
    } catch (error) {
      return { error };
    }
  }

  private startCountdown() {
    this.phase = "waiting";
    this.countdown = RETRY_SECONDS;
    this.clearTimer();
    this.timer = window.setInterval(() => {
      if (this.countdown <= 1) {
        this.clearTimer();
        void this.attemptProbe(false);
        return;
      }
      this.countdown -= 1;
    }, 1000);
  }

  private recovered() {
    this.offline = false;
    this.hide();
  }

  private hide() {
    this.clearTimer();
    this.visible = false;
    this.reason = null;
    this.code = "";
    this.phase = "waiting";
    this.countdown = RETRY_SECONDS;
    this.attempt = 0;
  }

  private clearTimer() {
    if (this.timer === undefined) return;
    // One handle for both kinds of wait; clearing it as both is cheaper than
    // tracking which it currently is.
    window.clearTimeout(this.timer);
    window.clearInterval(this.timer);
    this.timer = undefined;
  }
}

export const reachability = new ReachabilityStore();
