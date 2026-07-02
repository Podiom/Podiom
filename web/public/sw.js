// Podiom service worker: renders OS-level Web Push notifications for
// attention-required agent events, and routes taps back to the right session.
//
// De-dup rule: if a dashboard window is currently visible, the in-app toast
// already covers the event, so we forward it to the page and skip the OS
// notification. When no window is visible (tab backgrounded or closed) we show
// the system notification — the whole point of Web Push.

self.addEventListener("install", () => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("push", (event) => {
  event.waitUntil(handlePush(event));
});

async function handlePush(event) {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch (_e) {
    data = {};
  }

  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  const visible = windows.find((c) => c.visibilityState === "visible");
  if (visible) {
    // A dashboard tab is in front; let the in-app toast handle it.
    visible.postMessage({ type: "push-preview", session_id: data.session_id, kind: data.kind });
    return;
  }

  const actions = data.kind === "permission" && data.approval?.request_id
    ? [{ action: "approve", title: "Approve" }]
    : [];

  await self.registration.showNotification(data.title || "Podiom", {
    body: data.body || "",
    tag: data.session_id || "podiom",
    renotify: true,
    icon: "/favicon.svg",
    badge: "/favicon.svg",
    actions,
    data,
  });
}

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const data = event.notification.data || {};
  if (event.action === "approve") {
    event.waitUntil(approvePermission(data));
    return;
  }
  event.waitUntil(focusSession(data));
});

async function focusSession(data) {
  const sessionId = data.session_id;
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  for (const client of windows) {
    if ("focus" in client) {
      await client.focus();
      client.postMessage({ type: "notification-click", session_id: sessionId });
      return;
    }
  }
  if (self.clients.openWindow) {
    await self.clients.openWindow("/");
  }
}

async function approvePermission(data) {
  const approval = data.approval || {};
  if (!approval.request_id) {
    await focusSession(data);
    return;
  }
  const res = await fetch(`/api/permission-decisions/${encodeURIComponent(approval.request_id)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      behavior: "allow",
      updatedInput: approval.input || {},
    }),
  });
  if (!res.ok) {
    await focusSession(data);
  }
}
