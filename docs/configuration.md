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
| `collapse_reasoning` | bool | Fold a finished thinking/working note in chat down to one clickable line once the turn's answer arrives. Default `false` (notes stay expanded). Editable from Settings → General. |
| `auto_archive_days` | positive integer | Archive sessions after this many days without activity. Default `7`. Editable from Settings → General. |

`provider`, `profile` and `fallback` are editable from **Settings → Providers**.
`collapse_reasoning` and `auto_archive_days` are editable from **Settings → General**.
`model`, `effort`, `permission_mode` and `permission_timeout` are set in this
file only — the web UI does not edit them, and per-agent overrides still apply.

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

Projects created from GitHub prefer a real clone in the project's `repo/`
subdirectory using the user's Git credentials. When that is unavailable, Podiom
falls back to an App-authorized archive snapshot, which does not require Git or
GitHub CLI.

## `profiles`

Optional named auth contexts, each 1:1 with one underlying account. Podiom owns
only the directory path + name — never credentials; the login runs the CLI's own
auth flow against the profile dir.

Sign in from **Settings → Providers**: each provider gets a card listing its
accounts — the CLI's own login first, then every profile pointed at that
provider. Press **Sign in** on the account's row. Podiom runs the provider's
login CLI, opens the authorization page in a popup, and (for Claude) forwards
the code you paste back. The CLI performs the token exchange and writes to the profile dir itself
— Podiom never handles the resulting credential. The same flow works from a
phone, since the browser never has to reach the daemon's own localhost.

If a turn dies because its account is signed out, chat shows the same sign-in
card inline, scoped to that session's provider and profile — so the fix is one
click from where the failure happened rather than a trip to a terminal.

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
| `openai_api_key` | string | OpenAI API key used server-side for Whisper transcription. **A secret stored in plain text** — prefer the `PODIOM_OPENAI_API_KEY` or `OPENAI_API_KEY` environment variables, which take precedence. Editable from Settings → Credentials → Voice input; never returned by the API or logged. |

## `server`

| Field | Default | Meaning |
| --- | --- | --- |
| `bind` | `127.0.0.1` | Web UI / API bind address (keep on loopback unless intentionally exposing). |
| `port` | `8787` | Web UI / API port. |
| `allow_from` | *(empty)* | Optional list of source IPs/CIDRs allowed to connect at all (e.g. `["192.168.1.0/24"]`). Loopback is always allowed; empty means no restriction. Useful when `bind` is not loopback. |
| `advertise` | `true` | Announce this daemon on the local network over mDNS/DNS-SD as `_podiom._tcp`, so the [mobile apps](mobile.md) can find it instead of being told an address. Skipped automatically when `bind` is loopback (nothing else could reach the advertised address) and in the Home Assistant app. |

Every API and WebSocket client must present the **gateway token**, generated
automatically on first daemon start and stored at `$PODIOM_HOME/gateway.token`.
Retrieve it with `podiom token show`, rotate it with `podiom token rotate` —
see [Security](security.md#gateway-token) for the full model. In the
[Home Assistant app](home-assistant.md), `server.port` remains the internal
Ingress port (`8099`). The optional external mobile port is a Supervisor Network
mapping for the separate `8100/tcp` API listener, not a `config.yaml` setting.
That listener accepts private LAN ranges by default; a non-empty `allow_from`
replaces those defaults and can restrict it to the actual local subnet. The
Ingress proxy remains accepted on the Ingress listener independently.

## `logging`

Daemon-owned structured logs live under `$PODIOM_HOME/logs` (default
`~/.podiom/logs`).

| Field | Default | Meaning |
| --- | --- | --- |
| `level` | `info` | Minimum log level: `debug`, `info`, `warn`, or `error`. |
| `retention_days` | `7` | Number of calendar days of daemon logs to keep. |
