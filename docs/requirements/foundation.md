# Podiom — Initial Requirements

*A thin orchestration layer for local LLM agents (Claude, Codex). Leans on the
native capabilities of the underlying CLIs — MCP, tools, memory — while
maintaining durable, named agents and sessions of its own.*

Status: **v1.6 — pre-handover consistency pass.** Resolved the Claude
instruction-delivery mechanism (generated Podiom-managed `CLAUDE.md` with
cross-directory `@`-imports, or `--append-system-prompt-file`), guarded against
Codex double-loading, clarified that fallback entries are provider-carrying
profiles, and fixed decision-log cross-references. Functionally builds on v1.5
(projects + schedules at system level). See §12 for the full decision log.

---

## 1. Product principles

These constrain every later decision. When a requirement is ambiguous, resolve
it in favour of the principle.

1. **Thin, not invasive.** Podiom orchestrates; it does not replace. It shells
   out to the local `claude` and `codex` CLIs and relies on *their* MCP config,
   tools, and memory rather than reimplementing them.
2. **Podiom owns its own truth.** A Podiom session is durable. The underlying
   CLI session is a fleeting backing resource that can be torn down and
   recreated (e.g. on a profile or provider switch) without losing the Podiom
   conversation.
3. **One place to see everything.** Sessions, schedules, agents, and run history
   are all observable from a single CLI and a single web UI sharing one core.
4. **Cross-platform by construction.** Windows, Linux, and macOS are
   first-class. Platform differences are isolated to a thin `exec`/paths layer.
5. **Config as data.** System configuration is declarative YAML. Integration
   specifics live in a separate, documented markdown contract.
6. **Lean on native policy, don't seize it.** The Claude/Codex reference in
   Appendix A documents an OpenClaw-style integration that is deliberately
   *invasive*: it bypasses Claude permissions (`bypassPermissions`), forces a
   strict host-only MCP surface (`--strict-mcp-config` + an `mcp__openclaw__*`
   allow-list), and runs Codex with `approvalPolicy: "never"` +
   `sandbox: "danger-full-access"`. Podiom uses the same *mechanisms* but the
   opposite *defaults*: it inherits the CLI's own MCP servers, tools, memory,
   permission prompts, and sandbox unless the user opts into tighter control.
   Podiom is a conductor, not a replacement runtime. See §8.5 for the explicit
   mapping of where Podiom and OpenClaw diverge.
7. **Deployment-target neutral.** `podiomd` must not assume it owns its process
   lifecycle, network exposure, or storage location. v1 ships **standalone**, but
   the core is built so a **Home Assistant add-on** (container, supervisor-managed,
   ingress-fronted web UI, mapped storage volume) is later a packaging concern,
   not a rewrite. Concretely: the storage root is overridable (`PODIOM_HOME`,
   defaulting to `~/.podiom/`), the web bind/ingress is configurable, and the core
   never hard-codes how it was started. (§2 lists HA as a future delivery form.)

---

## 2. Scope (v1)

### In scope
- Integrate with **Claude** and **Codex** local CLIs.
- Durable chat sessions, each currently backed by one CLI session/thread.
- Agents maintained by Podiom (identity, defaults, optional per-agent config).
- **Per-agent workspace** (agent-local scratch) plus a **shared, system-level
  project structure** (`~/.podiom/projects/`) that agents collaborate on.
- **Per-agent identity** via `SOUL.md` plus layered `AGENTS.md` instructions
  (base + per-agent), composed by Podiom and delivered to each backend.
- **Two permission modes** (`approve` default, `yolo` opt-in) mapped onto each
  provider's native approval/sandbox controls.
- Slash-command-driven chat (`/model`, `/effort`, `/profile`, …).
- Mid-chat model, effort, and profile switching, with history replay on switch.
- **Optional profiles** (named, isolated auth contexts) and an **automatic
  fallback chain** across profiles and providers on rate limit.
- Automatic session naming + description after the first message.
- Embedded scheduler (Alternative A) for recurring agent routines.
- CLI and web interface over a shared core.
- YAML system configuration.

### Out of scope (v1, noted for later)
- Inter-routine dependencies / DAG workflows (schedules are independent for now).
- OS-level service installation for boot persistence (`podiom service install`).
- LLM/CLI integrations beyond Claude and Codex.
- Multi-user / remote access (single local user assumed).
- **Home Assistant add-on** packaging (standalone only in v1; core is built to
  allow it later — Principle 7).
- **Channel integrations** (Telegram, Slack, etc.). Not built in v1, but the
  session data model carries a channel-origin attribute now so sessions can later
  start in one channel and continue in another (§4 R4.10–R4.11).

---

## 3. Architecture overview

```
OS (manual start in v1; service install deferred)
       |
   podiomd  (long-running daemon)
     |-- core orchestration  (session state, agent state — source of truth)
     |-- CLI adapter layer    (claude / codex — pluggable integrations)
     |-- embedded scheduler   (cron / every routines)
     |-- web server + WebSocket (live streaming to browser)
     |
   podiom  (CLI) -- thin client; always connects to a running podiomd (R11.1)
```

Proposed layout:

```
podiom/
  internal/core/        # sessions, agents, message history — source of truth
  internal/adapter/     # claude + codex adapters behind one interface
  internal/exec/        # cross-platform subprocess + binary discovery
  internal/schedule/    # embedded cron/every scheduler
  internal/config/      # YAML loading + validation
  internal/store/       # durable persistence of sessions/history
  cmd/podiom/           # CLI entry (cobra)
  cmd/podiomd/          # daemon: web server + scheduler
  web/                  # Svelte+Vite SPA (TS+Tailwind); vite build → embedded via go:embed
  docs/integrations/    # the integration contract markdown (see §8)
```

Runtime data layout (all under a single root, R9.1):

```
~/.podiom/
  config.yaml           # system configuration
  AGENTS.md             # Podiom-owned base instructions (ships with install)
  podiom.db             # SQLite: sessions, history, rolling summaries
  agents/<name>/
    SOUL.md             # user identity (always created)
    AGENTS.md           # user per-agent instructions (optional)
    workspace/          # agent-local cwd; a generated per-backend context file
  projects/             # SHARED across agents: projects.yaml + one dir per project
  schedules/            # self-describing job files (*.md with frontmatter)
  profiles/             # optional profile config dirs (CLAUDE_CONFIG_DIR/CODEX_HOME)
```

### Two process models behind one interface

The single most important architectural consequence of Appendix A is that the
two providers have **fundamentally different process lifecycles**, and the
adapter interface must accommodate both:

- **Claude — per-turn process.** Each turn spawns `claude -p` with
  `--input-format stream-json` / `--output-format stream-json`. The process is
  short-lived; continuity comes from persisting the Claude **session ID** and
  passing `--resume <id>` on the next turn.
- **Codex — long-lived app-server.** A single `codex app-server --listen
  stdio://` process stays alive, and Podiom speaks a JSON-RPC-style protocol to
  it (`thread/start`, `thread/resume`, `turn/start`). Continuity comes from
  persisting the Codex **`threadId`**; the process must be restarted/reconnected
  on failure.

