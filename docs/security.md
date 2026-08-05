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
`$PODIOM_HOME/gateway.token`. Only static web assets, the token-entry screen,
and `/healthz` are reachable without it; nothing about sessions, agents,
plans, or memory is exposed pre-token.

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

Why a token even when the UI sits behind authenticated proxies (HA Ingress):
defense in depth for the client→daemon hop, safe LAN exposure of standalone
installs (`server.bind` beyond loopback plus `server.allow_from`), and the
auth primitive for the future remote mode.

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
Connected repositories are downloaded as source snapshots into project `repo/`
subdirectories; v1 does not create Git remotes, commits, pushes, or PRs.

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
