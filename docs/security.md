# Security & logging

This page documents Podiom's security posture and the structured run logging it
emits. It reflects the implementation as shipped in v1 (requirements §8.6, §10,
R11.5) and is the reference checked during the Phase 9 security review pass.

## Threat model in one line

Podiom is a **single-user, localhost daemon** that orchestrates agent CLIs which
already have access to the user's machine and accounts. Podiom does not add a
sandbox; it adds a **deliberate approval boundary** and keeps sensitive
configuration out of logs and client payloads.

## Permission modes

Every session and scheduled run carries a permission mode (R5.18–R5.21, §8.4):

| Mode | Meaning | How it is enforced |
| --- | --- | --- |
| `approve` *(default)* | Each tool call is relayed to a human who allows or denies it. | Claude: an MCP permission server (`--permission-prompt-tool`) → daemon broker → UI/CLI. Codex: `approvalPolicy: on-request` + `sandbox: read-only`, relayed through the same broker. |
| `auto` | File edits inside the session's working directory run unattended; everything else still asks. | Claude: `--permission-mode acceptEdits`, with the permission relay still wired for every non-edit tool. Codex: `approvalPolicy: on-request` + `sandbox: workspace-write`, scoped to the working directory alone. |
| `yolo` *(opt-in)* | Every tool call is auto-approved. | Claude: `--permission-mode bypassPermissions`. Codex: `approvalPolicy: never` + `sandbox: danger-full-access`. |

**`auto` is not the same shape on both providers**, and the difference is worth
knowing rather than papering over: under Codex, commands that stay inside the
sandbox run without asking; under Claude, edits are automatic but every `Bash`
call still reaches the relay. Claude's own classifier-driven `auto` mode would
have closed the gap, but measured against Claude Code 2.1.220 it is silently
downgraded to `default` in headless `-p` runs (as is `manual`), so `acceptEdits`
is the only value that actually takes effect.

**`auto`'s writable scope is deliberately narrow.** On Codex the writable scope
is governed by `runtimeWorkspaceRoots`, *not* by `sandboxPolicy.writableRoots` —
measured: holding `writableRoots` fixed, a directory listed in the runtime roots
was written with no approval request, and the same write was refused once it was
dropped from that list. Podiom's broad root set includes the projects parent
directory (so agents can read the shared ledger), so `auto` receives the working
directory alone; handing it the broad set would let one session write into every
project on disk. Reads are unaffected — also measured — so ledger access still
works. `approve` and `yolo` keep the broad set, where writes are respectively
impossible and unrestricted by design.

**On `--dangerously-skip-permissions`:** it and `--permission-mode
bypassPermissions` are exactly equivalent — both report
`permissionMode: bypassPermissions` in Claude's `system/init` event. Podiom uses
the latter because the former carries extra environment refusals (it is the flag
`--allow-dangerously-skip-permissions` exists to gate) for no behavioural gain.
Codex's `approvalPolicy: never` + `sandbox: danger-full-access` is the
programmatic equivalent of its `--dangerously-bypass-approvals-and-sandbox`.

Mode validity is enforced in Go (`config.KnownPermission`), not by a schema
constraint: migration 28 dropped the `permission_mode` CHECK for the same reason
migration 25 dropped the provider CHECKs — baking the list into the schema made
every new mode a migration.

**`yolo` is whole-machine access by design (R8.31).** Podiom does *not* pretend
the workspace is a sandbox in `yolo` — the only guard is the explicit opt-in and
the `approve` default. Because of this, Podiom surfaces an explicit warning every
time `yolo` is selected:

- CLI `podiom agents create … --permission yolo` prints a whole-machine warning.
- The `/permission yolo` slash command returns a notice spelling out that the
  workspace is not a sandbox and how to switch back.
- Switching an existing web chat to `yolo` requires a confirmation that the
  change grants whole-machine access and applies from the next turn.
- The web "Hire agent" modal labels the option `yolo · full access`.

In the Home Assistant image, "whole-machine" means everything reachable by the
dedicated non-root `podiom` account inside the add-on container. That includes
all persistent projects, credentials, SSH keys, and Podiom state, but not
root-owned system paths. Running Claude as non-root also allows its
`bypassPermissions` mode to support autonomous goal sessions without the CLI's
root/sudo refusal.

The mandatory `approve` auto-deny timeout (`global.permission_timeout`) ensures a
blocked permission prompt never hangs an agent indefinitely (R8.18): if no human
decision arrives in time, the broker returns *deny*.

### Unattended runs (scheduler / roadmap pickup / agent-initiated starts)

