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

### `podiom onboard` / `podiom setup`

Run the first-use wizard.

```
podiom onboard
podiom setup
```

The wizard starts `podiomd` if needed, helps get Claude and/or Codex available,
asks personality/workstyle questions, creates the first agent, then asks a
working provider to draft the agent's `SOUL.md`. The generated soul is previewed
before saving, with regenerate and edit options.

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
| `--permission approve|yolo` | Agent permission default. |
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
| `/permission approve|yolo` | Set permission mode. |
| `/name <text>` | Rename the session. |
| `/describe <text>` | Set the session description. |
| `/help` | Print command help. |

### `podiom schedules list`

List every schedule file with its timing, agent, permission policy, next-run
time, and recent run count. See [scheduling.md](scheduling.md) for the file
format.

```
podiom schedules list
```

Invalid files are shown with an `[invalid]` marker and the parse error rather
than being silently skipped.

### `podiom schedules run`

Trigger a schedule immediately ("Run now"). The run executes a full agent turn
and creates a durable schedule-origin session you can revisit and continue
manually; the command prints the run id, status, and session id.

```
podiom schedules run morning-calendar
```

A disabled schedule can still be run manually; only automatic firing is
suppressed while it is disabled.

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

---

*More commands and flags are added as later phases land; each gets an entry here
when it ships.*
