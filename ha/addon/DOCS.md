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
2. The UI asks for the **gateway token**. Open the add-on's
   **Configuration** page — the token is in the *Gateway token* field
   (Podiom put it there for you). Copy it.
3. **Log in the CLIs.** Return to the Podiom token screen and use the
   **Claude terminal** and **Codex terminal** buttons. Add a profile name first
   if you want a profile-scoped login. Each button opens a dedicated onboarding
   terminal outside the UI and drops you straight into that CLI's login flow:
   - **claude**: follow the printed URL in your own browser, then paste the
     code back into the terminal if prompted.
   - **codex**: a device-code flow prints a URL and a short one-time code.
     Note: *device code login* must be enabled in your ChatGPT security
     settings (or by your workspace admin).
   When the login finishes you land in a shell, and a link back to Podiom is
   printed.
4. **Paste the gateway token** into the token screen. Each browser only asks
   once.
5. **Create your first agent.**

### Profiles

Profiles are named CLI login contexts (e.g. `work` and `personal`). Define
them in Podiom's config, then log each one in via a profile-scoped onboarding
link from the token screen. The underlying paths are
`terminal/claude/<profile-name>` and `terminal/codex/<profile-name>`; the same
path works for the first login and for re-logins later.

## The gateway token

Home Assistant's login protects the *browser → HA* hop; the gateway token
protects the *client → Podiom* hop. It is generated automatically on first
start and mirrored to this add-on's **Configuration** page.

- **Reading it:** Configuration page only. It is deliberately never printed
  to the add-on **log**.
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