The `adapter` interface therefore abstracts over "send a turn, stream the
result, give me back a resumable handle" — not over a single uniform process
shape. (See D7 in §12.)

### Technology stack (decided)

- **R3.1** Podiom is written in **Go**, chosen for: first-class subprocess
  handling (`os/exec`) for driving the local CLIs; goroutines + channels for
  concurrent per-agent execution; a single statically linked binary per OS;
  built-in `net/http` + WebSocket for the web UI sharing one core; and clean
  cross-compilation to Windows/Linux/macOS.
- **R3.2** Libraries/tooling anticipated (not locked, but the intended direction):
  `cobra` (CLI), `robfig/cron` (scheduler, §7), a **pure-Go SQLite driver**
  (`modernc.org/sqlite`) to avoid cgo and keep cross-compilation and container
  packaging simple (R11.2), and `kardianos/service` for the deferred OS-service /
  boot-persistence feature (§2).
- **R3.5** **Web frontend stack:** **Svelte + Vite + TypeScript + Tailwind**, with
  the browser-native **WebSocket API** for live streaming. Rationale: the core UX
  is a local, stateful, real-time chat (token streaming + inline approve/deny
  permission prompts, R6.5), which favours a reactive client over server-rendered
  fragments; Svelte's compile-away model keeps the bundle and runtime footprint
  small.
  - **Plain Svelte + Vite, not SvelteKit.** The app is a **single-page app built
    to static assets**; no SSR and no Node runtime. `vite build` output is
    embedded into the Go binary via `embed` and served by `podiomd` — one binary,
    no separate frontend process. This keeps `~/.podiom/` distribution and the
    future Home Assistant container (Principle 7) simple. Routing is client-side;
    TypeScript types mirror the server data model (sessions with origin/schedule
    provenance, profiles, permission-request payloads) over a typed WebSocket
    message contract.
- **R3.3** Cross-platform care points identified for the `exec`/paths layer:
  robust binary discovery for `claude`/`codex` including Windows npm `.cmd`/`.exe`
  shims, OS-aware path handling, and context-based cancellation + platform process
  groups for killing hung agents (§10).
- **R3.4** Pure-Go dependencies (notably the SQLite driver) and the single static
  binary directly support Principle 7: they make a Home Assistant container
  add-on a packaging exercise rather than a port.

---

## 4. Sessions and the durability model

This is the core concept and the source of most subtlety.

- **R4.1** A Podiom **session** is the durable unit. It owns the full ordered
  message history (user + assistant turns), the bound agent, and current
  settings (model, effort, profile).
- **R4.2** A session is, at any moment, backed by exactly one underlying CLI
  session/thread. This backing is **rebindable**: it can be replaced without
  destroying the Podiom session.
- **R4.3** On a **profile or provider switch mid-chat**, Podiom MUST create a new
  underlying CLI session/thread under the new profile (or provider) and **replay
  the existing Podiom history** into it so the conversation continues seamlessly.
  Switches are common due to rate limits — this path must be robust, not an edge
  case. (Profiles are defined in §8.7; fallback in §8.8.)
- **R4.4** The same replay mechanism applies whenever the backing session is
  lost or invalidated (CLI restart, crash, expiry).
- **R4.5** Because Podiom holds the canonical history, the underlying CLI's own
  session memory is treated as a cache, not the source of truth.
- **R4.6** Replay is **provider-agnostic**: because Podiom owns the canonical
  history, a switch target may be a different profile *of the same provider* or a
  *different provider entirely* (Claude→Codex). The fallback chain (§8.8) relies
  on exactly this.

### Auto-naming
- **R4.7** After the **first user message** (and its response), Podiom MUST
  generate a concise **name** and **description** for the session.
- **R4.8** Naming is **non-blocking**: the session is fully usable immediately;
  name/description populate asynchronously when ready.
- **R4.9** *(D3 — resolved.)* Naming uses the **session's own model** (no
  separate utility model in v1), but the naming call is issued at **low/minimal
  effort** to keep it cheap and fast even when the session itself runs at high
  effort (Claude `--effort low`; Codex low `effort` on that turn). A configurable
  dedicated `naming_model` may be added later.

### Session origin and provenance
- **R4.10** Every session records a **channel of origin** — where it was created
  (e.g. `web`, `cli`, `schedule`, and later `telegram`, `slack`). Origin is set
  at creation and **never changes**; it is *provenance*, shown visually so the
  user can see "this session was born in X".
- **R4.11** Origin is **not binding**: a session may be continued from a
  **different channel** than it started in (start in Telegram, continue in web).
  This works because the session — not the transport — owns the canonical history
  (R4.5); a channel is just another interface onto the session, parallel to how
  profile/provider are interchangeable backends. (Channel integrations beyond
  web/cli/schedule are future scope, §2; the origin attribute is captured now so
  the data model needn't change later.)
- **R4.12** **Schedule provenance.** A session created by a scheduled run records
  which **schedule** and which **run** produced it, so the user can **revisit
  that session and continue working in it manually**. (Ties to §7 R7.9.)

---

## 5. Agents

### 5.1 Definition & defaults
- **R5.1** Agents are defined and maintained by Podiom (name, identity, default
  provider, default model, default effort, permission mode, optional config
  pointers).
- **R5.2** A new chat session binds to an agent and inherits its defaults, which
  the user can then override per-session via slash commands.
- **R5.3** Agents rely on the underlying CLI's own MCP config, tools, and memory
  **by default** (Principle 6 / §8.4).
  - **R5.4** *(O1 — resolved: additive)* When an agent is given its own MCP
    servers/tools, they are **added on top of** the globally inherited ones — the
    agent sees both its own and the user's native tools (`--mcp-config` without
    `--strict-mcp-config` on Claude; additive MCP config on Codex). A future
    `tools: strict` per-agent option may be added to replace rather than augment,
    but v1 is additive only.

### 5.2 Per-agent directory (workspace)
- **R5.5** Every agent gets its own **agent directory** under
  `~/.podiom/agents/<name>/`, created by Podiom when the agent is first defined.
  It holds the agent's identity/instruction sources and its **workspace**.
  (Schedules and projects are **system-level**, not per-agent — see §7 and §5.3.)
- **R5.6** The agent's **`workspace/`** subdirectory is the **`cwd`** of the
  agent's CLI process: Claude's working directory (where `claude -p` runs);
  Codex's `cwd` on `thread/start` / `turn/start`. It is the agent's own scratch
  space. Shared, cross-agent work lives in `~/.podiom/projects/` (§5.3); the
  workspace is for agent-local material. This keeps Podiom lean — native relative
  paths, sandbox roots, and context-file discovery all follow from `cwd` with no
  artificial injection.
