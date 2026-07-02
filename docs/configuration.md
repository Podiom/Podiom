# Configuration

Podium's system configuration is a single declarative YAML file at
`$PODIUM_HOME/config.yaml` (default `~/.podium/config.yaml`). It is written with
inline comments on first run; this page mirrors those comments.

Schedules and projects are **not** configured here — schedules are
self-describing markdown files under `~/.podium/schedules/`, and projects live in
the shared ledger at `~/.podium/projects/projects.yaml`.

## `global`

Defaults applied across all agents unless overridden per agent.

| Field | Values | Meaning |
| --- | --- | --- |
| `provider` | `claude` \| `codex` | Default backend for new agents. |
| `model` | string | Default model name (empty = provider default). |
| `effort` | string | Default provider-supported reasoning effort. |
| `permission_mode` | `approve` \| `yolo` | `approve` relays each side effect to you (safe default); `yolo` auto-approves with whole-machine access. |
| `permission_timeout` | duration | Approve-mode prompt timeout before auto-deny, e.g. `30s` or `3m`. |
| `fallback` | list of profile names or `default` | Optional default fallback chain used when an agent declares none. |

## `github`

Public GitHub App details for connecting project repositories.

| Field | Values | Meaning |
| --- | --- | --- |
| `app_slug` | string | Official Podium GitHub App slug, used for the install URL. |
| `client_id` | string | Public GitHub App client ID used for device authorization. |
| `web_base` | URL | Optional override; defaults to `https://github.com`. |
| `api_base` | URL | Optional override; defaults to `https://api.github.com`. |
| `login_base` | URL | Optional override; defaults to `https://github.com/login`. |

Connected repos are downloaded as source snapshots into each project's `repo/`
subdirectory.
This does not require Git or GitHub CLI.

## `profiles`

Optional named auth contexts, each 1:1 with one underlying account. Podium owns
only the directory path + name — never credentials; you log in yourself with the
CLI's own auth flow against the profile dir.

| Field | Values | Meaning |
| --- | --- | --- |
| `name` | string | Profile name referenced by agents and fallback chains. |
| `provider` | `claude` \| `codex` | The provider this account belongs to. |
| `config_dir` | path | Claude profiles: exported as `CLAUDE_CONFIG_DIR`. |
| `home_dir` | path | Codex profiles: exported as `CODEX_HOME`. |

Omitting a profile on an agent uses the CLI's normal global login.

## `agents`

Named colleagues maintained by Podium. Empty optional fields inherit from
`global`. Each agent gets a directory under `~/.podium/agents/<name>/`.
Deleting an agent from the UI or CLI also removes its matching entry here when
present, after archiving its sessions into the preserved agent workspace.

| Field | Values | Meaning |
| --- | --- | --- |
| `name` | string | Unique agent name. |
| `provider` | `claude` \| `codex` | Backend (inherits `global`). |
| `profile` | profile name | Auth context (omit for global login). |
| `model` / `effort` | string | Per-agent overrides. |
| `permission_mode` | `approve` \| `yolo` | Per-agent override. |
| `fallback` | list of profile names or `default` | Ordered rate-limit fallback (may cross providers). |
| `mcp_config` | path | Opt-in per-agent MCP config, additive to native tools. |

## `server`

| Field | Default | Meaning |
| --- | --- | --- |
| `bind` | `127.0.0.1` | Web UI / API bind address (keep on loopback unless intentionally exposing). |
| `port` | `8787` | Web UI / API port. |

## `logging`

Daemon-owned structured logs live under `$PODIUM_HOME/logs` (default
`~/.podium/logs`).

| Field | Default | Meaning |
| --- | --- | --- |
| `level` | `info` | Minimum log level: `debug`, `info`, `warn`, or `error`. |
| `retention_days` | `7` | Number of calendar days of daemon logs to keep. |
