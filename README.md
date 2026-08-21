<div align="center">

<img src="web/public/podium-mark-teal.svg" alt="Podiom mark" width="64">

# Podiom

**Your AI agents, in concert.**

Podiom is an open-source, local-first workspace for Claude Code and OpenAI Codex.
Give each agent a name and durable context, then chat, schedule work, or hand it
a goal to pursue over time. Podiom keeps the sessions, projects, tasks, progress,
and decisions together while the native CLIs do the work.

[![CI](https://github.com/Podiom/Podiom/actions/workflows/ci.yml/badge.svg)](https://github.com/Podiom/Podiom/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Podiom/Podiom?style=flat-square)](https://github.com/Podiom/Podiom/releases/latest)
[![License](https://img.shields.io/github/license/Podiom/Podiom?style=flat-square)](LICENSE)
[![Local-first](https://img.shields.io/badge/local--first-%E2%9C%93-C9A24E?style=flat-square&labelColor=3A3430)](docs/security.md)

[Goals](docs/goals.md) · [Get started](#get-started) · [Documentation](#documentation) · [Releases](https://github.com/Podiom/Podiom/releases/latest)

</div>

<p align="center">
  <img src="docs/assets/screenshots/goal-timeline.png" alt="A Podiom goal with success criteria, a progress metric, and an auditable activity timeline" width="100%">
</p>

<p align="center"><sub>A goal keeps the outcome, progress, and evidence in one place.</sub></p>

## What is Podiom?

Claude Code and Codex are easy to start in a terminal. They are harder to
manage once you have several sessions, projects, and unattended jobs running at
the same time. Context ends up spread across terminal tabs, and you spend time
reconstructing what happened and what an agent should do next.

Podiom puts those agents in one local workspace. Sessions stay available after
the backing CLI session changes. Projects carry shared context. Roadmap tasks
and schedules keep work moving. You can see which agent did the work, open the
session that produced it, and continue from there.

Podiom does not replace the provider runtime. It shells out to the native
`claude` and `codex` CLIs and uses their models, MCP servers, tools, skills, and
authentication.

## Hand over an outcome

A task tells an agent what to do once. A [goal](docs/goals.md) gives one lead
agent an outcome to own over days or weeks. Describe what you want, define what
"done" means, add optional metrics, and choose how often the agent should review
its progress.

- The lead agent turns the outcome into roadmap tasks and schedules, delegates
  work when useful, and changes the plan as it learns.
- Each review records progress, evidence, metric changes, and the next step the
  agent intends to take.
- Missing capabilities become structured access requests. Work that only you
  can do becomes an action item instead of disappearing into a chat transcript.
- Every run and tool call stays attached to the goal. The agent can propose
  completion, but only you can mark the goal done.

> [!IMPORTANT]
> Goals are deliberately autonomous. The lead agent and every linked task or
> schedule run with full access and without per-action approval prompts. Podiom
> records the resulting tool activity on the goal timeline. Read the
> [Goals guide](docs/goals.md#goals-run-in-yolo-mode) before assigning a goal.

## Run the whole team from one place

### Give agents a durable identity

Each agent has a name, workspace, identity, provider, model, profile, permission
mode, and fallback defaults. Claude and Codex agents can work side by side
without flattening their provider-specific behavior.

<p align="center">
  <img src="docs/assets/screenshots/agents-dashboard.png" alt="Podiom's roster of named Claude Code and Codex agents" width="100%">
</p>

<p align="center"><sub>Named agents keep their own identity, workspace, and runtime defaults.</sub></p>

### Keep the conversation

Podiom stores a canonical history for every session. If you switch provider or
profile, it can replay that history onto a fresh backing CLI session. Scheduled
runs and roadmap tasks create normal sessions too, so unattended work is not a
dead-end log.

<p align="center">
  <img src="docs/assets/screenshots/agent-chat-session.png" alt="A project-linked Podiom chat with durable history, provider controls, and usage indicators" width="100%">
</p>

<p align="center"><sub>Open the exact session behind a task, schedule, or goal run and continue the conversation.</sub></p>

### Share work across agents

Projects give every agent the same source context and standing instructions.
The Roadmap holds assignable tasks, while the embedded scheduler handles
recurring routines and timed pickups. Agent-created work keeps its provenance,
so you can trace it back to the decision that created it.

### Keep the control plane local

Podiom stores its state under one configurable local root and ships the web UI
inside `podiomd`. It runs on macOS, Linux, and Windows, and the same core can run
as a Home Assistant add-on or in a container. Provider CLIs keep their native
authentication and policy controls. The native iOS and Android apps can connect
to standalone daemons or an opt-in, API-only Home Assistant LAN endpoint; see
the [mobile](docs/mobile.md) and [Home Assistant](docs/home-assistant.md) guides.

## Who is this for?

- Developers already using Claude Code or OpenAI Codex locally.
- Builders who want persistent, reviewable agent work instead of disposable
  terminal sessions.
- Maintainers who need recurring jobs, project context, and an audit trail in
  one lightweight workspace.
- People who prefer local state and native tools over moving their workflow
  into another hosted agent runtime.

## Get started

Install the latest release on macOS or Linux:

Homebrew users can install Podiom with `brew install Podiom/podiom/podiom`.

```sh
curl -fsSL https://github.com/Podiom/Podiom/releases/latest/download/install.sh | bash
```

On Windows PowerShell:

```powershell
irm https://github.com/Podiom/Podiom/releases/latest/download/install.ps1 | iex
```

The installer downloads the matching release binary, verifies its checksum,
can set up user-level autostart, and launches `podiom onboard` to check Claude
and Codex and create your first agent. Linux releases are distro-neutral static
binaries.

Open http://127.0.0.1:8787 after onboarding. Updates are available from the CLI
or web UI:

```sh
podiom update check
podiom update apply --yes
```

## Development

Prerequisites: Go 1.26+ and Node 20+.

```sh
# Build the Vite web UI and both binaries into bin/ with a version stamp.
make build

# Run the daemon in the foreground. It scaffolds ~/.podiom on first run.
./bin/podiomd

# In another shell, check that it is live.
./bin/podiom status
```

Open http://127.0.0.1:8787 for the web UI.

For frontend development with hot reload, run `npm run dev` in `web/`. Vite
proxies API and WebSocket traffic to a running `podiomd`.

Every commit to `master` publishes a GitHub Release using the monotonic
`v0.1.<run-number>` series. The number identifies the release workflow run; it
does not imply a calendar cadence.

### Cross-platform builds and packaging

`podiomd` embeds the SPA and uses pure-Go SQLite, so it needs no external web
assets or cgo at runtime:

```sh
make cross    # linux/darwin/windows x amd64/arm64 -> bin/<os>-<arch>/
make package  # release archives in dist/ plus SHA256SUMS
```

All runtime state lives under one overridable root. Running Podiom as a Home
Assistant add-on or in a container is a packaging concern rather than a core
rewrite:

```sh
PODIOM_HOME=/data/podiom ./bin/podiomd
```

The web bind is configurable in `config.yaml` through `server.bind` and
`server.port`. It defaults to `127.0.0.1:8787`; see
[Configuration](docs/configuration.md).

## Layout

```text
cmd/podiom/     thin CLI client
cmd/podiomd/    daemon: web server, scheduler, and core
internal/       core, adapters, execution, scheduling, config, store, server, client
web/            Svelte, Vite, TypeScript, and Tailwind SPA, built into podiomd
docs/           requirements, references, and integration contracts
```

Runtime state lives under `$PODIOM_HOME`, which defaults to `~/.podiom/`.

## Documentation

- [Requirements](docs/requirements/foundation.md), the authoritative foundation spec (v1.6)
- [CLI reference](docs/cli.md)
- [Configuration](docs/configuration.md)
- [Agents](docs/agents.md), durable named colleagues and their stored defaults
- [Git](docs/git.md), source control for projects
- [Sessions](docs/sessions.md), the durable conversation unit
- [SOUL.md generation](docs/soul-generation.md), agent identity files
- [Scheduling](docs/scheduling.md), recurring routines and timed work
- [Projects and Roadmap](docs/projects.md)
- [Goals](docs/goals.md), outcomes an agent plans, reviews, and reports back on
- [Agent tools](docs/agent-tools.md), what agents can do with Podiom itself
- [Workspace tools](docs/workspace-tools.md), approved per-agent CLI installs
- [Notifications](docs/notifications.md), how schedules, goals, and stuck agents reach you
- [Voice input](docs/voice-input.md), prompts spoken through OpenAI Whisper
- [Photo attachments](docs/photo-attachments.md), retained photos for agents to inspect
- [Security and logging](docs/security.md), permission modes, tokens, redaction, and run logs
- [Mobile apps](docs/mobile.md), the Capacitor iOS and Android clients
- [Home Assistant app](docs/home-assistant.md), Podiom as a Home Assistant add-on
- [WebSocket contract](docs/websocket.md), the browser-native `/api/ws` endpoint
- [Integration contracts](docs/integrations/README.md)

## Contributing

Podiom is open source under the [MIT License](LICENSE). Contributions are
welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) for setup, validation, and pull
request guidelines.