- **R5.7** Podiom scaffolds the initial structure on creation:

  ```
  ~/.podiom/
    AGENTS.md                  # PODIOM-owned base instructions, ships with install (§5.4)
    agents/<name>/
      SOUL.md                  # USER-owned identity, always created (empty skeleton)
      AGENTS.md                # USER-owned per-agent instructions, OPTIONAL (user-created)
      workspace/               # the agent's cwd (§5.6) — agent-local scratch space
    projects/                  # SYSTEM-level, shared across agents (§5.3)
      projects.yaml
    schedules/                 # SYSTEM-level, self-describing job files (§7)
  ```

  `SOUL.md` and the optional per-agent `AGENTS.md` live at the **agent root**
  (not inside `workspace/`), since they are identity/instruction sources Podiom
  composes, not work product. Podiom generates whatever per-backend context file
  is needed inside `workspace/` at run time (an `@`-importing file for Claude, or
  a concatenated bundle for Codex) — see §5.4 / §8.4a.

- **R5.8** *(Resolved: shared access, no isolation.)* Agents are **not** isolated
  from each other. Every agent can read and write **every other agent's
  workspace**, in both `approve` and `yolo` modes. All agent directories live
  under a common root (`~/.podiom/agents/`) and that whole root is accessible to
  every agent, enabling cross-agent collaboration on shared projects.
  - This is a deliberate choice for a single-user, fully-trusted setup. It means
    workspace boundaries are **not** a security barrier: a compromised or
    prompt-injected agent can affect other agents' work. See §5.5 and §12 (D11)
    for the risk this accepts.

### 5.3 Shared project ledger (`~/.podiom/projects/`)
Projects are **agent-independent, system-level resources** — modelling how a real
company works, where several people (here, agents) collaborate on the same
codebase, book, or initiative. Projects are **not** owned by any one agent.

- **R5.9** Projects live under `~/.podiom/projects/`, with a **single shared
  `projects.yaml`** ledger and one subdirectory per project. Any agent can read
  and work on any project. Podiom creates an initial empty ledger at scaffold
  time; agents maintain it as they create and work on projects.
- **R5.10** Each project entry has the shape (note `path` is now relative to
  `~/.podiom/projects/`):

  ```yaml
  projects:
    - id: mission-control
      name: Mission Control
      description: Next.js web application. Likely a monitoring/dashboard UI.
      path: mission-control
      status: active
      stack:
        - Next.js 16
        - React 19
        - Tailwind CSS 4
        - TypeScript
      repo: null
      roadmap: [] # derived roadmap task IDs
      notes: >
        Bootstrapped with create-next-app. Uses Next.js 16 which has breaking
        changes from prior versions — read node_modules/next/dist/docs/ before
        modifying.
  ```

- **R5.11** Agents MUST be instructed (via the base `AGENTS.md`, §5.4) to always
  work **with and against** the shared project structure whenever something is
  created or maintained — regardless of whether the artifact is a book or
  software. Creating a new thing means adding a subdirectory under
  `~/.podiom/projects/` and recording/updating its entry in the shared
  `projects.yaml`. Because the instruction lives in the base `AGENTS.md`, every
  agent inherits it automatically.
- **R5.12** **Concurrency note (v1 limitation).** Since projects are shared and
  there is no concurrency cap (D5), two agents could write `projects.yaml` at the
  same time; v1 accepts **last-write-wins** (a simultaneous update may be lost).
  Serialising writes to the ledger is a future refinement, not built in v1.

### 5.4 Agent identity (`SOUL.md`) and instructions (`AGENTS.md`)
Podiom owns the canonical instruction/identity files (under `~/.podiom/`) and is
responsible for composing them into whatever payload each backend needs. The
files are **always physically separate for the user to read and edit**;
composition into a single payload is a **delivery detail**, applied only when a
backend lacks native file-linking.

There are three layers, composed in this fixed order:

1. **Base `~/.podiom/AGENTS.md`** — *Podiom-owned*, shipped with the install,
   always applies. Holds Podiom's standing rules (project-ledger discipline,
   workspace conventions). The user does not edit this.
2. **Per-agent `~/.podiom/agents/<agent>/AGENTS.md`** — *user-owned, optional*.
   If the user creates it, it is **appended** to the base layer for that agent.
3. **Per-agent `~/.podiom/agents/<agent>/SOUL.md`** — *user-owned, always
   created* with a new agent. Holds the agent's identity/purpose. Also appended,
   **last**, so identity sits on top of the rules.

- **R5.13** **`SOUL.md` is always created** when an agent is created, shipped as
  an empty, structured skeleton (headed prompts) for the user to fill in. It is
  user-owned and never pre-populated with Podiom's rules.
- **R5.14** The **base `AGENTS.md` is Podiom-owned and ships with the install**;
  it always applies to every agent. The optional per-agent `AGENTS.md` is
  user-owned. The two never overwrite each other — they are separate files,
  combined only at compose time.
