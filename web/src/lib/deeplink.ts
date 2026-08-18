// Deep links: the mapping between a URL hash and a place in Podiom.
//
// Notifications name a destination as a logical token plus some ids — never a URL —
// so this module is the one place that decides what a route looks like. Renaming a
// route therefore cannot break a notification already sitting on someone's phone.
//
// The hash is used rather than the path because it never reaches the server. That
// matters in all three places Podiom runs: a Capacitor WebView serving from
// capacitor://localhost or https://localhost, and a browser behind a Home Assistant
// ingress sub-path. None of them can route a real URL path, and all of them handle a
// fragment identically.
//
// One trap comes with that. Under HA ingress the daemon injects a <base href>, and a
// bare <a href="#/goals"> resolves against the base rather than the current
// document, which is fragile. Always navigate by assigning location.hash — see
// hrefFor if a real anchor is unavoidable.

import type { Notification } from "./types";

// Route is the set of top-level pages. It mirrors App.svelte's own Route union.
export type Route =
  | "chat"
  | "roadmap"
  | "goals"
  | "projects"
  | "schedules"
  | "skills"
  | "terminal"
  | "settings";

const ROUTES: Route[] = [
  "chat",
  "roadmap",
  "goals",
  "projects",
  "schedules",
  "skills",
  "terminal",
  "settings",
];

// GoalFocus is what to bring forward within a goal. The plain kinds scroll a section
// into view; the identified kinds single out one action item, question, or access
// request so a notification lands on the exact thing it was about.
export type GoalFocus =
  | { kind: "timeline" }
  | { kind: "completion" }
  | { kind: "recovery" }
  | { kind: "action"; id: string }
  | { kind: "question"; id: string }
  | { kind: "access"; id: string };

// Target is a resolved destination.
export type Target =
  | { kind: "route"; route: Route }
  | { kind: "chat"; sessionId: string; permission?: boolean }
  | { kind: "goal"; goalId: string; focus?: GoalFocus }
  | { kind: "schedule"; name: string; runId?: string }
  | { kind: "task"; taskId: string }
  | { kind: "settings"; tab?: string };

// routeOf reports which top-level page a target belongs to, which is what the shell
// switches on.
export function routeOf(target: Target): Route {
  switch (target.kind) {
    case "route":
      return target.route;
    case "chat":
      return "chat";
    case "goal":
      return "goals";
    case "schedule":
      return "schedules";
    case "task":
      return "roadmap";
    case "settings":
      return "settings";
  }
}

// formatHash renders a target as a hash, including the leading "#".
export function formatHash(target: Target): string {
  return "#" + pathOf(target);
}

// hrefFor builds an absolute href for a target, for the rare case an anchor element
// is genuinely needed. It resolves against the document's own URL rather than any
// injected <base>, so it stays correct under a Home Assistant ingress sub-path.
export function hrefFor(target: Target, currentHref = documentHref()): string {
  const url = new URL(currentHref);
  url.hash = pathOf(target);
  return url.toString();
}

function documentHref(): string {
  if (typeof document !== "undefined") return document.location.href;
  return "http://localhost/";
}

function pathOf(target: Target): string {
  switch (target.kind) {
    case "route":
      return `/${target.route}`;
    case "chat": {
      const base = `/chat/${enc(target.sessionId)}`;
      return target.permission ? `${base}/permission` : base;
    }
    case "goal":
      return `/goals/${enc(target.goalId)}${goalFocusPath(target.focus)}`;
    case "schedule": {
      const base = `/schedules/${enc(target.name)}`;
      return target.runId ? `${base}/runs/${enc(target.runId)}` : base;
    }
    case "task":
      return `/roadmap/tasks/${enc(target.taskId)}`;
    case "settings":
      return target.tab ? `/settings/${enc(target.tab)}` : "/settings";
  }
}

