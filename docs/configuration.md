# Configuration

Podiom's system configuration is a single declarative YAML file at
`$PODIOM_HOME/config.yaml` (default `~/.podiom/config.yaml`). It is written with
inline comments on first run; this page mirrors those comments.

Schedules and projects are **not** configured here — schedules are
self-describing markdown files under `~/.podiom/schedules/`, and projects live in
the shared ledger at `~/.podiom/projects/projects.yaml`.

## `global`

Defaults applied across all agents unless overridden per agent.

| Field | Values | Meaning |
| --- | --- | --- |
| `provider` | `claude` \| `codex` | Default backend for new agents. |
| `model` | string | Default model name (empty = provider default). |
| `effort` | string | Default provider-supported reasoning effort. |
| `permission_mode` | `approve` \| `auto` \| `yolo` | `approve` relays each side effect to you (safe default); `auto` runs edits inside the session's working directory unattended and still asks for the rest; `yolo` auto-approves with whole-machine access. |
| `permission_timeout` | duration | Approve-mode prompt timeout before auto-deny, e.g. `30s` or `3m`. |
| `fallback` | list of profile names or `default` | Optional default fallback chain used when an agent declares none. |
| `collapse_reasoning` | bool | Fold a finished thinking/working note in chat down to one clickable line once the turn's answer arrives. Default `false` (notes stay expanded). Editable from Settings → Chat display. |

## `github`

**Entirely optional.** Omit this block and Podiom uses its **official GitHub App**
(`podiom`) to connect and sync project repositories — nothing to
configure, it works out of the box for every user.

The block exists purely so that, if you want, you can register and point at your
**own** GitHub App instead of Podiom's. When you set these fields, connect + sync
run through your App rather than the official one.

| Field | Values | Meaning |
| --- | --- | --- |
| `app_slug` | string | Your GitHub App's slug, used for the install URL. Defaults to Podiom's official App. |
| `client_id` | string | Your GitHub App's public client ID, used for device authorization. Defaults to Podiom's official App. |
| `web_base` | URL | Optional override; defaults to `https://github.com`. Set for GitHub Enterprise. |
| `api_base` | URL | Optional override; defaults to `https://api.github.com`. Set for GitHub Enterprise. |
| `login_base` | URL | Optional override; defaults to `https://github.com/login`. Set for GitHub Enterprise. |

These are all **public identifiers — never secrets**. Do not put tokens, private
keys, or client secrets here. Authorization happens via GitHub's device flow, and
the resulting access token is stored separately in `~/.podiom/github/token.json`.

Connected repos are downloaded as source snapshots into each project's `repo/`
subdirectory.
This does not require Git or GitHub CLI.

## `profiles`

Optional named auth contexts, each 1:1 with one underlying account. Podiom owns
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

Named colleagues maintained by Podiom. Empty optional fields inherit from
`global`. Each agent gets a directory under `~/.podiom/agents/<name>/`.
Deleting an agent from the UI or CLI also removes its matching entry here when
present, after archiving its sessions into the preserved agent workspace.

| Field | Values | Meaning |
| --- | --- | --- |
| `name` | string | Unique agent name. |
| `provider` | `claude` \| `codex` | Backend (inherits `global`). |
| `profile` | profile name | Auth context (omit for global login). |
| `model` / `effort` | string | Per-agent overrides. |
| `permission_mode` | `approve` \| `auto` \| `yolo` | Per-agent override. |
| `fallback` | list of profile names or `default` | Ordered rate-limit fallback (may cross providers). Applied automatically for non-interactive runs; interactive turns prompt the user to confirm or override it. |
| `mcp_config` | path | Opt-in per-agent MCP config, additive to native tools. |

## `voice`

**Entirely optional.** Enables [voice input](voice-input.md): a microphone
button in chat and in the task/goal prompt fields, transcribed with the OpenAI
Whisper API. Omit the block and the buttons simply report that no key is
configured.

| Field | Values | Meaning |
| --- | --- | --- |
| `openai_api_key` | string | OpenAI API key used server-side for Whisper transcription. **A secret stored in plain text** — prefer the `PODIOM_OPENAI_API_KEY` or `OPENAI_API_KEY` environment variables, which take precedence. Editable from Settings → Voice input; never returned by the API or logged. |

## `server`

| Field | Default | Meaning |
| --- | --- | --- |
| `bind` | `127.0.0.1` | Web UI / API bind address (keep on loopback unless intentionally exposing). |
| `port` | `8787` | Web UI / API port. |
| `allow_from` | *(empty)* | Optional list of source IPs/CIDRs allowed to connect at all (e.g. `["192.168.1.0/24"]`). Loopback is always allowed; empty means no restriction. Useful when `bind` is not loopback. |

Every API and WebSocket client must present the **gateway token**, generated
automatically on first daemon start and stored at `$PODIOM_HOME/gateway.token`.
Retrieve it with `podiom token show`, rotate it with `podiom token rotate` —
see [Security](security.md#gateway-token) for the full model. In the
[Home Assistant app](home-assistant.md), the Ingress proxy address is
additionally enforced automatically, independent of `allow_from`.

## `logging`

Daemon-owned structured logs live under `$PODIOM_HOME/logs` (default
`~/.podiom/logs`).

| Field | Default | Meaning |
| --- | --- | --- |
| `level` | `info` | Minimum log level: `debug`, `info`, `warn`, or `error`. |
| `retention_days` | `7` | Number of calendar days of daemon logs to keep. |