- **R5.15** **Effective instruction = base `AGENTS.md` + per-agent `AGENTS.md`
  (if present) + `SOUL.md`**, in that order. This composition is **Podiom's
  responsibility**, not the CLI's — Podiom owns the canonical files and builds
  the right payload per backend. (This is why R5 no longer depends on `CODEX_HOME`
  or any CLI's directory-walk behaviour.)
- **R5.16** **Delivery per backend:**
  - **Claude — link if possible.** Where the backend supports native file linking
    (Claude's `@path` import), Podiom emits a thin generated context file that
    `@`-imports the canonical files, keeping them physically separate all the way
    in.
  - **Codex / no linking — bundle.** Where the backend has no import mechanism,
    Podiom **concatenates** the three layers (in the R5.15 order) into the single
    file the backend reads (e.g. the `AGENTS.md` it picks up from `cwd`). The
    user still only ever sees/edits the separate canonical files; the bundle is
    generated.
- **R5.17** Net effect: the user reads and edits clean, separate files
  (`SOUL.md`, optional per-agent `AGENTS.md`); Podiom's base rules are protected
  and always applied; and the model receives one coherent instruction regardless
  of whether the backend can link or must be handed a bundle.

### 5.5 Per-agent permission mode
- **R5.18** Each agent has a **permission mode** with two values:
  - **`approve`** (default) — the agent must get approval for side-effecting
    actions. Podiom surfaces each request to the user (relay).
  - **`yolo`** (opt-in) — everything is auto-approved ("danger-full-access").
- **R5.19** Mode maps onto native controls per provider (full mapping in §8.4):
  - `approve` → Claude `--permission-prompt-tool` (Podiom relays); Codex
    `approvalPolicy: on-request` + `sandbox: read-only`, so workspace writes
    prompt consistently across providers.
  - `yolo` → Claude `--permission-mode bypassPermissions`; Codex
    `approvalPolicy: never` + `sandbox: danger-full-access`.
- **R5.20** Mode is a per-agent default and MAY be overridden per session via a
  slash command (§6). *(Scheduled runs have a stricter policy — see §7.)*
- **R5.21** *(O10 — resolved: full disk.)* `yolo` grants access to the **whole
  machine**, not just the agent's workspace. This is an explicit, informed
  opt-in: choosing `yolo` means accepting that things can go badly wrong
  (a runaway or prompt-injected agent can touch anything on disk). Podiom does
  not artificially confine `yolo`. The default `approve` mode remains the safe
  posture; `yolo` is the user's deliberate trade of safety for autonomy.

---

## 6. Chat & slash commands

- **R6.1** Chat is the primary interaction surface in both CLI and web.
- **R6.2** A set of `/`-prefixed commands control session state. Initial set:
  - `/model <name>` — set the model for this session going forward.
  - `/effort <level>` — set reasoning effort for this session going forward.
  - `/profile <name|default>` — switch the profile (auth context); triggers
    history replay into a new backing session/thread (R4.3) if changed mid-chat.
    `default` means "no profile" (global CLI login).
  - `/permission <approve|yolo>` — set the permission mode (§5.5) for this
    session, overriding the agent default.
  - `/name`, `/describe` — manually override the auto-generated metadata.
  - `/agent <name>` — *(consider)* rebind or inspect the bound agent.
  - `/help` — list available commands.
- **R6.3** `/model` and `/effort` are **session-scoped defaults** (apply to
  subsequent turns until changed). Per-message override is a possible future
  extension, not v1.
- **R6.4** Settings changes are reflected in the web UI in real time and visible
  in CLI output.
- **R6.5** In `approve` mode, a tool/permission request from the agent surfaces
  **inline in the chat** (web and CLI) as an approve/deny prompt. Podiom relays
  the user's decision back to the CLI (Claude permission MCP response / Codex
  approval response). A configurable **timeout** auto-denies unanswered requests
  (required because the Claude permission call blocks the agent — see §8.4).

---

## 7. Scheduling (Alternative A — embedded)

- **R7.1** The scheduler runs **inside `podiomd`**, sharing agent state, adapters,
  and logging with the rest of the system.
- **R7.2** A **schedule is a single self-describing markdown file** at
  `~/.podiom/schedules/<name>.md` (system-level, mirroring `projects/`, not
  per-agent). The file is the complete job: **YAML frontmatter** declares the
  job's properties; the **markdown body** is the task the agent is prompted with.
  Podiom scans `~/.podiom/schedules/` and registers each file as a job — adding a
  job means dropping a file in the folder. The config file has **no `schedules:`
  block** (the files are the source of truth).
- **R7.2a** Frontmatter fields include at least: `agent`, `model`, `effort`,
  timing (`cron: "0 7 * * *"` **or** `every: 6h`), `run_permission`
  (`preapproved`|`yolo`, §7.7), and `enabled: true|false` (the on/off switch — a
  disabled file stays in place but does not fire). Example:

  ```markdown
  ---
  agent: jared
  model: <model-name>
  effort: low
  cron: "0 7 * * *"
  run_permission: preapproved
  enabled: true
  ---

  Summarise today's calendar and add a short note to the "daily-briefs" project.
  ```

- **R7.3** The job's **agent is named in its own frontmatter**, which is exactly
  why schedules are system-level rather than per-agent: the file is
  self-sufficient and needs no external config to know which agent runs it.
- **R7.3a** Each run executes as a **normal Podiom session** against the named
  agent. It is **not** "naked": the agent runs in its `workspace/` as `cwd`, so
  the composed identity/instructions (base `AGENTS.md` + per-agent `AGENTS.md` +
  `SOUL.md`, §5.4) are delivered exactly as in an interactive chat. The schedule
  body is the task layered on top of that standing context, and `model`/`effort`/
  `run_permission` from frontmatter apply to the run.
- **R7.4** Routines are **independent** in v1 (no inter-routine dependencies).
- **R7.5** Schedules, next-run times, manual-trigger, and run history are visible
  and controllable from the web UI and CLI (the UI reads/writes the frontmatter
  files).
- **R7.6** Limitation accepted for v1: routines only fire while the machine is on
  and `podiomd` is running. Boot persistence is deferred (see §2 out-of-scope).
- **R7.9** Each scheduled run **creates a durable session** (origin = `schedule`,
  §4 R4.10) and **persists the link** between that session and the schedule + run
  that produced it (§4 R4.12). The user can therefore **revisit a scheduled run's
  session and continue it manually**, and can filter sessions by schedule (e.g.
  "all runs of `morning-calendar`").

### 7.7 Permission policy for unattended runs
- **R7.7** Scheduled runs have **no human at the keyboard**, so the `approve`
  relay (§5.5 / §6.5) cannot apply — there is no one to answer the prompt. Each
  routine MUST therefore declare how it handles permission requests:
  - **`yolo`** — run with auto-approval. Note this is **whole-machine** access
    (§5.5 R5.21) with no human oversight, so it is a strong, deliberate opt-in for
    trusted routines only, or
  - **`preapproved`** — run in `approve` mode but with a configured allow-list of
    safe actions (Claude `allowedTools`; Codex `granular` approval with safe
    categories auto-approved); anything not pre-approved is **auto-denied**, not
    queued for a human.
- **R7.8** Default for scheduled routines is the **stricter** of the two:
  `preapproved` with an empty allow-list (i.e. deny side effects) unless the
  routine explicitly opts into `yolo`. This avoids a routine silently hanging on
  an un-answerable prompt or doing something unintended unattended — especially
  important given `yolo` is unconfined (§5.5 R5.21).

---

## 8. Integration contract

A dedicated `docs/integrations/*.md` document defines, **per provider**, exactly
how Podiom drives the CLI. It is the contract the `adapter` layer implements.
The requirements below are now grounded in the reference in Appendix A. Where the
reference shows an OpenClaw value that conflicts with Podiom's principles, the
**Podiom default** column is authoritative.

### 8.1 Common adapter interface

Every provider adapter MUST expose:
- **R8.1** Binary discovery cross-platform (incl. Windows npm `.cmd` shims).
- **R8.2** Start a new session/thread and return a **resumable handle** (Claude
  session ID / Codex `threadId`).
- **R8.3** Send a turn and stream incremental events (text deltas, tool calls,
  tool results, lifecycle) back to core.
- **R8.4** Resume an existing handle.
- **R8.5** Tear down cleanly (kill per-turn process / shut down app-server).

### 8.2 Claude adapter (per-turn)

- **R8.6** Launch shape: `claude -p --input-format stream-json
  --output-format stream-json --include-partial-messages --verbose`.
- **R8.7** **Model & effort** map directly to `--model <name>` and
  `--effort <level>`. Valid effort values per the reference:
  `low`, `medium`, `high`, `xhigh`, `max`. This is the backing for `/model` and
  `/effort` (§6) on Claude sessions.
- **R8.8** **Profile** maps to a config dir: when a profile is set, Podiom
  exports `CLAUDE_CONFIG_DIR=<profile-dir>` before launching `claude`; when no
  profile is set, it leaves `CLAUDE_CONFIG_DIR` unset and the CLI uses its global
  login (§8.7).
- **R8.9** **Resume**: persist the Claude session ID; pass `--resume <id>` on the
  next turn.
- **R8.10** **Streaming I/O**: write structured user input as stream-json on
  stdin; parse stream-json events on stdout, correlating user echoes (via
  `--replay-user-messages`), assistant deltas, tool calls, and tool results.

### 8.3 Codex adapter (long-lived app-server)

- **R8.11** Launch shape: `codex app-server --listen stdio://`; maintain a
  long-lived child process and a JSON-RPC client over stdin/stdout. Restart and
  reconnect on failure.
- **R8.12** Lifecycle requests: `thread/start` (new), `thread/resume`
  (`threadId` required), `turn/start` (per user turn).
- **R8.13** **Model & effort**: `model` on `thread/start`/`turn/start`; `effort`
  on `turn/start` (reference default `medium`). Backs `/model` and `/effort` for
  Codex sessions.
- **R8.14** **Profile**: when set, export `CODEX_HOME=<profile-dir>` (the
  reference's `codexHome`); when unset, the CLI uses its global login. Mirrors
  R8.8 (§8.7).
- **R8.15** **Resume**: persist `threadId`; use `thread/resume`.
- **R8.16** Correlate streaming notifications by `threadId` and `turnId`.

### 8.4 Permission modes — verified native mechanism

Podiom exposes exactly **two** permission modes (§5.5). Both are implemented with
the providers' *native* controls — verified against current Claude Code and Codex
documentation — not by Podiom seizing the policy layer the way the OpenClaw
reference does.

| Podiom mode | Claude (per-turn) | Codex (app-server) |
| --- | --- | --- |
| **`approve`** (default) | `--permission-prompt-tool <podiom-mcp>` — Claude calls a Podiom-run MCP permission tool for any action not matched by static allow/deny rules. The call **blocks** until Podiom answers, so a **timeout** is mandatory (auto-deny). | `approvalPolicy: "on-request"` + `sandbox: "read-only"` — Codex emits an approval request over the protocol for writes/commands that cross the read-only boundary, which Podiom relays. Optionally `approvalPolicy: { granular: {…} }` to auto-handle some categories. |
| **`yolo`** (opt-in) | `--permission-mode bypassPermissions` | `approvalPolicy: "never"` + `sandbox: "danger-full-access"` |

Key facts behind this (see Appendix A + verified docs):
- **R8.17** Claude's `approve` mode requires Podiom to run a small **MCP
  permission server**. It receives `{tool_name, input, tool_use_id}`, surfaces
  the request to the user (§6.5), and returns `{behavior: "allow"|"deny",
  updatedInput?}`. `updatedInput` lets Podiom tweak a command before it runs.
- **R8.18** Because that call blocks the agent, Podiom MUST enforce a per-request
  timeout (configurable) that auto-denies, so an unanswered prompt cannot hang a
  session indefinitely.
- **R8.19** Codex separates **sandbox** (what is technically possible) from
  **approvalPolicy** (when it stops to ask) — they are independent rattar.
  `approve` mode sets both; do not conflate them.
- **R8.20** `yolo` is **whole-machine** by decision (D10 / §5.5 R5.21): Podiom does
  not anchor it to the workspace. The `approve` default is the safety boundary;
  `yolo` deliberately removes it.

### 8.4a Workspace, identity, and project ledger wiring
- **R8.21** Podiom launches each provider with the agent's **`workspace/` as
  `cwd`** (§5.6): Claude's working directory; Codex `cwd` on
  `thread/start`/`turn/start`.
- **R8.22** **Podiom composes the instruction payload itself** from its canonical
  files (base `~/.podiom/AGENTS.md` + optional per-agent
  `~/.podiom/agents/<name>/AGENTS.md` + `~/.podiom/agents/<name>/SOUL.md`, in that
  order — §5.4), then delivers it per backend. **All three canonical files live
  outside `workspace/`** (at the Podiom root and the agent root), so delivery
  must reach across directories:
  - **Claude:** Podiom writes a **generated, Podiom-managed `CLAUDE.md` inside
    `workspace/`** (which is `cwd`, where Claude auto-discovers it) containing
    `@`-imports of the three canonical files by absolute path. *(Implementation
    note: `--append-system-prompt-file` pointing at the generated file is an
    equally valid delivery path and may be preferred if cwd auto-discovery proves
    unreliable; pick one and apply it consistently. The reference in Appendix A
    uses `--append-system-prompt-file`.)* The generated file is **not
    user-editable** — Podiom overwrites it each run; users edit only the canonical
    files.
  - **Codex (no `@`-import):** Podiom writes a **concatenated bundle** of the
    three layers (R5.15 order) into the `AGENTS.md` Codex reads from `cwd`
    (`workspace/`). This generated `AGENTS.md` is likewise Podiom-managed and
    regenerated each run; it is distinct from the user's canonical per-agent
    `AGENTS.md` at the agent root.
  Either way the user only ever edits the separate canonical files; the
  per-backend artifact in `workspace/` is generated and disposable.
- **R8.22a** **Avoid double-loading on Codex.** Since `workspace/` is `cwd` and
  the user's canonical per-agent `AGENTS.md` sits one level up at the agent root,
  Podiom must ensure Codex does not also pick up that agent-root file via its
  root-to-cwd walk (which would duplicate the per-agent layer). Either keep the
  canonical per-agent `AGENTS.md` outside Codex's walk path, or account for it in
  composition so each layer appears exactly once. This is an implementation
  constraint to verify against Codex's actual file-discovery behaviour.
- **R8.23** The project-ledger instruction (§5.11) lives in Podiom's **base
  `AGENTS.md`** (Podiom-owned, ships with install), not in the user's `SOUL.md`.

### 8.4b Policy posture — Podiom vs. the OpenClaw reference

The reference's column is **not** Podiom's default; it is what `yolo` opt-in
looks like. The lean column is the default.

| Concern | OpenClaw (reference, invasive) | **Podiom default (`approve`)** | `yolo` opt-in |
| --- | --- | --- | --- |
| Permissions (Claude) | `bypassPermissions` | `--permission-prompt-tool` relay | `bypassPermissions` |
| Approvals (Codex) | `approvalPolicy: "never"` | `on-request` | `never` |
| Sandbox (Codex) | `danger-full-access` | `read-only` | `danger-full-access` |
| MCP surface | `--strict-mcp-config` + host-only config | **Inherit native MCP servers** | (unchanged) |
| Tools | `mcp__host__*` allow-list | Inherit native tools | (unchanged) |
| Identity / prompt | Large host-generated runtime block | Layered files Podiom owns and composes: base `AGENTS.md` + per-agent `AGENTS.md` + `SOUL.md`; delivered by `@`-link (Claude) or bundle (Codex) | (unchanged) |
| Workspace / `cwd` | host-managed agent workspace | **Adopted** — per-agent workspace as `cwd`; shared across agents, not a boundary (D11) | full-disk in `yolo` (D10) |
| Profile isolation | `CLAUDE_CONFIG_DIR` / `CODEX_HOME` | **Adopted** — optional named profiles back multi-account auth (§8.7) | — |
| Resume | `--resume` / `thread/resume` | **Adopted** — backs durability | — |

The bottom three rows are adopted wholesale: workspace-as-`cwd`, profile
isolation, and resume are exactly the mechanisms Podiom's workspace (§5.2),
profile (§8.7), and durability (§4) models need. Divergence is purely on
the *policy* rows, and is now a single user-facing toggle: `approve` vs `yolo`.

### 8.5 History replay and the rolling summary (D4 — resolved)

Because a profile/provider switch means a **new config dir/provider → new auth →
new underlying session/thread**, the replay in R4.3 is concrete:
- **R8.24** Claude: start a fresh session under the new `CLAUDE_CONFIG_DIR` and
  replay Podiom's canonical history as stream-json user/assistant turns before
  the new live turn.
- **R8.25** Codex: `thread/start` under the new `CODEX_HOME`, replaying history
  into the new thread, then `turn/start` for the live turn.

**Long histories — proactive summarisation with a rolling summary (O4).** Full
replay of a very long session can be costly or hit context limits, and the moment
a switch is needed (a rate limit) is exactly when there is no spare capacity to
summarise. Podiom therefore prepares ahead of time:
- **R8.26** Podiom maintains a **rolling summary** of the older portion of each
  session, refreshed periodically (e.g. every N turns) **while capacity is
  ample**. On replay, Podiom sends `rolling summary + recent turns verbatim`
  rather than the entire raw history. Because the summary is always pre-computed,
  a switch never has to generate it under rate-limit pressure.
- **R8.27** Where the provider exposes live rate-limit status, Podiom uses it as
  a **proactive trigger** to refresh/extend the summary before a switch is
  forced. This is **provider-asymmetric** (verified):
  - **Codex** streams rate status in `account/rateLimits/updated` notifications
    (`rateLimits.primary.usedPercent`, `secondary.usedPercent`, `resetsAt`) —
    usable directly to summarise proactively at a configurable threshold (e.g.
    80%). The notification is account-scoped and carries no thread or turn id.
  - **Claude** parses utilization headers internally but does not yet expose them
    cleanly in stream-json; the reliable live signal today is the `api_retry`
    event (status 429), which arrives only once already limited. Until Claude
    exposes utilization as cleanly as Codex, the rolling summary (R8.26) is the
    guarantee on Claude; rate-status triggering is a future enhancement there.
- **R8.28** Net: the rolling summary makes replay robust on **both** providers
  regardless of rate-status visibility; live rate-status is an optimisation used
  where available (Codex now, Claude later).

### 8.6 Security requirements carried from the reference

- **R8.29** Treat any generated MCP config as sensitive (may contain server
  commands, local URLs, tokens, credentials). Redact from user-facing logs.
- **R8.30** Never expose raw system prompts / developer instructions to
  untrusted clients.
- **R8.31** `yolo` (Claude `bypassPermissions` / Codex `approvalPolicy: "never"`
  + `danger-full-access`) is whole-machine access by design (§5.5 R5.21). Podiom does
  not pretend the workspace is a sandbox in `yolo`. The guard is the **explicit
  user opt-in** and the `approve` default — not technical confinement.
- **R8.32** Keep per-profile auth state isolated (`CLAUDE_CONFIG_DIR` /
  `CODEX_HOME`) and never cross-contaminate. *(Note: agent **workspaces** are
  intentionally shared across agents, §5.8; it is the **profile/auth** state that
  stays isolated.)*
- **R8.33** Detect rate-limit / auth-failure signals on the stream so Podiom can
  trigger the fallback chain (§8.8) — ties to §6 `/profile` and R4.3.

### 8.7 Profiles (the auth model)

A **profile** is a named, isolated auth context bound to its own config
directory. It is how Podiom supports several Claude/Codex accounts — each with
its own rate limits — behind the same CLI.

- **R8.34** A profile maps **1:1 to one underlying account**. It is realised as a
  dedicated config dir: `CLAUDE_CONFIG_DIR=<profile-dir>` for Claude,
  `CODEX_HOME=<profile-dir>` for Codex. Switching profile = switching that env
  var, which means new auth → new session/thread → history replay (§4).
- **R8.35** **Profiles are optional.** Two cases:
  - **With a profile:** Podiom launches the CLI with the profile's
    `CLAUDE_CONFIG_DIR`/`CODEX_HOME` set, giving an isolated auth that can coexist
    with other profiles and be switched between.
  - **Without a profile (default):** Podiom does **not** set
    `CLAUDE_CONFIG_DIR`/`CODEX_HOME` at all, letting the CLI use its normal
    global login. This is the simple one-user, one-login case.
- **R8.36** "No profile" (default auth) is a **first-class** source and target of
  a switch — Podiom can fall back *from* default *to* a named profile and vice
  versa, identically to profile-to-profile.
- **R8.37** **Podiom never handles credentials.** A profile is just *a directory
  path and a name* that Podiom owns. The actual login is performed by the CLI's
  own auth flow, run by the user against that dir (e.g.
  `CLAUDE_CONFIG_DIR=<dir> claude login`). Podiom never stores, reads, or enters
  passwords/tokens in plaintext. (Aligns with the credential boundary in
  Principle 1 / security rules.)

### 8.8 Fallback chain (automatic on rate limit)

- **R8.38** A session/agent MAY declare an **ordered fallback chain**: a sequence
  of targets Podiom steps through automatically when the current target is rate-
  limited (detected per R8.33). **Each chain entry is a profile name** (or
  `default`); since a profile is bound 1:1 to a provider (R8.34, and each profile
  declares its `provider`), a fallback entry implicitly carries **both** the auth
  context **and** the provider. A chain may therefore mix Claude and Codex
  profiles, and stepping to a Codex entry from a Claude one is a provider switch.
- **R8.39** A fallback target may be **another profile of the same provider**
  (e.g. `CLAUDE_CONFIG_DIR=xyz claude -p`) **or a different provider entirely**
  (e.g. Claude → Codex). Both are supported because replay is provider-agnostic
  (R4.6).
- **R8.40** On each fallback step Podiom performs the standard switch: new
  session/thread under the next target + history replay (using the rolling
  summary, §8.5). The Podiom session and its canonical history are unchanged;
  only the backing target moves.
- **R8.41** The chain is **ordered and configurable** per agent (and/or globally
  as a default). Behaviour at end-of-chain (all targets exhausted) is a defined
  policy: surface the rate-limit to the user rather than loop. *(Exact
  end-of-chain behaviour — e.g. wait-and-retry vs. fail — is a tuning detail.)*

---

## 9. Configuration (YAML)

- **R9.1** All Podiom state lives under a **single fixed root, `~/.podiom/`**, on
  every OS (no per-OS XDG / `Application Support` / `%AppData%` split). This holds
  the config, the SQLite DB, and all agent directories:

  ```
  ~/.podiom/
    config.yaml              # system configuration (this file)
    AGENTS.md                # Podiom-owned base instructions (ships with install)
    podiom.db                # SQLite: sessions, history, rolling summaries
    agents/
      <name>/
        SOUL.md              # user identity (always created)
        AGENTS.md            # user per-agent instructions (optional)
        workspace/           # agent-local cwd (§5.2)
    projects/                # shared across agents (§5.3): projects.yaml + project dirs
    schedules/               # self-describing job files, *.md w/ frontmatter (§7)
    profiles/                # optional: profile config dirs may live here too
  ```

  (A profile may point its `config_dir`/`home_dir` anywhere, but keeping them
  under `~/.podiom/profiles/` keeps everything Podiom-related together.)
- **R9.2** Config covers at least: defined agents, profile definitions, fallback
  chains, default model/effort, web server bind address/port, and the permission
  mode default. It does **not** define schedules (self-describing files, §7) or
  projects (shared dir, §5.3).

Illustrative shape (not final):

```yaml
# `global` holds defaults applied across agents unless overridden per agent.
# Default permission mode is `approve` (relay to user). `yolo` is opt-in.
global:
  provider: claude
  model: <model-name>
  effort: <level>
  permission_mode: approve            # approve (default) | yolo
  agents_root: ~/.podiom/agents       # per-agent dirs created underneath
  # Optional global fallback chain, used when an agent declares none of its own.
  fallback: [work, personal, codex-main]

# Profiles are OPTIONAL named auth contexts, 1:1 with an underlying account
# (§8.7). Omit `profile` on an agent to use the CLI's normal global login (no
# CLAUDE_CONFIG_DIR/CODEX_HOME set). Podiom owns only the dir path + name; the
# user performs the actual login via the CLI's own auth flow (R8.37).
profiles:
  - name: work
    provider: claude
    config_dir: ~/.podiom/profiles/claude-work   # exported as CLAUDE_CONFIG_DIR
  - name: personal
    provider: claude
    config_dir: ~/.podiom/profiles/claude-personal
  - name: codex-main
    provider: codex
    home_dir: ~/.podiom/profiles/codex-main      # exported as CODEX_HOME

agents:
  - name: jared
    provider: claude
    profile: work                     # omit this line to use global login (default)
    model: <model-name>
    effort: <level>
    permission_mode: approve          # per-agent override of the global default
    fallback: [work, personal, codex-main]  # ordered; rate-limit → next target,
                                            #   crosses providers (Claude→Codex)
    # dir auto-created at <agents_root>/jared/ with SOUL.md (always, user identity)
    #   + optional user AGENTS.md + workspace/ (agent-local scratch). Base
    #   ~/.podiom/AGENTS.md (Podiom-owned) always applies; Podiom composes
    #   base+agent AGENTS.md+SOUL.md into the per-backend payload. Projects are
    #   shared at ~/.podiom/projects/ and schedules at ~/.podiom/schedules/.
    #   (§5.2 / §5.3 / §5.4 / §7)
    # mcp_config: ./config/jared-mcp.json   # opt-in per-agent MCP, additive (D1)
    # inherits native MCP/tools/memory by default

  - name: brewer
    provider: codex
    profile: codex-main
    permission_mode: yolo             # trusted; yolo = whole-machine access (§5.5 R5.21)

# NOTE: there is no `schedules:` block. Schedules are self-describing markdown
# files with frontmatter at ~/.podiom/schedules/*.md (§7). Projects are shared at
# ~/.podiom/projects/ (§5.3). Neither is configured here.

server:
  bind: 127.0.0.1
  port: 8787
```

---

## 10. Cross-platform requirements

- **R10.1** Single statically linked binary per OS via Go cross-compilation.
- **R10.2** All paths via OS-aware handling (`path/filepath`, `~` expansion).
  All Podiom state lives under the fixed root `~/.podiom/` on every OS (R9.1) —
  deliberately *not* the per-OS conventional dirs — for a single, predictable,
  backup-friendly location.
- **R10.3** Robust binary discovery for `claude`/`codex`, including Windows
  `.cmd`/`.exe` shims and npm install locations.
- **R10.4** Context-based cancellation for hung agents; platform-specific
  process-group handling isolated to the `exec` layer.

---

## 11. Non-functional

- **R11.1** *(O2 — resolved: daemon-attached.)* `podiom` (CLI) is a **thin
  client that always talks to a running `podiomd`**. It does not run sessions
  in-process. If no daemon is running, the CLI reports this and points the user
  to start it. This guarantees a single source of runtime truth (the daemon owns
  all session, agent, and schedule state) and avoids any CLI-vs-daemon state
  divergence.
- **R11.2** *(O6 — resolved: SQLite.)* Podiom's session bookkeeping — canonical
  message history, rolling summaries (§8.5), and provider handles (Claude session
  ID, Codex `threadId`) — plus session metadata (channel origin, §4 R4.10, and
  schedule/run linkage, §4 R4.12) — is persisted in an **embedded SQLite
  database** at `~/.podiom/podiom.db` (R9.1). SQLite is chosen for transactional
  safety (history is the source of truth and must replay exactly; no half-written
  history after a crash) and for fast queries powering the session list (incl.
  filtering by origin or schedule). Agent **workspaces** remain plain files on
  disk (§5.2), so project content stays transparent and git-friendly; only
  Podiom's internal bookkeeping lives in the DB.
- **R11.3** *(O5 — resolved: no limit in v1.)* Podiom imposes **no concurrency
  cap** in v1: every triggered run (interactive or scheduled) executes
  immediately. Accepted trade-off: many parallel Claude turns (each its own
  process) can use significant RAM/CPU, and parallel runs against one account can
  reach rate limits sooner (mitigated by §8.5). A global or per-account cap is a
  natural future addition.
- **R11.4** Live streaming of agent output to the web UI via WebSocket.
- **R11.5** Clear, structured logging of runs (manual and scheduled).

---

## 12. Resolved decisions (decision log)

All previously open questions are now decided. This log records the choice and a
one-line rationale for each.

- **D1 — Per-agent tools/MCP → additive (was O1).** Agent-specific MCP/tools are
  *added on top of* inherited native tools (no strict replacement in v1). A
  `tools: strict` per-agent option may come later. (§5.4)
- **D2 — CLI architecture → daemon-attached only (was O2).** `podiom` always
  connects to a running `podiomd`; no standalone in-process mode. Single source
  of runtime truth. (§11 R11.1)
- **D3 — Naming → session model at low effort (was O3).** Auto-naming uses the
  session's own model but issues the call at low/minimal effort for speed and
  cost. A dedicated `naming_model` may come later. (§4 R4.9)
- **D4 — Long-history replay → rolling summary + rate-status trigger (was O4).**
  Podiom keeps a pre-computed rolling summary so a switch never has to summarise
  under rate-limit pressure; live rate status triggers proactive refresh where
  exposed (Codex now via `rate_limits.*`; Claude later). (§8.5)
- **D5 — Concurrency → no cap in v1 (was O5).** Every triggered run executes
  immediately; RAM/CPU and per-account rate pressure accepted as a known
  trade-off. Caps are a future option. (§11 R11.3)
- **D6 — Persistence → embedded SQLite (was O6).** Session history, rolling
  summaries, and provider handles live in SQLite for transactional safety and
  fast queries; workspaces stay as plain files. (§11 R11.2)
- **D7 — Adapter interface → one interface, two process models (was O7).**
  Per-turn (Claude) and long-lived app-server (Codex) behind a single adapter
  abstraction. (§3)
- **D8 — Auth → optional named profiles, 1:1 with accounts (was O8, refined).**
  A *profile* is a named, isolated auth context bound to a config dir
  (`CLAUDE_CONFIG_DIR`/`CODEX_HOME`), 1:1 with one underlying account. Profiles
  are **optional**: with a profile Podiom sets the dir; without one it uses the
  CLI's global login. "No profile" (default) is a first-class switch source/target.
  Podiom owns only the dir path + name — never credentials; the user runs the
  CLI's own login. (§8.7)
- **D9 — Identity/instructions → three layers, Podiom-composed (revised).**
  Effective instruction = base `~/.podiom/AGENTS.md` (Podiom-owned, ships with
  install) + per-agent `AGENTS.md` (user, optional) + `SOUL.md` (user, always
  created), in that order. Files stay separate for the user; **Podiom** composes
  the payload per backend — `@`-link where supported (Claude), concatenated
  bundle where not (Codex). Composition is Podiom's job, not the CLI's, so it no
  longer depends on `CODEX_HOME` or directory-walk behaviour. (§5.4 / §8.4a)
- **D10 — `yolo` scope → whole machine (was O10).** `yolo` is full-disk by
  design; an explicit informed opt-in. `approve` (default) is the safe posture.
  (§5.5 R5.21)
- **D11 — Cross-agent workspace access → fully shared (new).** Every agent can
  read/write every other agent's workspace in all modes; workspaces are **not** a
  security boundary. Deliberate for a single-user, fully-trusted setup. (§5.8)
- **D12 — Permission mechanics → verified two-mode (was O11).** `approve` = Claude
  `--permission-prompt-tool` (blocking Podiom MCP server + timeout) / Codex
  `on-request` + `read-only`. `yolo` = Claude `bypassPermissions` / Codex
  `never` + `danger-full-access`. Sandbox and approval are independent on Codex.
  (§8.4)
- **D13 — Fallback chain → ordered, cross-provider, automatic on rate limit
  (new).** An agent (or global default) declares an ordered list of targets;
  on rate limit Podiom steps to the next, which may be another profile *or*
  another provider (Claude→Codex), replaying history at each step. Possible
  because replay is provider-agnostic. (§8.8)
- **D14 — Storage root → single `~/.podiom/` on all OSes (new).** Config, SQLite
  DB, and all agent directories live under `~/.podiom/`, not per-OS conventional
  dirs, for one predictable location. (§9 R9.1)
- **D15 — Schedules → self-describing frontmatter files at `~/.podiom/schedules/`
  (revised by D23).** A schedule is one markdown file: YAML frontmatter (agent,
  model, effort, cron/every, run_permission, enabled) + body (the task). System-
  level, not per-agent; the config has no `schedules:` block. (§7) *(This
  supersedes the earlier per-agent `schedules/` placement.)*
- **D16 — Config section rename → `defaults:` is now `global:` (new).** Cosmetic
  rename of the top-level defaults block. (§9)
- **D17 — Tech stack → Go, recorded explicitly (new).** Go for subprocess
  handling, goroutines, single static binary, built-in HTTP/WebSocket, clean
  cross-compilation. Intended libs: cobra, robfig/cron, pure-Go SQLite
  (`modernc.org/sqlite`, no cgo), kardianos/service (deferred). (§3 R3.1–R3.4)
- **D18 — Deployment-target neutral; HA add-on later (new).** v1 standalone, but
  the core assumes nothing about its process lifecycle, network, or storage
  location; storage root overridable via `PODIOM_HOME`. A Home Assistant add-on
  becomes a packaging concern later, not a rewrite. (Principle 7; §2)
- **D19 — Identity/instructions → three Podiom-composed layers (revised D9).**
  base `AGENTS.md` (Podiom, ships) + per-agent `AGENTS.md` (user, optional) +
  `SOUL.md` (user, always), composed by Podiom and delivered by `@`-link (Claude)
  or bundle (Codex). Files stay separate for the user. (§5.4 / §8.4a)
- **D20 — Session provenance → channel origin + schedule linkage (new).** Each
  session records an immutable channel-of-origin (web/cli/schedule, later
  telegram/slack) shown visually but not binding (a session can continue from
  another channel); scheduled-run sessions persist their schedule+run link so they
  can be revisited and continued manually. (§4 R4.10–R4.12; §7 R7.9)
- **D21 — Web frontend → plain Svelte + Vite + TS + Tailwind, embedded SPA
  (new).** Single-page app built to static assets, embedded in the Go binary via
  `embed` and served by `podiomd`; browser-native WebSocket for streaming. No
  SvelteKit, no SSR, no Node runtime — keeps distribution a single binary and the
  HA container simple. (§3 R3.5)
- **D22 — Projects → agent-independent, shared at `~/.podiom/projects/` (new).**
  Projects moved out of per-agent workspaces to a system-level shared dir with one
  `projects.yaml` ledger, so multiple agents can collaborate on the same project
  (as in a real company). v1 accepts last-write-wins on the shared ledger
  (no concurrency cap, D5). (§5.3)
- **D23 — Schedules → system-level self-describing frontmatter files (new,
  revises D15).** Schedules moved out of per-agent dirs to `~/.podiom/schedules/`,
  each a single markdown file whose frontmatter (agent/model/effort/timing/
  run_permission/enabled) makes it self-sufficient; the body is the task. The
  config's `schedules:` block is removed — the files are the source of truth.
  (§7 R7.2–R7.3a)

### Security posture summary
Podiom's only real safety boundary in v1 is the **`approve` default** (per-action
relay to the user). Workspace isolation is intentionally **not** a boundary
(D11), and `yolo` is intentionally whole-machine (D10). This is appropriate for a
single-user, fully-trusted, local deployment and should be revisited before any
multi-user or shared deployment (already out of scope, §2).

---

## Appendix A — Claude/Codex integration reference

The integration requirements in §8 are grounded in an OpenClaw-style runtime
reference for Claude Code and OpenAI Codex (process model, CLI parameters,
app-server protocol, and policy values), captured 2026-06-28. Key facts adopted:

- **Claude** runs **per-turn** as `claude -p` with stream-json stdin/stdout;
  resume via persisted session ID + `--resume`. Model via `--model`, effort via
  `--effort <level>` using provider-supported values. Profile isolation via
  `CLAUDE_CONFIG_DIR`.
- **Codex** runs as a **long-lived** `codex app-server --listen stdio://`;
  lifecycle via `thread/start`, `thread/resume`, `turn/start`. Model/effort sent
  in the protocol (effort default `medium`). Profile isolation via `CODEX_HOME`.
- The reference's **policy values are deliberately invasive** (Claude
  `bypassPermissions`; Codex `approvalPolicy: "never"` +
  `danger-full-access`; strict host-only MCP). Podiom adopts the *mechanisms* but
  inverts these *defaults* per Principle 6 and §8.4.

The full reference document should be checked into `docs/integrations/` as the
source of truth the adapter layer implements against.
