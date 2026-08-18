# WebSocket Contract

Phase 3 adds a browser-native WebSocket endpoint at:

```text
GET /api/ws
```

The browser sends JSON client messages and receives JSON server messages. The
TypeScript mirror lives in `web/src/lib/types.ts`; the Go source of truth lives
in `internal/server/ws_contract.go`.

## Authentication

The handshake must carry the [gateway token](security.md#gateway-token).
Browsers cannot set headers on a WebSocket, so the token rides the
subprotocol list: the client offers `podiom.v1` plus `podiom-token.<token>`,
and the server validates before upgrading and echoes only `podiom.v1`.
Non-browser clients may instead send the `X-Podiom-Token` header (or
`Authorization: Bearer`). A handshake without a valid token is rejected
with `401`.

When the token is rotated, the daemon force-closes every live connection with
close code **`4401`** — the web UI treats that as "re-enter the token" (and
any other close as "reconnect"). The connection URL must be derived from the
page's own location (the app may live under a proxy sub-path, e.g. Home
Assistant Ingress), never hard-coded to the origin root.

## Client Messages

```json
{"type":"list"}
```

Refresh agents and sessions.

```json
{"type":"create_session","request_id":"...","agent_name":"jared"}
```

Create a web-origin session.

```json
{"type":"send_turn","request_id":"...","agent_name":"jared","message":"Hello"}
{"type":"send_turn","request_id":"...","session_id":"...","message":"Continue"}
```

Send a turn to a new or existing session.

An existing-session turn may reference draft photo uploads in display order:

```json
{
  "type": "send_turn",
  "request_id": "turn-1",
  "session_id": "4b8d…",
  "message": "What is shown here?",
  "attachment_ids": ["8a41…", "c237…"]
}
```

`message` may be empty when `attachment_ids` is non-empty. Attachment IDs must
be unique, unbound drafts owned by that session; at most four are accepted. A
slash-command turn cannot contain attachment IDs. The REST `POST /api/chat`
request accepts the same `attachment_ids` field.

Slash commands use the same `send_turn` envelope and return `notice`, `session`,
and `done` messages rather than provider deltas.

Web turns are daemon-owned. Closing or reconnecting the socket does not cancel
an active turn; the browser can reattach to the session:

```json
{"type":"attach_session","request_id":"...","session_id":"..."}
```

Explicit user cancellation is session-scoped:

```json
{"type":"stop_turn","request_id":"...","session_id":"..."}
```

Session settings can be changed without writing a slash command into chat
history:

```json
{"type":"update_session_settings","request_id":"...","session_id":"...","permission_mode":"yolo"}
```

```json
{
  "type": "permission_decision",
  "request_id": "<permission request id>",
  "decision": {"behavior":"allow","updatedInput":{}}
}
```

Answer an inline permission request. Denies use:

```json
{"behavior":"deny","message":"Denied from web"}
```

When an interactive turn reaches a provider session limit, the daemon blocks the
turn and emits a `fallback_request`. Resolve it with:

```json
{
  "type": "fallback_decision",
  "request_id": "<fallback request id>",
  "fallback_decision": {"action":"use_configured"}
}
```

