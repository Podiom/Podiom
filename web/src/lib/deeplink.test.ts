import { describe, expect, it } from "vitest";

import {
  formatHash,
  hrefFor,
  parseHash,
  routeOf,
  targetFromNotification,
  type Target,
} from "./deeplink";
import type { Notification } from "./types";

// notification builds a minimal notification with the fields a target depends on.
function notification(fields: Partial<Notification>): Notification {
  return {
    ID: "not-1",
    Type: "goal.action_requested",
    Category: "goals",
    Importance: "important",
    Title: "Alice needs your help",
    Body: "",
    AgentName: "Alice",
    SessionID: "",
    GoalID: "",
    ScheduleName: "",
    TaskID: "",
    ResourceKind: "",
    ResourceID: "",
    NavTarget: "",
    Actionable: false,
    CreatedAt: "2026-08-18 07:00:00",
    ReadAt: "",
    ResolvedAt: "",
    ...fields,
  };
}

describe("formatHash and parseHash", () => {
  // Every target must survive a round trip, because a notification's destination is
  // formatted on one side and parsed on the other. A shape that formats but does not
  // parse would send a tap to the fallback page.
  const targets: Array<[string, Target]> = [
    ["top-level route", { kind: "route", route: "goals" }],
    ["chat session", { kind: "chat", sessionId: "sess-1" }],
    ["chat permission", { kind: "chat", sessionId: "sess-1", permission: true }],
    ["goal", { kind: "goal", goalId: "goal-1" }],
    ["goal timeline", { kind: "goal", goalId: "goal-1", focus: { kind: "timeline" } }],
    ["goal completion", { kind: "goal", goalId: "goal-1", focus: { kind: "completion" } }],
    ["goal recovery", { kind: "goal", goalId: "goal-1", focus: { kind: "recovery" } }],
    ["goal action item", { kind: "goal", goalId: "goal-1", focus: { kind: "action", id: "item-1" } }],
    ["goal question", { kind: "goal", goalId: "goal-1", focus: { kind: "question", id: "q-1" } }],
    ["goal access request", { kind: "goal", goalId: "goal-1", focus: { kind: "access", id: "req-1" } }],
    ["schedule", { kind: "schedule", name: "repo-health" }],
    ["schedule run", { kind: "schedule", name: "repo-health", runId: "run-1" }],
    ["task", { kind: "task", taskId: "task-1" }],
    ["settings", { kind: "settings" }],
    ["settings tab", { kind: "settings", tab: "notifications" }],
  ];

  it.each(targets)("round-trips a %s", (_name, target) => {
    expect(parseHash(formatHash(target))).toEqual(target);
  });

  it("produces hashes, never paths", () => {
    for (const [, target] of targets) {
      expect(formatHash(target).startsWith("#/")).toBe(true);
    }
  });

  // Schedule names are user-authored file names, so they can contain characters that
  // would otherwise split a hash segment or end the fragment.
  it.each([
    "weekly report",
    "repo/health",
    "release #1",
    "ünïcode",
    "a?b",
  ])("round-trips the schedule name %j", (name) => {
    const target: Target = { kind: "schedule", name };
    const hash = formatHash(target);
    expect(hash).not.toContain(" ");
    expect(parseHash(hash)).toEqual(target);
  });

  it.each([
    ["", "empty"],
    ["#", "bare hash"],
    ["#/", "slash only"],
    ["#/nonsense", "unknown page"],
    ["#/chat/sess-1/nonsense", "unknown chat sub-path"],
    ["#/goals/goal-1/nonsense", "unknown goal focus"],
    ["#/goals/goal-1/actions", "action without an id"],
    ["#/schedules/name/runs", "run without an id"],
    ["#/roadmap/tasks", "task without an id"],
    ["#/settings/a/b", "too many settings segments"],
  ])("returns null for %j (%s)", (hash) => {
    expect(parseHash(hash)).toBeNull();
  });

  // A hand-mangled escape must not throw on the way to the fallback.
  it("does not throw on a malformed escape", () => {
    expect(() => parseHash("#/goals/%E0%A4%A")).not.toThrow();
  });
});

describe("routeOf", () => {
  it.each([
    [{ kind: "chat", sessionId: "s" } as Target, "chat"],
    [{ kind: "goal", goalId: "g" } as Target, "goals"],
    [{ kind: "schedule", name: "n" } as Target, "schedules"],
    [{ kind: "task", taskId: "t" } as Target, "roadmap"],
    [{ kind: "settings" } as Target, "settings"],
    [{ kind: "route", route: "skills" } as Target, "skills"],
  ])("maps %j to its page", (target, want) => {
    expect(routeOf(target)).toBe(want);
  });
});

describe("hrefFor", () => {
  // Under a Home Assistant ingress the daemon injects a <base href>, and a bare
  // "#/goals" anchor resolves against that base rather than the current document.
  // hrefFor builds the URL from the document itself so an anchor stays correct.
  it("keeps an ingress sub-path", () => {
    const href = hrefFor(
      { kind: "goal", goalId: "goal-1" },
      "http://homeassistant.local:8123/api/hassio_ingress/abc123/",
    );
    expect(href).toBe("http://homeassistant.local:8123/api/hassio_ingress/abc123/#/goals/goal-1");
  });

  it("replaces an existing hash rather than appending to it", () => {
    const href = hrefFor({ kind: "route", route: "chat" }, "http://localhost:8787/#/goals/goal-1");
    expect(href).toBe("http://localhost:8787/#/chat");
  });
});