Scheduled fires, server-side task pickups, and agent-initiated task starts
(`podiom_start_task`, typically from a goal review) have no human to answer a
prompt (§7.7), so they never use the interactive relay. They run either:

- `yolo` — whole-machine, deliberate; or
- `preapproved` *(default, stricter)* — an allow-list. Claude enforces it
  natively via `--allowedTools`; Codex/fake consult the in-process
  `core.AllowListRelay`. **An empty allow-list denies everything.**

#### Goals

Goals are the deliberate `yolo` case, applied to a whole chain: a goal exists to
reach an outcome without the user, so its lead agent's planning/review sessions
**and** every roadmap task and schedule that carries the goal's `goal_id` run
`yolo` (forced at session creation and, for schedules, again at fire time so
older `preapproved` schedule files still run autonomously). Schedules a goal
creates are normalized to `run_permission: yolo` on disk, so the posture is
visible, not just enforced. This is disclosed in the UI (a full-access warning on
goal creation and a persistent badge on the goal) and audited: because `yolo`
tool calls never reach the permission broker, Podiom instead parses each tool
call out of the provider stream and records it as a `tool_use` goal event — the
goal's timeline is the audit counterweight to its full access. Those events store
the command text and file paths (so the user can see what ran) but truncate large
inputs and elide written file contents, consistent with the redaction rules
below.

## Gateway token

Every API call and WebSocket connection to `podiomd` must present the
**gateway token** — a cryptographically random secret generated automatically
on the daemon's first start and stored (mode `0600`) at
`$PODIOM_HOME/gateway.token`. On the normal web listener, only static assets,
the token-entry screen, `/healthz`, and schedule webhooks (below) are reachable
without it; nothing about sessions, agents, plans, or memory is exposed
pre-token.

How each client gets it:

- **The `podiom` CLI** reads it from disk automatically — same machine, same
  trust domain, zero friction.
- **Browsers** remember it locally after unlock. Standalone browsers enter it
  once in the web UI's token screen; get the value with `podiom token show`.
  Home Assistant browsers bootstrap it through the HA-authenticated setup flow
  after onboarding has completed.

Rotation (`podiom token rotate`, or the `rotate_token` toggle in the HA app)
invalidates the previous value immediately: live browser tabs are disconnected
with close code `4401` and unlock again, while the CLI picks it up from disk on
its next call.

Two rules keep the value contained: the daemon logs token *events* (generated,
rotated) but never the value, and the only unauthenticated browser token return
is the HA-only bootstrap endpoint after onboarding has completed.

### Home Assistant LAN listener

The Home Assistant image keeps two separate trust boundaries:

- Port `8099` is the complete web/Ingress surface. The source guard accepts the
  HA Ingress proxy and loopback only. The SPA, onboarding bootstrap, and web
  terminal exist only here and rely on the authenticated HA session in front.
- Port `8100` is an API-only listener for the native apps. Supervisor leaves its
  host mapping disabled by default. When the user opts in, it exposes only
  `/healthz`, `/api/*`, and `/api/ws` to private LAN sources (or the explicit
  `server.allow_from` list). It does not serve the SPA or terminal.

The LAN listener deliberately applies a stricter token policy than Ingress:
every API and WebSocket request requires the gateway token. The HA onboarding
bootstrap and schedule-webhook exemptions do not apply there. CORS preflight
may be answered without the token, but the request it authorizes may not.

The listener speaks plain HTTP. A gateway token grants control of the whole
Podiom daemon and therefore crosses the LAN in cleartext when this option is
used. Enable it only on a trusted local network; use TLS termination for any
broader network. Nabu Casa continues to protect the HA browser/Ingress path and
does not turn this listener into a remote native-app endpoint.

### Schedule webhooks

A schedule with `webhook: true` can be fired by a third-party service — a git
host, an automation step, a home controller. None of those can hold the gateway
token, and none of them should: it opens the whole daemon. So
`POST /api/schedules/<name>/webhook` is the one write endpoint exempt from the
token, and it is guarded instead by a **per-schedule secret**:

- 32 bytes of entropy, generated by Podiom when the trigger is created and
  compared in constant time. A schedule file that declares `webhook: true`
  without a secret fails to parse, so the endpoint is never open.
- Holding it can start exactly one schedule. It grants no read access — not to
  that schedule, not to its runs, not to anything else — and a leak is contained
  by turning the trigger off and back on, which retires the old secret and mints
  a new one.
- Rejections are uniform: a wrong secret, an unknown schedule, and a schedule
  with no webhook trigger all answer `401` with the same body, so the endpoint
  cannot be used to enumerate schedule names. The daemon logs which it actually
  was; the caller is not told.
