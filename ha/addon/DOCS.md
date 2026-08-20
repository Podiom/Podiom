# Podiom

Podiom runs a team of agentic AI colleagues — powered by the `claude` and
`codex` CLIs — with a web UI for chatting, permission prompts, plans,
schedules, and memory. This add-on packages the full Podiom stack for Home
Assistant, so by default you get safe internet access to it (e.g. from your
phone via Nabu Casa) without opening a port. Native-app LAN access is a separate
opt-in described below.

## What's inside the container

- **podiomd** — the Podiom daemon and web UI (served through Ingress)
- **podiom** — the Podiom CLI
- **claude** (`@anthropic-ai/claude-code`) and **codex** (`@openai/codex`)
- **mcp-proxy** — bridges remote MCP servers for the CLIs
- **uv / uvx** — runs MCP servers distributed as Python packages (e.g. `uvx some-mcp`)
- **ttyd** — the web terminal behind the `/terminal/...` onboarding links
- **Node and Python** — the runtimes the above are built on, and also the first
  two entries in **Language toolchains** below

Exact bundled versions are listed in the changelog for every release.

## Installation

1. Add the repository to your add-on store:
   **Settings → Add-ons → Add-on store → ⋮ → Repositories** and add
   `https://github.com/Podiom/ha-app`.
2. Install **Podiom** and start it. It appears in the sidebar.

Existing installs that already use `https://github.com/Podiom/homeassistant-addons`
continue through GitHub's repository redirect. Do not add a new repository at
the old name.

## First run, step by step

1. **Start the add-on** and open **Podiom** from the sidebar.
2. The UI opens a Home Assistant setup page with an embedded terminal.
3. **Run Onboard.** The embedded wizard verifies the bundled Claude/Codex
   CLIs, guides you through device login when needed, lets you choose a
   provider/profile, creates your first agent, and generates its `SOUL.md`.
4. When the wizard finishes, press **Take the stage**. The setup page stores
   the gateway token and opens the dashboard.

After setup, Podiom opens directly to the dashboard from any HA-authenticated
browser. The **Terminal** sidebar item stays available for later Claude/Codex
re-authentication or shell access.

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

## Connecting the Podiom iOS or Android app

The Home Assistant sidebar address is an Ingress page and cannot be used as the
server address in the standalone Podiom mobile app. Enable the add-on's separate
API-only LAN port instead:

1. Open this add-on's **Configuration** tab.
2. Expand **Network** / **Show disabled ports**.
3. Enter a free host port beside **Podiom mobile API** (`8100/tcp`). We
   recommend `8787`.
4. Save and restart the add-on.
5. Copy the **Gateway token** from this page.
6. In the mobile app enter `http://<HOME-ASSISTANT-LAN-IP>:<HOST-PORT>` and the
   token — for example `http://192.168.1.7:8787`.

Do not include the HA port `8123` or a sidebar path such as
`/6142004a_podiom`. The mobile listener exposes only Podiom's health, API, and
WebSocket endpoints; the dashboard, setup flow, and terminal stay behind HA
Ingress. The add-on does not advertise this endpoint over mDNS, so enter it
manually.

This connection is plain HTTP and sends the gateway token over your network.
Enable it only on a trusted LAN. For access away from home, continue using the
HA web UI through HTTPS/Nabu Casa; the native app does not currently log in to
Home Assistant.

## Language toolchains

Unlike a standalone Podiom install, this container is sealed: your agents
cannot `apt install` a compiler, and anything written outside `/data` is lost
on the next add-on update. So the toolchains available to agents are a
**Configuration page setting** instead.

Tick what you need under **Language toolchains** and save.

| Toolchain | Provides | Approx. disk |
| --- | --- | --- |
| `go` | `go`, `gofmt` | ~265 MB |
| `node` | `node`, `npm`, `npx` | built in |
| `python` | `python`, `python3`, `pip` | ~90 MB |
| `rust` | `rustc`, `cargo`, `rustup` | ~1.5 GB |
| `swift` | `swift`, `swiftc`, `swift build`, `swift test`, SwiftPM | ~3.3 GB (1 GB download) |

**How it behaves**

- Saving the option **restarts the add-on** — that is how the new list is read
  and how the toolchains reach every agent's `PATH`.
- The install then runs **in the background**, so the UI is available
  immediately. Progress, and any failure, appear in the add-on's **Log** tab.
  A failed install is retried on the next restart.
- Toolchains live on `/data/podiom/toolchains/`, so they survive restarts,
  add-on updates, and land in your Home Assistant backups.
- **Removing one from the list deletes it** and frees the space.
- Podiom refuses an install that would not leave ~500 MB free on `/data`, and
  says so in the log. Worth knowing if you run Home Assistant from an SD card.

**`node` cannot be removed.** It is listed so the set is honest about what the
container has, but the bundled `claude` and `codex` are npm packages that run
on it — unticking it is refused and logged. `python` has no such constraint: a
ticked Python is installed to `/data` and takes precedence for your projects,
while `mcp-proxy` and `uv` keep using their own bundled interpreter.

**Swift here does not mean iOS builds.** You get the open-source toolchain —
SwiftPM packages, shared logic modules, `swift build` and `swift test`.
Building `.app` bundles, running the iOS Simulator and code signing all require
Xcode, which is macOS-only and cannot run in this container. For those steps,
point your agent at a Mac (SSH, or a macOS CI runner).

**If an agent asks for a toolchain**, it files a CLI-tool access request.
Approving it acknowledges the request — Podiom cannot install a system-wide
toolchain on your behalf — then you tick the box here and save.

## Backups — free, and worth protecting

Everything Podiom knows lives on `/data`: sessions and history, agent
SOUL/MEMORY files, plans, projects, schedules, skills, profiles, the CLI
logins, the gateway token, and your git SSH key. The non-root `podiom` account's
environment and passwd home both point to `/data/home`, so Git reads
`/data/home/.gitconfig` and OpenSSH reads `/data/home/.ssh`. Upgrading from an
older root-based release migrates their ownership without widening private-key
permissions.
Home Assistant's native backups therefore capture and restore a **complete**
Podiom state with no extra setup — treat this as a feature and back up
regularly.

Because those backups contain **CLI credentials, your git SSH private key, and
the gateway token**, we strongly recommend **password-protected backups**
(Settings → System → Backups).

## Security honesty notes

- **The terminal runs as the non-root `podiom` account.** Anyone who can open a
  `terminal/...` link can still reach all persistent Podiom data:
  `$PODIOM_HOME`, every profile's credentials, SSH keys, and the gateway token.
  It cannot write root-owned system paths in the container. These links sit
  behind the same HA login as the rest of the add-on — which means **your HA
  account's security IS your Podiom security**. Enable multi-factor
  authentication on your Home Assistant users.
- Podiom is single-user and fully trusted by design; the add-on does not
  change that model, it just fences it behind HA.
- The full web server and terminal accept connections only from the Ingress
  proxy. The separate API-only mobile port is disabled by default, restricted
  to local/private source ranges, and requires the gateway token when enabled.

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

The image **pins** exact versions of `claude`, `codex`, `mcp-proxy`, `uv`, and
`ttyd`, and disables the CLIs' self-updaters — you always run the combination
we tested. New versions ship as add-on updates; the changelog lists the
bundled versions of each release and calls out CLI version changes, since CLI
flags Podiom depends on can shift between CLI releases. Your `/data` state
(including logins) survives every update.
