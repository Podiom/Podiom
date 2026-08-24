# Podiom as a Home Assistant app

Podiom can be deployed as a **Home Assistant app (add-on)** — an alternative
to the [standalone install](cli.md#install-scripts), not a replacement. The
app runs the full Podiom stack inside a supervised container: `podiomd` with
the web UI, the `podiom` CLI, the `claude` and `codex` CLIs, `mcp-proxy`, and
a web onboarding terminal.

**Why:** Home Assistant solves safe internet exposure. If your HA is reachable
from outside (e.g. Nabu Casa cloud), Podiom's web UI becomes reachable from
your phone anywhere — with HA handling TLS, remote routing, and login (use HA
MFA!). Podiom itself opens no ports to the internet.

## Install

1. In Home Assistant: **Settings → Add-ons → Add-on store → ⋮ → Repositories**,
   add `https://github.com/Podiom/ha-app`.
2. Install **Podiom** and start it. It appears in the HA sidebar.

Existing installs that already use `https://github.com/Podiom/homeassistant-addons`
continue through GitHub's repository redirect. Do not add a new repository at
the old name.

Images are published for `amd64` and `aarch64` (Raspberry Pi–class hardware
works — the LLM compute is cloud-side; see the resource note below).

## First run

1. Open **Podiom** from the sidebar. On a fresh install, the web UI opens a
   Home Assistant setup page with an embedded terminal.
2. Open **Onboard**. The wizard verifies the bundled Claude/Codex CLIs, guides
   you through device login when needed, creates your first agent, and
   generates its `SOUL.md`.
3. When the wizard finishes, click **Take the stage**. The setup page stores
   the gateway token and opens the dashboard.
4. Future visits, including from other HA-authenticated browsers, open the
   dashboard directly. HA installs also show a **Terminal** sidebar item for
   later Claude/Codex re-authentication or maintenance shell access.

## The gateway token

HA's login protects the *browser → HA* hop; the gateway token authenticates
the *client → podiomd* hop ([full model](security.md#gateway-token)). In the
HA app the token lifecycle is mostly terminal-free:

- **First setup** — after `podiom onboard` completes, the setup page stores the
  token in the browser. Later HA-authenticated browsers bootstrap the same token
  automatically after confirming onboarding is complete.
- **Rotate** — switch on the `rotate_token` toggle and save; the app restarts,
  rotates, writes the new value back, and resets the toggle. Open browser tabs
  disconnect and bootstrap the new value through the HA-authenticated setup
  check.

The token value never appears in the add-on **log**. The CLI can still show it
with `podiom token show` inside the container, and the HA setup page can show
it after onboarding has completed.

## Web terminal

The app exposes terminal entries behind the same Ingress login, **outside** the
Podiom UI:

| URL (under the app's Ingress path) | Drops you into |
| --- | --- |
| `…/terminal/onboard` | the shared `podiom onboard` wizard |
| `…/terminal/shell` | a maintenance shell |

The HA setup page embeds the Onboard entry. The later **Terminal** sidebar page
opens Shell for maintenance.

### Re-authenticating later

Use **Settings → Providers**. Every account row carries a
sign-in dot (green signed in, red signed out, amber unknown); find the account
and press **Sign in**. Podiom runs the provider's own login and opens the
authorization page in a popup — Claude then asks you to paste back the code
that page shows, Codex shows a one-time code and finishes on its own. Nothing
leaves the browser except that code, and the CLI writes its own credentials
into the profile's directory.

If a provider CLI has no login Podiom can drive, the panel falls back to
printing the terminal command. Use **Terminal** → Shell for that:

```sh
claude auth login
codex login --device-auth
```

For a profile-scoped login from the shell, create the directory yourself and
prefix the CLI's environment variable:

```sh
mkdir -p /data/home/.claude-work
CLAUDE_CONFIG_DIR=/data/home/.claude-work claude auth login

mkdir -p /data/home/.codex-work
CODEX_HOME=/data/home/.codex-work codex login --device-auth
```

> **Honest note:** the terminal runs as the same non-root `podiom` account as
> the daemon. Whoever reaches it can access all persistent Podiom data —
> `$PODIOM_HOME`, every profile's credentials, SSH keys, and the gateway token
> — but cannot write root-owned system paths in the container. These entries
> sit behind HA's login exactly like the UI, so **your HA account's security is
> Podiom's security. Enable HA MFA.**

## Storage, backups, updates

Everything persistent lives on `/data` (`PODIOM_HOME=/data/podiom`,
`HOME=/data/home`), so:

- **Restarts and app updates lose nothing** — sessions, agent SOUL/MEMORY,
  skills, profiles, CLI logins, Git configuration, SSH keys, and the gateway
  token all survive. Upgrading from an older root-based image migrates their
  ownership without broadening key permissions.
- **HA backups cover all of Podiom for free.** A restored backup brings back
  the complete state. Because `/data` contains CLI credentials and the
  gateway token, **use password-protected backups**.
- **Updates ship as app updates** through the store (the in-app self-update is
  disabled in HA mode). Each release pins specific `claude`/`codex` CLI
  versions — the changelog lists them.

## Language toolchains

The container is sealed — agents cannot `apt install` a compiler, and anything
written outside `/data` is lost on the next app update. So the **Language
toolchains** option on the app's Configuration page decides what the container
provides: `go`, `node`, `python`, `rust`, `swift`.

Ticked toolchains install into `/data/podiom/toolchains/` in the background at
start and are on `PATH` for every agent process, Claude- and Codex-backed
alike. Unlike the [toolset](toolset.md) — individual CLI tools agents install
for themselves — these are whole language runtimes, and you choose them.
Unticking one deletes it. `node` is listed but fixed, because the bundled
`claude` and `codex` run on it.

This is HA-only by design. On a standalone install you already own the host —
install what you need there.

Individual command-line tools are a separate matter and need nothing from you:
agents install those themselves into `/data/podiom/toolset/`, which is also on
`/data` and so also survives app updates. See [toolset.md](toolset.md).

Note on Swift: this is the open-source toolchain (SwiftPM, `swift build`,
`swift test`). `xcodebuild`, the iOS Simulator and code signing need Xcode,
which is macOS-only and cannot run in this container.

Full behaviour, disk costs and caveats: `ha/addon/DOCS.md`.

## Always-on scheduling

The app starts on boot and is watchdog-supervised, so [schedules](scheduling.md)
and nightly memory dreaming fire reliably — a concrete advantage over a
standalone install where they only run while `podiomd` happens to be running.

## Resource honesty

The CLIs are cloud-backed clients, so a Pi is viable — but Podiom has **no
concurrency cap**: every parallel agent turn spawns processes and consumes
RAM. On small boards, keep the number of simultaneously active agents modest.

## Networking details

- **Ingress remains the default browser surface.** It listens inside the
  container on `8099` and accepts only HA's Ingress proxy (plus loopback). The
  web UI, terminal, onboarding bootstrap, WebSocket streaming, permission
  prompts, and plan mode work there, including remotely through Nabu Casa.
- A second listener on container port `8100` is **API-only** for the native
  [mobile apps](mobile.md). It serves `/healthz`, `/api/*`, and `/api/ws`; it
  does not serve the SPA or terminal, and every API/WebSocket request requires
  the Podiom gateway token.
- Supervisor declares `8100/tcp` disabled by default. To opt in, open the Podiom
  add-on's **Configuration → Network**, choose **Show disabled ports**, map the
  Podiom mobile API to a host port (`8787` recommended), save, and restart.
  Connect the app to `http://<HA-LAN-IP>:<mapped-port>` with no sidebar path.
- The LAN listener accepts private IPv4 networks and IPv6 ULA by default.
  `server.allow_from` can replace those defaults with a narrower subnet. It is
  plain HTTP, so use it only on a trusted LAN; it is not a Nabu Casa or remote
  mobile endpoint.
- Developers: `scripts/ingress-sim` simulates the Ingress sub-path + headers
  against a local daemon for testing without an HA install.

## Source layout

The add-on is built from this repository (`ha/` — Dockerfile, s6 services,
manifest sources); release CI publishes multi-arch images to
`ghcr.io/podiom/podiom-ha` and renders the manifest into
[`Podiom/ha-app`](https://github.com/Podiom/ha-app),
the repository users add to their store. The in-store user guide is the
add-on's `DOCS.md` (from `ha/addon/DOCS.md`); this page is the canonical
long-form documentation.
