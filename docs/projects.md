# Projects and the Roadmap

## Projects (shared ledger)

Projects are **shared, system-level, agent-independent** resources (§5.3 / D22) —
modelling how a real team collaborates on the same codebase, book, or initiative.
They are not owned by any one agent: every agent can read and work on every
project.

- Each project is a subdirectory under `~/.podiom/projects/` plus an entry in the
  single shared ledger `~/.podiom/projects/projects.yaml` (R5.10).
- Agents are instructed (via the Podiom-owned base `AGENTS.md`) to work **with and
  against** the ledger — create a project dir and record/update its entry whenever
  they create or maintain something durable (R5.11).
- Because agents maintain the file directly, the ledger is the source of truth and
  v1 accepts **last-write-wins** on concurrent writes (R5.12).

A ledger entry:

```yaml
projects:
  - id: mission-control
    name: Mission Control
    description: Next.js dashboard UI.
    path: mission-control        # relative to ~/.podiom/projects/
    status: active               # active | paused | done
    stack: [Next.js, TypeScript]
    repo: null
    roadmap: []                  # derived roadmap task IDs
    notes: >
      Anything the next agent needs to know.
    instructions: >
      Standing instructions that apply to sessions bound to this project.
```

`repo` is optional. When connected through the Projects page, Podiom stores a
GitHub snapshot link instead of requiring `git`:

```yaml
repo:
  provider: github
  mode: snapshot
  owner: Podiom
  name: Podiom
  full_name: Podiom/Podiom
  html_url: https://github.com/Podiom/Podiom
  default_branch: main
  ref: main
  synced_at: "2026-07-01T12:00:00Z"
  source_kind: archive
```

Connecting a repo downloads a GitHub archive snapshot into the project's
`repo/` directory (`~/.podiom/projects/<project>/repo/`) and writes
`.podiom-source.json` in the project directory (`~/.podiom/projects/<project>/`).
This is source context for agents, not a Git checkout: there is no `.git`, no
remote, and no push/write-back path in v1.

### Connecting GitHub

Podiom uses the official public GitHub App and GitHub's device authorization
flow. The app is already configured by default; users do not create their own
GitHub App.

The web flow is:

1. Authorize Podiom with your GitHub account.
2. Choose which repositories the Podiom GitHub App may read.
3. Pick one repository for the project.

Podiom downloads the selected ref as a source snapshot. Re-syncing replaces the
snapshot contents after staging and path validation, preserving previous contents
under `.podiom-backups/` when a backup is needed.

Create and browse projects from the **Projects** page in the web UI, or list them
with `podiom projects list`.

## Roadmap (tasks)

The **Roadmap** is a kanban board of **tasks** — units of work on a project,
assignable to an agent and startable on demand. Tasks are a Podiom-managed entity
(persisted in SQLite) and are **independent** in v1: there are no inter-task
dependencies (within the §2 out-of-scope line).

Each project's `roadmap` array in `projects.yaml` is a derived list of task IDs
for that project. Task details live in SQLite and are expanded by Podiom when
drafting roadmap prompts.

Each project may also have user-authored standing instructions in its
`instructions` ledger field. Those instructions apply to sessions bound to the
project, including manual project chats, roadmap task sessions, scheduled task
pickups, and goal planning/review sessions for goals with that project id. For
connected GitHub projects this remains Podiom ledger metadata, not part of the
replaceable source snapshot under `repo/`.

A task has: a project, a title and optional body, an assigned agent, a status
column (`backlog` → `in_progress` → `review` → `done`), and an optional scheduled
pickup time.

### Starting work

- **Start on demand** (a backlog card with an assigned agent): creates a durable
  **roadmap-origin session** bound to the agent, linked back to the task, and
  seeds it with the task as the first turn. The chat shows a "part of `<project>`"
  provenance banner. If the project has a connected repo, each provider turn in
  that project-linked session receives project details, repo metadata, and the
  local source snapshot path. The visible chat history stores only the user's
  actual messages. The task moves to **In Progress**.
- **Open in chat** (an already-started card): reopens the task's existing session
  to continue manually.
- **Scheduled pickup**: a task with a pickup time is started automatically by the
  embedded scheduler when that time arrives, running unattended under the
  stricter **preapproved** permission policy (§7.7) — side effects are denied
  unless explicitly safe. Starting moves it to In Progress so it is not picked up
  twice. Pickup times are interpreted as UTC.

Drag cards between columns to change status, assign an agent from the card, and
add tasks with **+ New task**. Observe tasks with `podiom tasks list`.

## HTTP API

- `GET /api/projects`, `POST /api/projects`
- `GET /api/projects/<id>/instructions`, `PUT /api/projects/<id>/instructions`
- `GET /api/tasks`, `POST /api/tasks`
- `PATCH /api/tasks/<id>` (assign / move / edit / set pickup)
- `POST /api/tasks/<id>/start` — start on demand → returns the session
- `GET  /api/tasks/<id>/session` — the task's latest session (404 if not started)
- `GET /api/github/status`
- `POST /api/github/device/start`
- `POST /api/github/device/poll`
- `GET /api/github/repos`
- `POST /api/projects/<id>/repo`
- `POST /api/projects/<id>/repo/sync`
- `DELETE /api/projects/<id>/repo`

Sessions can carry a durable `project_id`. Manual web sessions may choose a
project before the first message, while roadmap sessions also carry
`origin = roadmap` and a `task_id`. The session detail endpoint
(`GET /api/sessions/<id>`) includes the task, project id, and project name for
the provenance banner.
