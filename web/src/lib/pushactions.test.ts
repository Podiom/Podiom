import { beforeEach, describe, expect, it, vi } from "vitest";

// The Capacitor plugin and the platform keystore are native; stubbed so the routing logic —
// which is the part with silent failure modes — can be tested on its own.
const listeners: Record<string, (event: unknown) => void> = {};
vi.mock("@capacitor-firebase/messaging", () => ({
  FirebaseMessaging: {
    addListener: vi.fn(async (name: string, fn: (event: unknown) => void) => {
      listeners[name] = fn;
      return { remove: vi.fn() };
    }),
    getToken: vi.fn(async () => ({ token: "tok" })),
    requestPermissions: vi.fn(async () => ({ receive: "granted" })),
    checkPermissions: vi.fn(async () => ({ receive: "granted" })),
    deleteToken: vi.fn(async () => {}),
  },
}));

const secure: Record<string, string> = {};
vi.mock("./native", () => ({
  isNative: true,
  platform: "ios",
  secureGet: vi.fn(async (key: string) => secure[key] ?? null),
  secureSet: vi.fn(async (key: string, value: string) => {
    secure[key] = value;
  }),
  secureForget: vi.fn(async (key: string) => {
    delete secure[key];
  }),
}));

vi.mock("./api", () => ({
  registerNotificationDevice: vi.fn(async () => ({
    device: {},
    installation_id: "install-1",
  })),
  deleteNotificationDevice: vi.fn(async () => {}),
}));

const { handleNotificationInteraction, startNativePush } = await import("./push");

// payload is what the relay delivers: flat, snake_cased, with the routing ids in data.
function payload(extra: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    notification_id: "not-1",
    type: "goal.action_requested",
    title: "Alice needs your help",
    nav_target: "goal_action_item",
    action_set: "goal_action_item",
    goal_id: "goal-1",
    resource_id: "item-1",
    installation_id: "install-1",
    ...extra,
  };
}

describe("notification interaction routing", () => {
  let navigated: unknown[];
  let actions: Array<[string, string]>;
  let actionResult: boolean;

  beforeEach(() => {
    navigated = [];
    actions = [];
    actionResult = true;
    for (const key of Object.keys(secure)) delete secure[key];
    secure["notification-installation-id"] = "install-1";
    startNativePush({
      onNavigate: (target) => navigated.push(target),
      onForeground: () => {},
      onAction: async (id, actionID) => {
        actions.push([id, actionID]);
        return actionResult;
      },
    });
  });

  it("navigates on a plain tap", async () => {
    await handleNotificationInteraction("tap", payload());
    expect(actions).toHaveLength(0);
    expect(navigated).toEqual([
      { kind: "goal", goalId: "goal-1", focus: { kind: "action", id: "item-1" } },
    ]);
  });

  // Swiping a notification away is not a decision about what it asked. The notification is
  // still in the Center, and acting on a dismissal would answer for the user.
  it("does nothing on a dismissal", async () => {
    await handleNotificationInteraction("dismiss", payload());
    expect(actions).toHaveLength(0);
    expect(navigated).toHaveLength(0);
  });

  // These two are navigation, so they must not reach the daemon as operations.
  it.each(["open", "review"])("navigates for the %s button", async (actionID) => {
    await handleNotificationInteraction(actionID, payload());
    expect(actions).toHaveLength(0);
    expect(navigated).toHaveLength(1);
  });

  it.each(["allow", "deny", "approve", "done", "blocked", "mark_done", "answer:1"])(
    "performs the %s action against the daemon",
    async (actionID) => {
      await handleNotificationInteraction(actionID, payload());
      expect(actions).toEqual([["not-1", actionID]]);
      // A successful action needs no navigation: the thing it asked about is settled.
      expect(navigated).toHaveLength(0);
    },
  );

  // The important failure case. A push reaches the phone through the relay, which it can
  // reach from anywhere; the daemon may be on a network it cannot. A button press that
  // quietly failed is worse than one that hands the decision back.
  it("opens the app when the action does not go through", async () => {
    actionResult = false;
    await handleNotificationInteraction("done", payload());
    expect(actions).toEqual([["not-1", "done"]]);
    expect(navigated).toHaveLength(1);
  });

  // An action must reach the daemon that produced the notification — the relay is never in
  // the return path — so a notification from another installation opens the app instead of
  // being answered against the wrong one.
  it("does not act on a notification from another installation", async () => {
    await handleNotificationInteraction("approve", payload({ installation_id: "install-other" }));
    expect(actions).toHaveLength(0);
    expect(navigated).toHaveLength(1);
  });

  it.each([
    ["the payload names no installation", { installation_id: undefined }],
    ["this app has not registered one", {}],
  ])("acts anyway when %s", async (label, extra) => {
    if (label === "this app has not registered one") {
      delete secure["notification-installation-id"];
    }
    await handleNotificationInteraction("done", payload(extra));
    expect(actions).toHaveLength(1);
  });

  // Without a notification id there is nothing to act on, so the app opens instead of
  // sending a request that cannot be addressed.
  it("opens the app when the payload has no notification id", async () => {
    await handleNotificationInteraction("done", payload({ notification_id: undefined }));
    expect(actions).toHaveLength(0);
    expect(navigated).toHaveLength(1);
  });

  // The plugin hands over whatever JSON arrived, so a malformed payload must route rather
  // than throw.
  it("tolerates a payload with nothing usable in it", async () => {
    await expect(handleNotificationInteraction("tap", {})).resolves.toBeUndefined();
    expect(navigated).toEqual([{ kind: "route", route: "chat" }]);
  });
});

describe("plugin listeners", () => {
  // The action a user pressed has to reach the routing above from the plugin's own event,
  // including when the press is what launched the app.
  it("routes a pressed action from the plugin event", async () => {
    const actions: Array<[string, string]> = [];
    startNativePush({
      onNavigate: () => {},
      onForeground: () => {},
      onAction: async (id, actionID) => {
        actions.push([id, actionID]);
        return true;
      },
    });
    secure["notification-installation-id"] = "install-1";

    listeners.notificationActionPerformed?.({
      actionId: "done",
      notification: { data: payload() },
    });
    await vi.waitFor(() => expect(actions).toEqual([["not-1", "done"]]));
  });
});
