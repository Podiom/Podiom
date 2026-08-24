# Podiom CLI reference

Podiom ships two binaries:

- **`podiomd`** — the long-running daemon. Owns all session, agent, and schedule
  state; serves the web UI and API; runs the embedded scheduler.
- **`podiom`** — a thin client that always talks to a running `podiomd`. It does
  not run sessions in-process (R11.1 / D2).

This page is kept in sync with the binaries' built-in help (`--help` on any
command is authoritative).

## Storage root

All Podiom state lives under a single root:

- Default: `~/.podiom/`
- Override: set the `PODIOM_HOME` environment variable.

On first start, `podiomd` scaffolds the tree and writes a commented default
`config.yaml`, the Podiom-owned base `AGENTS.md`, and an empty project ledger.
Existing files are never overwritten.

## `podiomd`

Run the daemon (foreground):

```
podiomd
```

| Flag | Description |
| --- | --- |
| `--help` | Show help. |
| `--version` | Print version and commit. |

Bind address and port come from `config.yaml` (`server.bind`, `server.port`;
default `127.0.0.1:8787`).

## `podiom`

Global flags:

| Flag | Description |
| --- | --- |
| `--addr host:port` | Daemon address. Precedence: `--addr` → `PODIOM_ADDR` → `config.yaml` → `127.0.0.1:8787`. |
| `--version` | Print version and commit. |

### Install scripts

Public copy-paste installers:

```
curl -fsSL https://github.com/Podiom/Podiom/releases/latest/download/install.sh | bash
irm https://github.com/Podiom/Podiom/releases/latest/download/install.ps1 | iex
```

The scripts download release archives, verify `SHA256SUMS`, install `podiom` and
`podiomd`, optionally configure user-level autostart, then run `podiom onboard`.
By default they download from GitHub Releases (`latest/download` for the default
install, or `download/<version>` when a version is specified).

CI publishes every commit to `master` as `v0.1.<github-run-number>`. This is a
monotonic pre-1.0 series: no calendar cadence is implied, and release bursts are
fine.

Common script options:

| Option | Description |
| --- | --- |
| `--version VERSION` / `-Version VERSION` | Release version, default `latest`. |
| `--install-dir DIR` / `-InstallDir DIR` | Binary install directory. |
| `--podiom-home DIR` / `-PodiomHome DIR` | Set `PODIOM_HOME` for this install/autostart. |
| `--autostart ask|yes|no` / `-Autostart ask|yes|no` | Whether to start `podiomd` at login. |
| `--no-onboard` / `-NoOnboard` | Install only; skip first-run wizard. |
| `--source-fallback` / `-SourceFallback` | Build from source if release download fails. |

### `podiom status`

Report whether the daemon is running, plus its version and uptime.

```
podiom status
podiom --addr 127.0.0.1:8787 status
```

Exits non-zero with a "start it with: podiomd" hint when the daemon is
unreachable.

### `podiom doctor`

Check daemon reachability and native provider readiness.

```
podiom doctor
```

The command never reads credentials. It locates `claude` and `codex`, prints
versions when available, and gives install/login hints when a provider is not
ready.

### `podiom logs path`

Print the active daemon log path.

```
podiom logs path
```

Logs live under `$PODIOM_HOME/logs` (default `~/.podiom/logs`).

### `podiom logs follow`

Print recent daemon log lines and keep following the active log across daily
rotation.

```
podiom logs follow
podiom logs follow -n 200
podiom logs follow --no-follow
```

| Flag | Description |
| --- | --- |
| `--lines`, `-n` | Number of recent lines to print first (default 100). |
| `--no-follow` | Print recent lines and exit. |

### `podiom update check`

Check GitHub Releases for a newer Podiom build. This works without a running
daemon.

```
podiom update check
podiom update check --json
podiom update check --version v0.1.123
```

`dev` and `*-dirty` builds are reported as non-release builds; applying an
update from them requires `--force`.

### `podiom update apply`

Download, checksum-verify, extract, and install a release archive for the current
OS/architecture.

```
podiom update apply --yes
podiom update apply --version v0.1.123 --yes
podiom update apply --force --yes
```

When `podiomd` is reachable, the CLI asks the daemon to coordinate the update so
the web UI and daemon restart together. If no daemon is reachable, the CLI
updates the sibling binaries directly. Updating may interrupt active turns or
scheduled runs because `podiomd` restarts.

Linux support is distro-neutral: releases ship static Go binaries, not distro
packages.

### `podiom token show`

