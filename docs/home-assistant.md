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

1. Open **Podiom** from the sidebar — the web UI shows a token screen.
2. Open the add-on's **Configuration** page and copy the `gateway_token`
   value (it is generated on first start and managed by Podiom — treat the
   field as read-only).
3. Return to the token screen and open the **Claude terminal** and/or
   **Codex terminal** buttons. Add a profile name first if you want a
   profile-scoped CLI login.
4. Paste the token into the token screen. The browser remembers it.
5. Create your first agent.

## The gateway token

HA's login protects the *browser → HA* hop; the gateway token authenticates
the *client → podiomd* hop ([full model](security.md#gateway-token)). In the
HA app the token lifecycle is terminal-free:

- **Retrieve** — the Configuration page's `gateway_token` field.
- **Rotate** — switch on the `rotate_token` toggle and save; the app restarts,
  rotates, writes the new value back, and resets the toggle. Open browser tabs
  drop back to the token screen; enter the new value.

The token value never appears in the add-on **log** — only the Configuration
page shows it.

## CLI logins (web terminal)

The `claude`/`codex` CLIs authenticate with their own device-style flows. The
app exposes onboarding entries behind the same Ingress login, **outside** the
Podiom UI:

| URL (under the app's Ingress path) | Drops you into |
| --- | --- |
| `…/terminal/claude` | `claude` login |
| `…/terminal/codex` | `codex` device-code login |
| `…/terminal/<cli>/<profile>` | the same, scoped to a named [profile](configuration.md#profiles) |

The HA token screen has buttons for these entries, so you do not need to edit
the URL by hand. Each entry runs the right login command, then drops to a shell
and prints a link back to Podiom. The login prints a URL you open in your own
browser and a code to paste back — this works fully over the web terminal.

> **Honest note:** whoever reaches a terminal entry has shell access to the
> whole container — `$PODIOM_HOME`, every profile's credentials, and the
> gateway token. These entries sit behind HA's login exactly like the UI, so
> **your HA account's security is Podiom's security. Enable HA MFA.**

## Storage, backups, updates

Everything persistent lives on `/data` (`PODIOM_HOME=/data/podiom`,
`HOME=/data/home`), so:

- **Restarts and app updates lose nothing** — sessions, agent SOUL/MEMORY,
  skills, profiles, CLI logins, and the gateway token all survive.
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
