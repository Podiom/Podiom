# Podiom

Podiom runs a team of agentic AI colleagues — powered by the `claude` and
`codex` CLIs — with a web UI for chatting, permission prompts, plans,
schedules, and memory. This add-on packages the full Podiom stack for Home
Assistant, so you get safe internet access to it (e.g. from your phone via
Nabu Casa) without opening a single port yourself.

## What's inside the container

- **podiomd** — the Podiom daemon and web UI (served through Ingress)
- **podiom** — the Podiom CLI
- **claude** (`@anthropic-ai/claude-code`) and **codex** (`@openai/codex`)
- **mcp-proxy** — bridges remote MCP servers for the CLIs
- **ttyd** — the web terminal behind the `/terminal/...` onboarding links

Exact bundled versions are listed in the changelog for every release.

## Installation

1. Add the repository to your add-on store:
   **Settings → Add-ons → Add-on store → ⋮ → Repositories** and add
   `https://github.com/Podiom/homeassistant-addons`.
2. Install **Podiom** and start it. It appears in the sidebar.

## First run, step by step

1. **Start the add-on** and open **Podiom** from the sidebar.
2. The UI opens a Home Assistant setup page with an embedded terminal.
3. **Run Onboard.** The embedded wizard verifies the bundled Claude/Codex
   CLIs, guides you through device login when needed, lets you choose a
   provider/profile, creates your first agent, and generates its `SOUL.md`.
4. When the wizard finishes, press **Take the stage**. The setup page stores
   the gateway token in this browser and opens the dashboard. Each browser only
   needs this once.

After setup, Podiom opens directly to the dashboard. The **Terminal** sidebar
item stays available for later Claude/Codex re-authentication or shell access.

### Re-authenticating later

Use **Terminal** → Shell, then run:

```sh
claude /login
codex login --device-auth
```

For a profile-scoped login, create the directory yourself and prefix the CLI's
environment variable:

```sh
mkdir -p /data/home/.claude-work
CLAUDE_CONFIG_DIR=/data/home/.claude-work claude /login

mkdir -p /data/home/.codex-work
CODEX_HOME=/data/home/.codex-work codex login --device-auth
```

## The gateway token

Home Assistant's login protects the *browser → HA* hop; the gateway token
protects the *client → Podiom* hop. It is generated automatically on first
start and copied into the browser at the end of setup.

- **Reading it:** first-run setup copy step or `podiom token show` inside the
  container. It is deliberately never printed to the add-on **log**.
- **Rotating it:** turn on **Rotate gateway token** on the Configuration page
  and save. The add-on restarts, rotates the token, updates the field, and
  resets the toggle. Open browser tabs are disconnected and ask for the new
  value; the `podiom` CLI inside the container picks it up automatically.
- The *Gateway token* field looks editable but is managed by Podiom — edits
  are overwritten with the real value on the next start.

## Backups — free, and worth protecting

Everything Podiom knows lives on `/data`: sessions and history, agent
SOUL/MEMORY files, plans, projects, schedules, skills, profiles, the CLI
logins, and the gateway token. Home Assistant's native backups therefore
capture and restore a **complete** Podiom state with no extra setup — treat
this as a feature and back up regularly.

Because those backups contain **CLI credentials and the gateway token**, we
strongly recommend **password-protected backups**
(Settings → System → Backups).

## Security honesty notes

- **The terminal is root in the container.** Anyone who can open a
  `terminal/...` link can reach everything: `$PODIOM_HOME`, every profile's
  credentials, and the gateway token. These links sit behind the same HA
  login as the rest of the add-on — which means **your HA account's security
  IS your Podiom security**. Enable multi-factor authentication on your Home
  Assistant users.
- Podiom is single-user and fully trusted by design; the add-on does not
  change that model, it just fences it behind HA.
- The add-on's web server only accepts connections from the Ingress proxy;
  no port is published on your network.

## Resource honesty (Raspberry Pi & friends)

The `claude`/`codex` CLIs are cloud-backed — the model compute happens
remotely, so Pi-class hardware is viable. However, Podiom has **no
concurrency cap**: every agent turn you run in parallel spawns real processes
and consumes real RAM. On small boards, keep an eye on memory and run fewer
things at once.

## Always-on scheduling — a benefit of running under HA

Standalone Podiom only fires schedules and nightly "dreaming" while the
daemon happens to be running. Under Home Assistant the add-on starts on boot
and is watchdog-supervised, so schedules fire reliably and missed dreams
catch up after a reboot. If you use schedules, this deployment is the
dependable way to run them.

## Bundled CLI versions and updates

The image **pins** exact versions of `claude`, `codex`, `mcp-proxy`, and
`ttyd`, and disables the CLIs' self-updaters — you always run the combination
we tested. New versions ship as add-on updates; the changelog lists the
bundled versions of each release and calls out CLI version changes, since CLI
flags Podiom depends on can shift between CLI releases. Your `/data` state
(including logins) survives every update.
