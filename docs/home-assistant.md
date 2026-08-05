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
   add `https://github.com/Podiom/homeassistant-addons`.
2. Install **Podiom** and start it. It appears in the HA sidebar.

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

Use **Settings → Global config → profile**. Every profile chip carries a
sign-in dot (green signed in, red signed out, amber unknown); pick the account
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

## Always-on scheduling

The app starts on boot and is watchdog-supervised, so [schedules](scheduling.md)
and nightly memory dreaming fire reliably — a concrete advantage over a
standalone install where they only run while `podiomd` happens to be running.

## Resource honesty

The CLIs are cloud-backed clients, so a Pi is viable — but Podiom has **no
concurrency cap**: every parallel agent turn spawns processes and consumes
RAM. On small boards, keep the number of simultaneously active agents modest.

## Networking details

- The app is **Ingress-only**: no ports are exposed, and `podiomd` accepts
  connections only from HA's Ingress proxy (and container-local callers).
- The web UI, WebSocket streaming, permission prompts, and plan mode all work
  through Ingress, including remotely via Nabu Casa.
- Developers: `scripts/ingress-sim` simulates the Ingress sub-path + headers
  against a local daemon for testing without an HA install.

## Source layout

The add-on is built from this repository (`ha/` — Dockerfile, s6 services,
manifest sources); release CI publishes multi-arch images to
`ghcr.io/podiom/podiom-ha` and renders the manifest into
[`Podiom/homeassistant-addons`](https://github.com/Podiom/homeassistant-addons),
the repository users add to their store. The in-store user guide is the
add-on's `DOCS.md` (from `ha/addon/DOCS.md`); this page is the canonical
long-form documentation.