describe("targetFromNotification", () => {
  it("routes a session question to its chat", () => {
    const target = targetFromNotification(
      notification({ NavTarget: "session", SessionID: "sess-1" }),
    );
    expect(target).toEqual({ kind: "chat", sessionId: "sess-1" });
  });

  it("routes a permission request to the session's permission prompt", () => {
    const target = targetFromNotification(
      notification({ NavTarget: "session_permission", SessionID: "sess-1" }),
    );
    expect(target).toEqual({ kind: "chat", sessionId: "sess-1", permission: true });
  });

  it.each([
    ["goal", undefined],
    ["goal_timeline", { kind: "timeline" }],
    ["goal_completion", { kind: "completion" }],
    ["goal_recovery", { kind: "recovery" }],
  ])("routes %s to the goal", (navTarget, focus) => {
    const target = targetFromNotification(notification({ NavTarget: navTarget, GoalID: "goal-1" }));
    expect(target).toEqual(focus ? { kind: "goal", goalId: "goal-1", focus } : { kind: "goal", goalId: "goal-1" });
  });

  // The identified focuses single out the exact thing the notification was about, so
  // the resource id has to reach the target.
  it.each([
    ["goal_action_item", "action"],
    ["goal_question", "question"],
    ["goal_access_request", "access"],
  ])("routes %s to the specific resource", (navTarget, focusKind) => {
    const target = targetFromNotification(
      notification({ NavTarget: navTarget, GoalID: "goal-1", ResourceID: "res-1" }),
    );
    expect(target).toEqual({
      kind: "goal",
      goalId: "goal-1",
      focus: { kind: focusKind, id: "res-1" },
    });
  });

  // A schedule that asks a question reuses the goal-question token but has no goal.
  it("routes a schedule's question to the schedule", () => {
    const target = targetFromNotification(
      notification({
        Type: "schedule.question",
        NavTarget: "goal_question",
        ScheduleName: "repo-health",
        ResourceID: "q-1",
      }),
    );
    expect(target).toEqual({ kind: "schedule", name: "repo-health" });
  });

  // A finished run's transcript lives in the session it created, which is what the
  // requirement asks a schedule notification to open.
  it("routes a finished schedule run to the session it created", () => {
    const target = targetFromNotification(
      notification({
        Type: "schedule.succeeded",
        NavTarget: "schedule_run",
        ScheduleName: "repo-health",
        ResourceID: "run-1",
        SessionID: "sess-9",
      }),
    );
    expect(target).toEqual({ kind: "chat", sessionId: "sess-9" });
  });

  // A run that has only started has no session yet: the id is written when it
  // finishes, so there is nothing to open but the schedule.
  it("routes a started schedule run to the schedule", () => {
    const target = targetFromNotification(
      notification({
        Type: "schedule.started",
        NavTarget: "schedule_run",
        ScheduleName: "repo-health",
        ResourceID: "run-1",
      }),
    );
    expect(target).toEqual({ kind: "schedule", name: "repo-health", runId: "run-1" });
  });

  // A system warning about an unreadable schedule names the file, not a run — the
  // file never got far enough to produce one.
  it("routes a schedule-file warning to the schedule, not a run", () => {
    const target = targetFromNotification(
      notification({
        Type: "system.warning",
        NavTarget: "schedule_run",
        ScheduleName: "broken",
        ResourceID: "schedule-file:broken",
      }),
    );
    expect(target).toEqual({ kind: "schedule", name: "broken" });
  });

  it("routes a task notification to the roadmap task", () => {
    const target = targetFromNotification(
      notification({ NavTarget: "task", TaskID: "task-1" }),
    );
    expect(target).toEqual({ kind: "task", taskId: "task-1" });
  });

  // A daemon newer than the client can send a token this build has never heard of. A
  // tap must still land somewhere sensible rather than doing nothing.
  it.each([
    [{ NavTarget: "invented_token", GoalID: "goal-1" }, { kind: "goal", goalId: "goal-1" }],
    [{ NavTarget: "invented_token", SessionID: "sess-1" }, { kind: "chat", sessionId: "sess-1" }],
    [{ NavTarget: "invented_token", TaskID: "task-1" }, { kind: "task", taskId: "task-1" }],
    [{ NavTarget: "invented_token", ScheduleName: "nightly" }, { kind: "schedule", name: "nightly" }],
    [{ NavTarget: "invented_token" }, { kind: "route", route: "chat" }],
  ])("falls back for an unknown token %j", (fields, want) => {
    expect(targetFromNotification(notification(fields))).toEqual(want);
  });

  // Missing ids are the other degradation: land on the section rather than nowhere.
  it.each([
    [{ NavTarget: "session" }, { kind: "route", route: "chat" }],
    [{ NavTarget: "goal_timeline" }, { kind: "route", route: "goals" }],
    [{ NavTarget: "task" }, { kind: "route", route: "roadmap" }],
    [{ NavTarget: "schedule_run" }, { kind: "route", route: "schedules" }],
  ])("falls back when the id is missing %j", (fields, want) => {
    expect(targetFromNotification(notification(fields))).toEqual(want);
  });

  // Every target a notification produces must be a hash this client can parse back.
  it("always produces a parseable hash", () => {
    const samples = [
      { NavTarget: "session", SessionID: "sess-1" },
      { NavTarget: "goal_action_item", GoalID: "goal-1", ResourceID: "item-1" },
      { NavTarget: "schedule_run", ScheduleName: "weekly report", ResourceID: "run-1" },
      { NavTarget: "task", TaskID: "task-1" },
      { NavTarget: "invented_token" },
    ];
    for (const fields of samples) {
      const target = targetFromNotification(notification(fields));
      expect(parseHash(formatHash(target))).toEqual(target);
    }
  });
});