- The rest of the schedule surface is unchanged. Reading, editing, deleting, and
  the manual `/run` trigger all still require the gateway token.
- A webhook run is an unattended run like any other: it obeys the schedule's own
  `run_permission`, and a goal-linked schedule still runs `yolo`. Whoever holds
  the secret can therefore start work at that schedule's privilege level — treat
  it accordingly, and prefer `preapproved` with a tight `allowed_tools` for a
  schedule exposed to the internet.
- The request body is passed to the agent as prompt text, capped at 8KB, and is
  never logged.
- The source-IP guard still applies. With `allow_from` set, or in HA mode, an
  outside sender is refused before it reaches the handler.

Why a token even when the UI sits behind authenticated proxies (HA Ingress):
defense in depth for the client→daemon hop, authenticated LAN exposure of
standalone and opt-in Home Assistant installs, and the auth primitive for the
[mobile apps](mobile.md) and the future remote mode.

### Cross-origin requests

The web UI is same-origin — `podiomd` serves it — so no CORS is offered to
browsers. The [mobile apps](mobile.md) are the exception: they load the same UI
from the app bundle and reach the daemon across the network, which makes every
request cross-origin.

`podiomd` therefore answers CORS preflights for exactly three origins, the fixed
set a Capacitor WebView can present: `capacitor://localhost` (iOS),
`https://localhost` and `http://localhost` (Android). No other origin gets CORS
headers.

This does not widen the browser-facing surface. None of those origins is
reachable as a page origin in an ordinary browser, credentialed mode is off (the
gateway token is an explicit header, never an ambient cookie), and CORS governs
only who may *read* a response — every `/api/` request still needs a valid token
and still passes the source-IP guard.

### Local network advertising

With `server.advertise` on (the default, and skipped on a loopback bind),
`podiomd` announces itself as `_podiom._tcp` over mDNS so the mobile apps can
find it. The announcement carries a hostname-derived label, the port and the
build version — deliberately nothing sensitive, since anything on the LAN can
read it. It grants no access: connecting still requires the gateway token. Set
`server.advertise: false` to stop announcing. Advertising remains disabled in
Home Assistant mode because the container cannot reliably advertise the host
port selected by the user; those instances are entered manually.

## Sensitive data handling

### MCP configuration & credentials (R8.29)

A generated MCP config may embed server commands, local URLs, tokens, or
credentials. Podiom treats `Agent.MCPConfig` as sensitive:

- It is tagged `json:"-"` on the store model, so it is **redacted at every JSON
  boundary** — both the REST API and the WebSocket `state` message — in one place.
  (`internal/store/redaction_test.go` locks this contract.)
- It is never written to a log line. The per-turn Claude MCP config file written
  into `workspace/.podiom/` is created `0600` and removed after the turn.

### System prompts / developer instructions (R8.30)

Composed agent instructions (the base `AGENTS.md` + per-agent `AGENTS.md` +
`SOUL.md`, delivered as Claude `CLAUDE.md` `@`-imports or a Codex bundle) are an
internal `[]byte` payload handed to the adapter. They are **never** placed in any
client DTO (`store.Agent`, `store.Session`, `store.Message`) and are never logged.

### Profile / auth isolation (R8.32, R8.34–R8.37)

A profile is *just a directory name*. Podiom maps it to the backing CLI's own
config dir via an environment variable and **never handles credentials**:

- Claude: `CLAUDE_CONFIG_DIR=<profile.config_dir>`
- Codex: `CODEX_HOME=<profile.home_dir>`

When no profile is set, Podiom **unsets** that variable so the CLI uses its normal
global login — it never leaks one profile's variable into another profile's
process. (Agent *workspaces* are intentionally shared across agents, §5.8; it is
only the auth state that stays isolated.) The variable name lives in the registry
(`ProviderInfo.ProfileEnvVar`) and the strip-then-set is one shared helper,
`exec.ProfileEnv`, used by the adapters, the login probe, and the sign-in flow.

Signing in from Settings (`POST /api/provider-login`) keeps that boundary: the
daemon runs the provider's own login CLI scoped to the profile directory, relays
only the authorization URL to the browser and the user's pasted code to the
CLI's stdin, and never reads or stores the resulting token. The code is rejected
if it contains a newline (which would inject a second answer into the CLI), is
never logged, and is never echoed back in a response. `/api/provider-login`
and `/api/provider-status` are human-only: no agent tool wraps them, so an agent
cannot authenticate on the user's behalf.

### The credentials store

