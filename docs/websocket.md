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