function goalFocusPath(focus?: GoalFocus): string {
  if (!focus) return "";
  switch (focus.kind) {
    case "timeline":
    case "completion":
    case "recovery":
      return `/${focus.kind}`;
    case "action":
      return `/actions/${enc(focus.id)}`;
    case "question":
      return `/questions/${enc(focus.id)}`;
    case "access":
      return `/access/${enc(focus.id)}`;
  }
}

// parseHash resolves a hash back to a target, or null when it names nothing Podiom
// knows. Callers fall back to the default page rather than showing a blank screen: a
// stale or hand-edited link should be harmless.
export function parseHash(hash: string): Target | null {
  const segments = (hash.startsWith("#") ? hash.slice(1) : hash)
    .split("/")
    .map((segment) => segment.trim())
    .filter((segment) => segment !== "")
    .map(dec);
  if (segments.length === 0) return null;

  const [head, ...rest] = segments;
  switch (head) {
    case "chat":
      if (rest.length === 0) return { kind: "route", route: "chat" };
      if (rest.length === 1) return { kind: "chat", sessionId: rest[0] };
      if (rest.length === 2 && rest[1] === "permission") {
        return { kind: "chat", sessionId: rest[0], permission: true };
      }
      return null;
    case "goals": {
      if (rest.length === 0) return { kind: "route", route: "goals" };
      const focus = parseGoalFocus(rest.slice(1));
      if (focus === null) return null;
      return { kind: "goal", goalId: rest[0], ...(focus.focus ? { focus: focus.focus } : {}) };
    }
    case "schedules":
      if (rest.length === 0) return { kind: "route", route: "schedules" };
      if (rest.length === 1) return { kind: "schedule", name: rest[0] };
      if (rest.length === 3 && rest[1] === "runs") {
        return { kind: "schedule", name: rest[0], runId: rest[2] };
      }
      return null;
    case "roadmap":
      if (rest.length === 0) return { kind: "route", route: "roadmap" };
      if (rest.length === 2 && rest[0] === "tasks") return { kind: "task", taskId: rest[1] };
      return null;
    case "settings":
      if (rest.length === 0) return { kind: "settings" };
      if (rest.length === 1) return { kind: "settings", tab: rest[0] };
      return null;
    default:
      if (rest.length === 0 && (ROUTES as string[]).includes(head)) {
        return { kind: "route", route: head as Route };
      }
      return null;
  }
}

// parseGoalFocus resolves the segments after a goal id.
//
// It returns a wrapper rather than a bare GoalFocus so "no focus" and "not something
// we recognise" stay distinguishable: the first is a valid link to the goal itself,
// the second has to fail the whole parse.
function parseGoalFocus(segments: string[]): { focus?: GoalFocus } | null {
  if (segments.length === 0) return {};
  if (segments.length === 1) {
    const [only] = segments;
    if (only === "timeline" || only === "completion" || only === "recovery") {
      return { focus: { kind: only } };
    }
    return null;
  }
  if (segments.length === 2) {
    const [section, id] = segments;
    if (id === "") return null;
    switch (section) {
      case "actions":
        return { focus: { kind: "action", id } };
      case "questions":
        return { focus: { kind: "question", id } };
      case "access":
        return { focus: { kind: "access", id } };
    }
  }
  return null;
}

// enc/dec keep ids and schedule names safe in a hash. Schedule names are
// user-authored file names and can contain spaces and other characters that would
// otherwise split a segment.
function enc(value: string): string {
  return encodeURIComponent(value);
}

function dec(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    // A hand-mangled escape sequence should not throw on the way to a fallback.
    return value;
  }
}

// Navigation target tokens, mirroring internal/notify/registry.go. The server sends
// one of these plus the relevant ids; the mapping to a route lives here.
const NAV_SESSION = "session";
const NAV_SESSION_PERMISSION = "session_permission";
const NAV_GOAL = "goal";
const NAV_GOAL_TIMELINE = "goal_timeline";
const NAV_GOAL_ACTION_ITEM = "goal_action_item";
const NAV_GOAL_QUESTION = "goal_question";
const NAV_GOAL_ACCESS = "goal_access_request";
const NAV_GOAL_COMPLETION = "goal_completion";
const NAV_GOAL_RECOVERY = "goal_recovery";
const NAV_SCHEDULE_RUN = "schedule_run";
const NAV_TASK = "task";