Print the gateway token — the secret every API/WebSocket client must present
(see [Security](security.md#gateway-token)). Reads straight from
`$PODIOM_HOME/gateway.token`, so it works whether or not the daemon is running.

```
podiom token show
```

Paste the value into the web UI's token screen; each browser remembers it.

### `podiom token rotate`

Rotate the gateway token through the running daemon. The old value stops
working immediately: connected web clients are disconnected and prompted for
the new value, while CLI clients pick it up from disk automatically.

```
podiom token rotate
```

Rotation requires a running daemon so its in-memory token flips atomically
with the on-disk one. In the [Home Assistant app](home-assistant.md), use the
`rotate_token` toggle on the Configuration page instead.

### `podiom usage`

Show provider plan-limit usage per profile. Reports Claude and Codex plan-limit
utilization (5-hour and weekly windows) for each configured auth profile.

```
podiom usage
podiom usage --json
podiom usage --refresh
```

| Flag | Description |
| --- | --- |
| `--json` | Print machine-readable JSON. |
| `--refresh` | Force a live re-fetch instead of cached data. |

### `podiom usage tokens`

Show token usage across sessions. Aggregates and displays token usage (input,
output, cache) from all sessions, optionally filtered by agent.

```
podiom usage tokens
podiom usage tokens --agent jared
podiom usage tokens --json
```

| Flag | Description |
| --- | --- |
| `--agent` | Filter by agent name. |
| `--json` | Output as JSON. |

Without `--agent`, displays a summary table of all agents:

```
AGENT     SESSIONS  INPUT    OUTPUT   CACHE_R  CACHE_W  TOTAL
jared     47        1.9M     449.2K   50.0K    10.0K    2.4M
builder   12        523.1K   98.2K    0        0        621.3K
────────  ────────  ──────── ──────── ──────── ──────── ────────
TOTAL     59        2.4M     547.4K   50.0K    10.0K    3.0M
```

With `--agent`, displays detailed breakdown for that agent including per-model
usage.

### `podiom onboard` / `podiom setup`

Run the first-use wizard.

```
podiom onboard
podiom setup
```

The wizard starts `podiomd` if needed, helps get Claude and/or Codex available,
lets you choose a default provider or configured profile for the first agent,
asks personality/workstyle questions, creates the agent, then asks a working
provider to draft the agent's `SOUL.md`. The generated soul is previewed before
saving, with regenerate and edit options.

At the end, onboarding records completion in `$PODIOM_HOME/onboarding.json` and
prints the gateway-token location. On supported local desktops it copies the
token to the clipboard; otherwise it prints clear manual-copy instructions. The
token value is printed only to your terminal, never to daemon or add-on logs.

### `podiom memory`

Inspect and manage the durable `MEMORY.md` that each agent curates through
nightly session consolidation, called dreaming.

```
podiom memory show jared
podiom memory edit jared
podiom memory clear jared --yes
podiom memory dream jared
podiom memory status
podiom memory status jared
```

| Command | Description |
| --- | --- |
| `show <agent>` | Print the agent's current `MEMORY.md`. |
| `edit <agent>` | Open memory in `$EDITOR`, falling back to `vi`; saved edits are authoritative. |
| `clear <agent> [--yes]` | Empty memory after confirmation, or skip the prompt with `--yes`. |
| `dream <agent>` | Consolidate un-dreamed sessions now; does nothing when none are pending. |
| `status [<agent>]` | Show last-dream time, pending sessions, and memory line-budget usage. |

### `podiom skills`

Browse the reusable `SKILL.md` capability folders available to agents. Podiom
discovers skills under `~/.agents/skills`, `~/.claude/skills`, and
`~/.codex/skills`, then presents one deduplicated catalogue.

```
podiom skills list
podiom skills list --source codex
podiom skills show hello-podiom
podiom skills paths
podiom skills scan
podiom skills relink
```

| Command | Description |
| --- | --- |
| `list [--source agents\|claude\|codex]` | List skills with their source badges and descriptions. |
| `show <name>` | Print a skill's `SKILL.md`, source paths, and any cross-source conflict. |
| `paths` | Print the three skill roots and resolved union topology. |
| `scan` / `relink` | Rebuild the shared `~/.agents/skills` union links without overwriting real folders. |

### `podiom agents list`

List durable agents known to the daemon.

```
podiom agents list
```

### `podiom agents create`

Create an agent through `podiomd`. This stores the agent, creates
`$PODIOM_HOME/agents/<name>/SOUL.md`, and creates its `workspace/`.

```
podiom agents create jared
podiom agents create builder --provider claude --model sonnet --effort medium --permission approve
podiom agents create juno --generate-soul
```

| Flag | Description |
| --- | --- |
| `--provider claude|codex` | Provider for the agent. Empty inherits `global.provider`. |
| `--model name` | Default model. Empty means provider default. |
| `--effort level` | Default provider-supported reasoning effort. |
| `--permission approve|auto|yolo` | Agent permission default. |
| `--generate-soul` | Ask the daemon to generate and save the initial `SOUL.md` after creation. |

Choosing `--permission yolo` prints a whole-machine-access warning: in `yolo`
every tool call is auto-approved and the workspace is **not** a sandbox (R8.31).
See [Security & logging](security.md) for the full permission model.

### `podiom agents update`

Update durable agent defaults or regenerate an agent's `SOUL.md`.

```
podiom agents update jared --model sonnet
podiom agents update jared --generate-soul --notes "make the voice more direct"
podiom agents update jared --generate-soul --yes
```

When `--generate-soul` is used, the CLI previews the generated markdown and asks
before overwriting `$PODIOM_HOME/agents/<name>/SOUL.md`. `--yes` skips that
confirmation. See [SOUL.md generation](soul-generation.md) for the generated
shape.

### `podiom agents delete`

Delete an agent through `podiomd`.

```
podiom agents delete jared
```

The command requires typing the exact agent name before deletion. It archives
the agent's sessions as JSON files under
`$PODIOM_HOME/agents/<name>/workspace/session-archive/`, removes those sessions
from active history, removes the agent from Podiom's database, and removes a
matching `config.yaml` `agents` entry when present. Files under
`$PODIOM_HOME/agents/<name>/` are preserved.

### `podiom chat`

Send one chat turn through the daemon. Use `--agent` to create a new CLI-origin
session or `--session` to continue an existing session.

```
podiom chat --agent jared "Summarise this workspace"
podiom chat --session <session-id> "Continue"
```

In `approve` mode, permission requests are shown inline. Answer `y`/`yes` to
allow the requested tool input unchanged; any other answer denies it. Unanswered
requests auto-deny after `global.permission_timeout`.

Slash commands can be sent as the message body:

| Command | Effect |
| --- | --- |
| `/model <name>` | Set the session model for subsequent turns. |
| `/effort <level>` | Set a provider-supported reasoning effort. |
| `/profile <name|default>` | Switch auth profile. `default` clears the profile; the next turn replays history into a fresh backing session/thread. |
| `/permission approve|auto|yolo` | Set permission mode. |
| `/name <text>` | Rename the session. |
| `/describe <text>` | Set the session description. |
| `/compact` | Summarize older history to free the context window; the next turn replays the summary plus recent turns into a fresh backing session/thread. |
| `/help` | Print command help. |

### `podiom sessions`

Inspect and delete durable sessions — the chat threads Podiom keeps for each
agent, whether they were started from the web UI, the CLI, a schedule, or a
roadmap task. See [sessions.md](sessions.md) for the concept.

```
podiom sessions list
podiom sessions delete <id>
podiom sessions delete <id> --yes
```

`podiom sessions list` prints one line per session with its id, agent, origin,
and name (`-` when the session has not been named). An empty store prints
`no sessions yet`.

`podiom sessions delete <id>` permanently removes the session and its message
history; this cannot be undone. It asks `Delete session <id> and its chat
history?` first, and `-y`/`--yes` skips that prompt.

### `podiom schedules list`

List every schedule file with its timing, agent, permission policy, next-run
time, and recent run count. See [scheduling.md](scheduling.md) for the file
format.

```
podiom schedules list
```

Invalid files are shown with an `[invalid]` marker and the parse error rather
than being silently skipped.

The timing column shows the cadence, `webhook` for a schedule that only fires
when an outside service calls it, or `<cadence>+webhook` for one that does both.
A webhook-only schedule has no next-run time.

### `podiom schedules run`

Trigger a schedule immediately ("Run now"). The run executes a full agent turn
and creates a durable schedule-origin session you can revisit and continue
manually; the command prints the run id, status, and session id.

```
podiom schedules run morning-calendar
```

A disabled schedule can still be run manually; only automatic firing is
suppressed while it is disabled.

### `podiom schedules delete`

Delete a schedule through `podiomd`.

```
podiom schedules delete morning-calendar
podiom schedules delete morning-calendar --yes
```

Removes the schedule's markdown file and its run history. Sessions produced by
past runs of the schedule are preserved — deleting the schedule does not delete
the work it did.

The command asks `Delete schedule "<name>" and its run history?` before acting;
`-y`/`--yes` skips the prompt.

### `podiom projects list`

List the shared project ledger (`~/.podiom/projects/projects.yaml`). See
[projects.md](projects.md).

```
podiom projects list
```

### `podiom tasks list`

List roadmap tasks with their status, assigned agent, and project.

```
podiom tasks list
```

Tasks are created, assigned, moved, and started from the **Roadmap** page in the
web UI.

### `podiom tasks delete`

Delete a roadmap task through `podiomd`.

```
podiom tasks delete <id>
podiom tasks delete <id> --yes
```

Any session started from the task is preserved. A task that is `in_progress`
must be moved out of that status first.

The command asks `Delete task <id>? Its session (if any) is kept.` before
acting; `-y`/`--yes` skips the prompt.

### podiom mcp

Manage MCP servers and per-agent assignments.

```
podiom mcp list
podiom mcp show filesystem
podiom mcp add myserver --transport stdio --command npx --arg @modelcontextprotocol/server-filesystem --arg /tmp
podiom mcp add myapi --transport http --url http://localhost:3000/mcp
podiom mcp remove myserver
podiom mcp assign filesystem jared
podiom mcp unassign filesystem jared
podiom mcp check jared
```

| Command | Description |
| --- | --- |
| `list` | List the MCP catalogue (name, transport, sources, env status). |
| `show <name>` | Show an MCP server's config and which agents have it assigned. |
| `add <name>` | Add or replace a Podiom-owned MCP server. |
| `remove <name>` | Remove a Podiom-owned MCP server. |
| `assign <server> <agent>` | Assign an MCP server to an agent. |
| `unassign <server> <agent>` | Unassign an MCP server from an agent. |
| `check <agent>` | Dry-run projection showing, per assigned server, whether Claude/Codex will pick it up and why not if not. |

Flags for `add`:

| Flag | Description |
| --- | --- |
| `--transport` | Transport type: `stdio` or `http` (default `stdio`). |
| `--url` | HTTP MCP URL (for `http` transport). |
| `--command` | Stdio command to run. |
| `--arg` | Stdio command argument (repeatable). |
| `--env` | Environment variable, `NAME` or `NAME=VALUE` (repeatable); bare `NAME` passes through the daemon's own environment. |

### podiom profiles

Manage Claude and Codex auth profiles. Profiles are named authentication
configurations that agents can reference via their `profile` field.

```
podiom profiles list
podiom profiles create work --provider claude
podiom profiles create codex-main --provider codex --home-dir ~/.podiom/profiles/codex-main
podiom profiles update work --config-dir ~/.podiom/profiles/claude-work
podiom profiles delete work --yes
```

| Command | Description |
| --- | --- |
| `list` | List profiles with their provider and resolved directory. |
| `create <name>` | Create a profile. |
| `update <name>` | Update an existing profile. |
| `delete <name>` | Delete a profile (prompts for confirmation unless `--yes`). |

Flags for `create` and `update`:

| Flag | Description |
| --- | --- |
| `--provider` | Provider: `claude` or `codex` (default `claude`). |
| `--config-dir` | Claude config directory (sets `CLAUDE_CONFIG_DIR`; default `~/.podiom/profiles/claude-<name>`). |
| `--home-dir` | Codex home directory (sets `CODEX_HOME`; default `~/.podiom/profiles/codex-<name>`). |

### podiom plan

Review and decide implementation plans submitted by agents in plan mode. See
[plan-mode.md](requirements/plan-mode.md) for background on when plan mode
triggers and what each state means.

```
podiom plan show
podiom plan show abc123
podiom plan status
podiom plan status abc123
podiom plan approve abc123
podiom plan feedback abc123 "Please also handle the error case"
podiom plan reject abc123
```

| Command | Description |
| --- | --- |
| `show [session]` | Print the current plan Markdown. Omitting `session` auto-resolves to the one session awaiting approval (errors if zero or more than one). |
| `status [session]` | Print session id, plan state, whether it's an explicit plan-mode request, and (if present) the plan file path and last-updated time. |
| `approve <session>` | Approve the plan and continue the build, streaming the agent's next turn. |
| `feedback <session> <text>` | Send revision feedback and continue the conversation. |
| `reject <session>` | Reject the plan and leave plan mode. |

---

*More commands and flags are added as later phases land; each gets an entry here
when it ships.*
