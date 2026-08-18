// Web Push payloads are a flatter, snake_cased shape than the notification rows the
// REST API serves — the service worker has to read them without any of the app's
// code, so they stay deliberately small and self-describing.
//
// This adapts one into the shape the routing layer understands, so a Web Push tap and
// a Notification Center tap resolve to a destination through exactly the same code
// rather than through two mappings that can disagree.

import type { Notification } from "./types";

// pushPayloadAsNotification fills in what routing needs and leaves the rest empty.
// Routing reads only ids and the navigation target, so the unused fields are blank
// rather than invented.
export function pushPayloadAsNotification(payload: Record<string, unknown>): Notification {
  return {
    ID: str(payload.notification_id),
    Type: str(payload.type),
    Category: "",
    Importance: "normal",
    Title: str(payload.title),
    Body: str(payload.body),
    AgentName: "",
    SessionID: str(payload.session_id),
    GoalID: str(payload.goal_id),
    ScheduleName: str(payload.schedule_name),
    TaskID: str(payload.task_id),
    ResourceKind: "",
    ResourceID: str(payload.resource_id),
    NavTarget: str(payload.nav_target),
    Actionable: false,
    CreatedAt: "",
    ReadAt: "",
    ResolvedAt: "",
  };
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}