// scheduleFilePrefix marks a resource id that names a schedule file rather than a
// run. System warnings about an unreadable schedule use it, because there is no run
// to navigate to — the file never got far enough to produce one.
const scheduleFilePrefix = "schedule-file:";

// targetFromNotification decides where tapping a notification should land.
//
// It degrades rather than failing: a notification whose target cannot be resolved
// still opens the most relevant page it can, because a tap that does nothing is worse
// than a tap that lands one level out.
export function targetFromNotification(n: Notification): Target {
  switch (n.NavTarget) {
    case NAV_SESSION:
      return n.SessionID ? { kind: "chat", sessionId: n.SessionID } : { kind: "route", route: "chat" };

    case NAV_SESSION_PERMISSION:
      return n.SessionID
        ? { kind: "chat", sessionId: n.SessionID, permission: true }
        : { kind: "route", route: "chat" };

    case NAV_GOAL:
      return goalTarget(n);

    case NAV_GOAL_TIMELINE:
      return goalTarget(n, { kind: "timeline" });

    case NAV_GOAL_COMPLETION:
      return goalTarget(n, { kind: "completion" });

    case NAV_GOAL_RECOVERY:
      return goalTarget(n, { kind: "recovery" });

    case NAV_GOAL_ACTION_ITEM:
      return goalTarget(n, n.ResourceID ? { kind: "action", id: n.ResourceID } : undefined);

    case NAV_GOAL_ACCESS:
      return goalTarget(n, n.ResourceID ? { kind: "access", id: n.ResourceID } : undefined);

    case NAV_GOAL_QUESTION:
      // A schedule that asks a question reuses this token but has no goal: the
      // question belongs to the scheduled run, so it routes to the schedule instead.
      if (!n.GoalID && n.ScheduleName) return { kind: "schedule", name: n.ScheduleName };
      return goalTarget(n, n.ResourceID ? { kind: "question", id: n.ResourceID } : undefined);

    case NAV_SCHEDULE_RUN: {
      // A finished run's most useful destination is the durable session it created —
      // that is where its transcript and its error are — and the notification carries
      // that session id. Landing on the schedules list would show the schedule rather
      // than what the run actually did.
      if (n.SessionID) return { kind: "chat", sessionId: n.SessionID };
      if (!n.ScheduleName) return { kind: "route", route: "schedules" };
      // A run that has only started has no session yet, and a warning about an
      // unreadable schedule file never produced one, so both land on the schedule.
      if (!n.ResourceID || n.ResourceID.startsWith(scheduleFilePrefix)) {
        return { kind: "schedule", name: n.ScheduleName };
      }
      return { kind: "schedule", name: n.ScheduleName, runId: n.ResourceID };
    }

    case NAV_TASK:
      return n.TaskID ? { kind: "task", taskId: n.TaskID } : { kind: "route", route: "roadmap" };
  }

  // An unknown token means the daemon is newer than this client. Fall back to the
  // most specific thing the notification does name.
  if (n.GoalID) return goalTarget(n);
  if (n.SessionID) return { kind: "chat", sessionId: n.SessionID };
  if (n.TaskID) return { kind: "task", taskId: n.TaskID };
  if (n.ScheduleName) return { kind: "schedule", name: n.ScheduleName };
  return { kind: "route", route: "chat" };
}

function goalTarget(n: Notification, focus?: GoalFocus): Target {
  if (!n.GoalID) return { kind: "route", route: "goals" };
  return focus ? { kind: "goal", goalId: n.GoalID, focus } : { kind: "goal", goalId: n.GoalID };
}