Secrets granted to agents live in `credentials.yaml` under the storage root
(`0600`, atomic writes — the same trust model as `mcp.yaml`) and are injected
into agent CLI subprocess environments at spawn. A value never leaves the daemon:
`GET /api/credentials` serializes a view with no value field, log lines carry the
name only, and there is no endpoint anywhere that reads a value back.

There are three ways a value gets in, and all three land on the same validation:

- **You add one** on Settings → Credentials.
- **You approve an `env_var` access request** and type the value into the
  approval dialog. The request itself is rejected outright if it carries a value
  (`internal/core/goals.go`), so a secret never rides along in a stored request
  row, the goal timeline, or the model's own request text.
- **An agent stores one it already holds** with `podiom_store_credential`.

That third route is a deliberate relaxation of an earlier rule that agents must
never touch secrets. It is a net reduction in exposure rather than an increase:
the value was already in the agent's context — the user pasted it, or a CLI the
agent ran printed it — and before this existed the agent's only options were to
improvise a `.env`, a shell profile, or a scratch note that nothing manages,
rotates, or shows you. Four properties bound it:

- **Values only flow inward.** No tool, and no API, returns a stored value. An
  agent receives credentials the one way it always has: as environment variables
  in its own process.
- **Replacing needs an explicit flag.** Storing over an existing name is refused
  unless the caller passes `overwrite=true`, so an agent cannot silently clobber
  a token you entered. Rotation keeps the original purpose and goal link.
- **Every agent write is attributed.** `created_by_agent` / `created_by_session`
  are stamped by the MCP helper from its own launch flags, not from arguments the
  model chose, so the provenance shown on the Credentials page cannot be forged.
  Blank means you added it.
- **Deletion stays human-only.** `/api/credentials/` (the item route) carries no
  agent tool and stays on the `notManageable` list in
  `cmd/podiomd/manage_coverage_test.go`.

Standing instructions push agents toward the store and away from everywhere else:
check it before asking you for a token, put any secret received or generated into
it immediately, and never write a value into a shell profile, `MEMORY.md`, a
note, or the text of a task, schedule, progress entry, action item, or reply —
all of which Podiom stores and displays. Those rules are composed into every run
rather than kept only in the base `AGENTS.md`, which is written once at scaffold
time, so existing installations get them too.

### Voice-input OpenAI key

The [voice input](voice-input.md) feature stores its OpenAI Whisper key as
`voice.openai_api_key` in `config.yaml` — the one user-managed secret that
lives in the file, kept there deliberately so the Settings UI and the YAML
stay in sync. It is used server-side only: `GET /api/config` exposes just a
`key set` boolean, the key is never sent to a browser, and log lines record
presence, never the value. The `PODIOM_OPENAI_API_KEY` / `OPENAI_API_KEY`
environment variables override the file for setups that keep secrets out of
YAML entirely.

### Photo attachments

[Photo attachments](photo-attachments.md) are stored locally below
`$PODIOM_HOME/attachments/<session-id>/<attachment-id>/`. Each directory uses
daemon-generated IDs and contains a server-named original plus `visual.jpg`;
the client filename is display metadata only, reduced to a basename and never
used to construct a path. Filesystem paths are omitted from REST and WebSocket
attachment objects.

Upload handling detects the original MIME signature instead of trusting request
headers, permits only JPEG/PNG/GIF/WebP, limits each original and normalized part
to 10 MiB, and requires the normalized visual to be a decodable JPEG no larger
than 2000 px on either edge. The browser creates that JPEG at quality 0.85. This
re-encoding strips embedded metadata before provider delivery, but the retained
original is intentionally unchanged and can still contain EXIF location, camera,
or author data.

Original and thumbnail retrieval uses the normal gateway-token authentication
and `nosniff`/private/no-store response headers. Attachment IDs must belong to
the target session and can be bound only once. Deleting a session removes its
live files; archives and `$PODIOM_HOME` backups retain copies by design. Unbound
uploads older than 24 hours and filesystem orphans are cleaned at startup and
daily.

### GitHub project repo tokens

Podiom's GitHub project integration is local-first and does not ship a GitHub App
private key or client secret. The distributed app contains only public GitHub App
details (`app_slug`, `client_id`). Users authorize the app with GitHub's device
flow, and Podiom stores the returned local token under
`$PODIOM_HOME/github/token.json` with `0600` permissions.