`action` is either `use_configured` (advance the agent's configured fallback
chain) or `switch` (move to a specific target, with `provider` and `profile`
naming one of the request's offered `targets`). Either way the turn resumes on
the new target and the canonical history is replayed there. Non-interactive runs
(schedules, goals, dreams) never receive this prompt — they fall back
automatically along the configured chain.

## Server Messages

| Type | Payload |
| --- | --- |
| `hello` | Connection acknowledgement. |
| `state` | `agents`, `sessions`, `active_turns`. |
| `session` | Active/created session. |
| `history` | Ordered stored messages. |
| `message` | One stored user or assistant message, including `Attachments` on an attachment-bearing user message. `Kind` is `message`, `narration`, `reasoning`, or `error` (see [sessions](sessions.md#history)); a turn emits several as it persists its working notes. |
| `delta` | Incremental assistant text. |
| `assistant` | Final assistant text fallback. |
| `reasoning_delta` | Incremental provider thinking text, streamed separately from the answer. |
| `reasoning` | Completed provider thinking text, when the provider sends no deltas. |
| `permission_request` | Tool approval request. |
| `user_input_request` | Provider/user clarification request. |
| `fallback_request` | Session limit reached; the user must pick how to continue. Carries the rate-limited target, the configured next fallback, and the selectable `targets`. |
| `auth_required` | The turn's backing account is signed out. Carries `provider` and `profile` plus the provider's own wording, so the client can offer an inline sign-in rather than repeating "run /login". The turn ends; a transcript error row explains it after a reload. |
| `turn_state` | Current active-turn snapshot for a session. |
| `interview_state` | USER.md interview progress (`answered`, covered topics, status) and the server-rendered review draft when ready. |
| `notice` | Non-history UI notice, usually from slash commands. |
| `notification` | A notification was just recorded. Carries the full `notification` row. Broadcast whether or not any external channel is enabled — the Notification Center is where a notification lives, not a delivery destination. |
| `notification_update` | An existing notification's read or resolved state changed, including when acting on another device resolved it. Clients revise the row they already hold rather than announcing it again. |
| `notifications_read_all` | Every notification was marked read. Carries no payload: clients re-read the list, because a single call can touch hundreds of rows and sending each one would cost far more than a refresh. |
| `done` | Turn complete. |
| `error` | Error string. |

Session payloads include display metadata (`Name`, `Description`, `AutoNamed`)
and current settings (`Model`, `Effort`, `PermissionMode`). Permission requests
include `expires_at` when a timeout is active so clients can show an auto-deny
countdown.

An attachment-bearing stored message is representative of both `message` events
and entries in `history`:

```json
{
  "ID": 42,
  "SessionID": "4b8d…",
  "Seq": 3,
  "Role": "user",
  "Kind": "message",
  "Content": "What is shown here?",
  "Attachments": [{
    "ID": "8a41…",
    "SessionID": "4b8d…",
    "MessageID": 42,
    "Name": "garden.png",
    "MIMEType": "image/png",
    "SizeBytes": 381204,
    "Width": 1500,
    "Height": 2000,
    "CreatedAt": "2026-07-18 09:30:00"
  }]
}
```

Filesystem paths are deliberately absent from this payload.

### Notifications

A `notification` payload is the stored row. `NavTarget` is a logical token, not a
URL — the client owns the mapping from token plus ids to a route, so renaming a
route cannot break notifications already sitting on someone's phone:

```json
{
  "type": "notification",
  "notification": {
    "ID": "8f2c…",
    "Type": "goal.action_requested",
    "Category": "goals",
    "Importance": "important",
    "Title": "Alice needs your help",
    "Body": "Publish the release announcement.",
    "AgentName": "Alice",
    "GoalID": "4b8d…",
    "ResourceKind": "goal_action_item",
    "ResourceID": "1a7e…",
    "NavTarget": "goal_action_item",
    "Actionable": true,
    "CreatedAt": "2026-08-18 06:36:28",
    "ReadAt": "",
    "ResolvedAt": ""
  }
}
```

`Actionable` says the notification has operations beyond navigation; the
operations valid *right now* come from the REST endpoints below, not from the
broadcast, because they depend on domain state that keeps moving after the
notification was recorded.

`ReadAt` and `ResolvedAt` are separate lifecycles. Read means the user has seen
it; resolved means the underlying condition has been handled. Reading a
notification never resolves the domain object behind it.

USER.md interviews start with `start_interview`, use the same
`user_input_decision` response shape as chat questions, and can be reattached
with `attach_session`. If an interviewer ends before using its dedicated MCP
tools, the client sends `resume_interview`; the daemon permits one controlled
recovery turn. Interview assistant prose is never treated as a draft.

## REST Support

The web UI also uses REST for initial CRUD and history fetches:

| Endpoint | Purpose |
| --- | --- |
| `GET /api/agents` | List agents. |
| `POST /api/agents` | Create an agent. |
| `GET /api/agents/{name}` | Get one agent. |
| `GET /api/sessions` | List sessions. |
| `POST /api/sessions` | Create a session. |
| `GET /api/sessions/{id}` | Get one session and ordered history. |
| `POST /api/sessions/{id}/attachments` | Upload multipart `file` (original) and `visual` (normalized JPEG); returns draft attachment metadata. |
| `GET /api/attachments/{id}` | Retrieve the authenticated retained original. |
| `GET /api/attachments/{id}/thumbnail` | Retrieve the authenticated normalized JPEG preview. |
| `DELETE /api/attachments/{id}` | Delete an unbound draft upload. |
| `GET /api/notifications` | Notification Center page. Accepts `limit`, `offset`, `unread=1`, `unresolved=1`, `category`. Returns the rows with their currently valid `actions`, plus `unread` (counted across everything, not just the filtered view), `attention`, and `total`. `attention` counts only unread notifications the registry marks important or critical — that is what a badge should show, because counting every unread row leaves it permanently lit by routine progress and run activity. |
| `GET /api/notifications/{id}` | One notification and its currently valid actions. |
| `POST /api/notifications/{id}/read` | Mark seen. Guarded, so a repeat is `404` rather than a re-stamp that would reorder the list. |
| `POST /api/notifications/{id}/unread` | Return to unread. |
| `POST /api/notifications/{id}/resolve` | Mark the notification handled. Does not touch the domain object. |
| `POST /api/notifications/read-all` | Mark everything read. |
| `GET /api/notifications/preferences` | The whole preference model: categories, titles, labels, and each type's effective setting. Served so the settings screen has nothing hardcoded. |
| `GET /api/notification-devices` | Registered native-push devices, plus this installation's id. Push tokens are never returned. |
| `POST /api/notification-devices` | Register or refresh a device: `{device_id, platform, push_token, label?, app_version?}`. Idempotent on the Podiom device id, because the push token rotates while the device stays the same. Re-registering never changes the enabled flag, so the app cannot silently un-mute a device. |
| `POST /api/notification-devices/{id}/enable` | Resume delivery to one device. |
| `POST /api/notification-devices/{id}/disable` | Stop delivery to one device. This is registration state, separate from preferences: muting a device does not change which events matter. |
| `DELETE /api/notification-devices/{id}` | Remove a registration. |
| `POST /api/notifications/{id}/actions/{actionID}` | Perform one of a notification's actions. Dispatches to the same core operation the web UI uses. Returns `409` with `{status:"stale", reason, actions, resource:{kind,id,state}}` when the action is no longer valid — a notification can outlive the thing it was about. |
| `PUT /api/notifications/preferences` | `{"preferences":[{"type":…,"enabled":…}]}`. Unknown types are rejected as a whole, so a bad request is never half-applied. One update writes a row for every known channel, which is what keeps a switched-off type off when a new channel is added later. |

The older Phase 2 NDJSON `POST /api/chat` endpoint remains for the CLI.

Example upload:

```text
POST /api/sessions/4b8d…/attachments
Content-Type: multipart/form-data; boundary=…

file=<original JPEG/PNG/GIF/WebP>
visual=<JPEG, at most 2000 px on either edge>
```

The original and visual parts are each limited to 10 MiB; the complete request
is limited to 22 MiB. Uploads are drafts until a turn binds them atomically to
its new user message.
