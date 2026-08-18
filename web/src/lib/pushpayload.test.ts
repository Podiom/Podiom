import { describe, expect, it } from "vitest";

import { targetFromNotification } from "./deeplink";
import { pushPayloadAsNotification } from "./pushpayload";

describe("pushPayloadAsNotification", () => {
  // A Web Push tap and a Notification Center tap must reach the same destination.
  // Routing them through two mappings is exactly how they would drift apart.
  it("routes a goal action item to the same place as the API shape would", () => {
    const target = targetFromNotification(
      pushPayloadAsNotification({
        notification_id: "not-1",
        type: "goal.action_requested",
        title: "Alice needs your help",
        goal_id: "goal-1",
        resource_id: "item-1",
        nav_target: "goal_action_item",
        kind: "goal_action_item",
      }),
    );
    expect(target).toEqual({
      kind: "goal",
      goalId: "goal-1",
      focus: { kind: "action", id: "item-1" },
    });
  });

  it("routes a finished schedule run to its session", () => {
    const target = targetFromNotification(
      pushPayloadAsNotification({
        nav_target: "schedule_run",
        schedule_name: "repo-health",
        session_id: "sess-9",
        resource_id: "run-1",
      }),
    );
    expect(target).toEqual({ kind: "chat", sessionId: "sess-9" });
  });

  // A payload from an older daemon carries only the legacy fields, so it must still
  // land somewhere sensible rather than on the fallback page.
  it("falls back to the goal when there is no routing target", () => {
    const target = targetFromNotification(
      pushPayloadAsNotification({ title: "Alice requests access", goal_id: "goal-1", kind: "goal_access_request" }),
    );
    expect(target).toEqual({ kind: "goal", goalId: "goal-1" });
  });

  it("tolerates a payload with nothing usable in it", () => {
    const target = targetFromNotification(pushPayloadAsNotification({}));
    expect(target).toEqual({ kind: "route", route: "chat" });
  });

  // The service worker hands over whatever JSON arrived, so non-string values must not
  // become "undefined" or throw on the way to a route.
  it("ignores values that are not strings", () => {
    const notification = pushPayloadAsNotification({
      goal_id: 42,
      session_id: null,
      nav_target: { nested: true },
      task_id: undefined,
    });
    expect(notification.GoalID).toBe("");
    expect(notification.SessionID).toBe("");
    expect(notification.NavTarget).toBe("");
    expect(() => targetFromNotification(notification)).not.toThrow();
  });
});