GitHub tokens, temporary archive redirect URLs, and downloaded archive URLs are
treated as sensitive and must not be logged or returned from API responses.
GitHub-created projects attempt a real clone with the user's own Git credentials
and store only a clean remote URL. The GitHub App token remains confined to API
and archive requests and is never passed to Git or embedded in repository
configuration. If the clone fails, Podiom downloads the existing archive
snapshot fallback instead. Podiom's automatic sync path is fetch plus
fast-forward only; it does not create commits, push, reset, rebase, or stash.

## Structured run logging (R11.5)

`podiomd` logs structured records (Go `slog`, text handler) for both
**interactive** and **scheduled** runs, so every agent run is auditable. Logs are
written to stderr and `$PODIOM_HOME/logs/podiomd.log` (default
`~/.podiom/logs/podiomd.log`), rotate daily, and keep `logging.retention_days`
calendar days (default 7).

Interactive turns (`internal/core`, tagged `event=run`):

| Message | Emitted when | Key fields |
| --- | --- | --- |
| `turn started` | a turn begins | `session`, `agent`, `origin`, `unattended`, `provider`, `profile`, `permission` |
| `turn fallback` | a rate limit steps the fallback chain | `from`, `to` |
| `turn finished` | the assistant reply is persisted | `provider`, `reply_bytes` |
| `turn aborted` | the client/stream cancelled mid-turn | `provider` |
| `turn failed` | an error at compose/dispatch/fallback/persist | `stage`, `error` |

Scheduled runs (`internal/schedule`) log `scheduled run started` /
`scheduled run finished` / `scheduled run failed` and `task picked up`, each
linked to the durable session and run record.

Log records intentionally carry **identifiers and outcomes, not payloads** — no
message bodies, instructions, or MCP config — consistent with the redaction rules
above.

The CLI can inspect logs with `podiom logs path` and `podiom logs follow`. The
web UI reads the same log through loopback-only `/api/logs` endpoints; these are
not available to non-loopback clients because provider diagnostics may include
sensitive local troubleshooting details even after redaction.

## Cross-platform process control (R10.1–R10.4)

- **Binary discovery** resolves `claude`/`codex` via `<NAME>_BIN` overrides, then
  PATH (Windows `PATHEXT` resolves `.cmd`/`.exe`/`.bat` shims), then conventional
  npm global locations.
- **Hung-agent cancellation** uses context cancellation plus a process-**group**
  kill so the CLI *and* its children (npm shim → node → workers) die together:
  a negative-PID `SIGKILL` on Unix, `taskkill /T /F` on Windows.
- **Paths** use `path/filepath` and `~` expansion throughout; `PODIOM_HOME` is
  resolved to an absolute path at startup so a relative override or a daemon
  `chdir` cannot relocate the storage root.

All OS-specific behaviour is isolated to `internal/exec` and `internal/config`.

## Firebase client configuration

`android/app/google-services.json` and `ios/App/App/GoogleService-Info.plist` are
committed. They identify the app to the Podiom Firebase project and are what make native
push work out of a clone.

They are not credentials. They ship inside every published APK and IPA, so a released app
already exposes them, and what they permit is limited to registering an app instance and
obtaining an FCM token for `org.podiom.app`. They cannot send a notification — that needs
the FCM service-account key, which lives in the Push Relay — and they grant no access to
a Podiom installation, which is guarded by the gateway token.

The keys they contain are restricted in the Google Cloud console by application
(`org.podiom.app`) and by API (Firebase Cloud Messaging and Firebase Installations only).
That restriction, not the files' location, is what makes a copied key useless.

## The push relay credential

Registering with the Push Relay yields an instance id and a bearer credential, kept in
`$PODIOM_HOME/relay.json` at mode 0600.

It authorizes exactly one thing: pushing to, and managing, the devices registered under
this installation's own tenant. It is deliberately **not** the Podiom gateway token — a
relay compromise must not become access to the installation, and the relay is never in the
return path for a notification action, which goes from the app straight to `podiomd`.

The relay returns the credential once and has no endpoint that reads it back. So an
unreadable `relay.json` is treated as a hard error rather than as "not enrolled":
re-registering would abandon the existing tenant and every device under it, irrecoverably,
and registration is rate limited per address. Back it up with the rest of `$PODIOM_HOME`.

## Push tokens

Registering a mobile device stores a push token — the value that lets the Podiom
Push Relay reach that device. It is treated as sensitive routing information, not as
device metadata: it is accepted at registration and never returned by any API, never
written into notification history (which records device ids), and never included in
a push payload.

Push payloads themselves carry only what is needed to present a notification and
route a tap. They never carry the gateway token, secrets, environment values,
prompts, transcripts, tool output, or file contents, because a payload crosses
infrastructure Podiom does not operate. See [notifications.md](notifications.md).
