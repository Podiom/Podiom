# What agents can do with Podiom

Podiom gives every agent a set of `podiom_*` tools for operating Podiom itself —
creating roadmap tasks and schedules, managing projects and MCP servers, driving
[goals](goals.md), and inspecting its own session. This page is the map of that
surface: what is there, what is deliberately not, and what happens to the record
when an agent uses it.

The tools arrive over MCP, but not from a server you configure. Podiom injects
its own internal servers into every session (see
[requirements/mcp.md](requirements/mcp.md) for the catalogue of servers *you*
manage). They are stdio subprocesses of the `podiomd` binary, and an agent cannot
un-inject them through MCP assignment.

| Server | Injected into | What it is for |
| --- | --- | --- |
| `podiom_manage` | every session | Operating Podiom: the bulk of the surface below |
| `podiom_project` | every session | The project this session is in, and its branching policy — see [git.md](git.md) |
| `podiom_plan` | every session | Submitting an implementation plan for approval |
| `podiom_permission` | Claude turns needing approval | The permission relay; not a tool the agent chooses |
| `podiom_interview` | USER.md interview sessions only | Replaces the others for that one flow |

Each helper is launched with the calling session fixed on its command line, so
an agent cannot address another session's project, plan, or identity. That is
also what makes the attribution below trustworthy.

## What you create is attributed to you

When an agent creates a roadmap task or a schedule, Podiom records **which agent
and which session** authored it. The identity comes from the helper's own launch
flags, never from the model, so it can be neither forged nor forgotten.

This runs in both directions:

- On the **Roadmap** and **Schedules** pages, an agent-created item shows
  *created by \<agent\>* and links back to the conversation it came out of.
- In a **chat session's** detail panel, *created here* lists the tasks and
  schedules that conversation produced.
- An agent reads the same picture with `podiom_session_context`.

A task or schedule you made yourself carries no attribution — empty means the
human did it, which is why nothing needed backfilling when this arrived.

Authorship is fixed at creation. Editing a task never rewrites who made it, so a
later agent cannot claim work the user did. One known gap: an agent can add a
`pickup_at` time to a task *you* created, which makes it self-firing without
changing its author.

Skills and MCP catalogue entries are not attributed. They are global
configuration rather than work that spawns sessions, and neither can fire on its
own.

## The tool surface

### Your own session
`podiom_session_context` — no arguments; the session resolves from the agent's
own process. Reports origin (`web`, `cli`, `schedule`, `roadmap`, `goal`) and
whether the run is **unattended**, what the session is linked to (project, task,
goal, schedule), what it has created, and how much context is left. It never
returns message history.

### Workspace file snapshots

`podiom_attach_workspace_file` takes one UTF-8 text file path relative to the
session's project root (or the agent workspace for an unbound session) and an
optional label. It stores an immutable database snapshot and returns a Markdown
link the agent can put in any reply, task, schedule, goal entry, request, or
other user-visible prose. The dashboard renders those links as file pills and
opens their content in an authenticated in-app viewer, so the user never has to
browse the local filesystem.

Snapshots are limited to 256 KiB, preserve the exact validated bytes, and stay
available after the source file, session, task, schedule, goal, project, or
agent is changed or removed. There is no deletion or expiration workflow yet.

### Roadmap tasks
`podiom_list_tasks`, `podiom_get_task`, `podiom_create_task`,
`podiom_update_task`, `podiom_delete_task`, `podiom_start_task`.

`podiom_start_task` creates a session for the assigned agent and runs it
immediately in the background. A task created with `pickup_at` starts itself at
that time. See [projects.md](projects.md).

### Projects
`podiom_list_projects`, `podiom_get_project`, `podiom_create_project`,
`podiom_update_project`, `podiom_delete_project`.

### Schedules
`podiom_list_schedules`, `podiom_get_schedule`, `podiom_create_schedule`,
`podiom_update_schedule`, `podiom_delete_schedule`, `podiom_run_schedule`.

A schedule fires unattended sessions from then on, so agents are told to create
one only when you asked for recurring work — or, with `webhook: true`, for work
that should react to an outside event. Podiom generates the webhook secret; an
agent that creates one is told to read the schedule back and give you the URL so
you can wire up the sender. `podiom_update_schedule` edits one in place —
including `enabled: false` to park it without losing its history, and toggling
`webhook` off and on to rotate a leaked secret. Its name and its goal link are
not editable: the name is the filename, and the goal link forces yolo, which is
not something an edit should do quietly. See [scheduling.md](scheduling.md).

### Goals
`podiom_list_goals`, `podiom_get_goal`, `podiom_update_goal`,
`podiom_record_goal_progress`, `podiom_list_goal_events`,
`podiom_propose_goal_completion`, `podiom_request_access`, `podiom_ask_user`,
`podiom_request_user_action`, `podiom_list_access_requests`,
`podiom_list_workspace_tools`. See [goals.md](goals.md).

### Agents
`podiom_list_agents`, `podiom_get_agent`, `podiom_create_agent`,
`podiom_update_agent`, `podiom_generate_agent_soul`, `podiom_read_agent_memory`.

An agent may create a colleague and give a **brand-new** one its first identity.
It may not rewrite an existing SOUL, change anyone's permission mode, write or
clear a MEMORY.md, or delete an agent. See below.

### Skills and MCP servers
`podiom_search_skills`, `podiom_list_installed_skills`, `podiom_install_skill`,
`podiom_uninstall_skill`, `podiom_list_mcp`, `podiom_add_mcp_server`,
`podiom_remove_mcp_server`, `podiom_test_mcp_server`, `podiom_assign_mcp_server`.

### Platform
`podiom_get_config`, `podiom_patch_config`, `podiom_get_usage`,
`podiom_read_logs`.

`podiom_get_usage` reports how much of each provider plan's rate-limit windows is
spent, so an agent can weigh the cost of work it is about to spawn.

### Credentials
`podiom_list_credentials`, `podiom_store_credential`.

Podiom keeps one credentials store, and everything in it is set as an
environment variable in every agent session. An agent has two doors onto it and
deliberately not a third:

- **Look.** `podiom_list_credentials` returns names, purposes, and who stored
  each one — never values. Agents are told to check it before declaring
  themselves blocked on missing auth or asking you for a token, because anything
  listed is already sitting in their own environment.
- **Add.** `podiom_store_credential` takes a name and a value. This is for a
  secret the agent already holds — you pasted a token in chat, a CLI minted an
  API key, a signup returned one — and it exists so that secret lands in the
  store instead of in a `.env`, a shell profile, or an agent's memory. Replacing
  an existing name needs `overwrite=true`, the same shape as `confirm=true` on
  the destructive tools.
- **Not read, not delete.** No tool hands a value back, and no tool removes a
  credential. A value reaches an agent only as an environment variable in its own
  process; taking one away is yours alone.

A credential an agent stored shows *stored by \<agent\>* on the Credentials page
and links to the conversation it came out of. Blank means you added it — the same
rule the Roadmap and Schedules pages follow.

When an agent needs a credential it does *not* have, the route is still
`podiom_request_access` with `kind=env_var`: it names the variable and its
purpose, you enter the value privately, and the request never carries a secret.

## What agents deliberately cannot do

Every deletion tool requires `confirm=true`, which agents are told to pass only
after you have asked for that specific removal. Beyond that, some things are
human-only by construction:

| Not exposed | Why |
| --- | --- |
| Approving or denying an access request | An agent must never grant its own request |
| Answering its own question, or responding to its own action item | Those are your side of the conversation |
| Signing in to a provider; auth profiles; token rotation | An agent must never authenticate on your behalf |
| Reading a stored credential's value, or deleting one | An agent receives a value as an environment variable, never through an API; removing one is yours alone |
| Creating or deleting sessions | Creating one spawns an unattended run of a colleague (`podiom_start_task` and `podiom_run_schedule` are the audited routes); deleting one destroys your conversation history |
| Listing other sessions | Another agent's conversation with you is yours and theirs |
| Editing an existing agent's SOUL, or any permission mode | Identity and privilege are yours to set |
| Writing or clearing MEMORY.md | Memory accrues through [dreaming](soul-generation.md); the endpoint replaces the whole file |
| Deleting an agent | It removes every session that agent ever had; the API asks a human to type the name |
| Changing a goal's title, status, lead agent, or project | Only you move a goal through its lifecycle |

This list is enforced, not just documented. `TestManageToolsCoverAPIRoutes`
(`cmd/podiomd/manage_coverage_test.go`) fails the build when a new `/api` route
is neither wrapped by a `podiom_*` tool nor listed as excluded **with a written
reason**, so the tool surface and the API cannot drift apart silently. Two
companion tests keep the exclusions honest in the other direction.

## Where agents learn this

Agents read the tool descriptions themselves — those are the primary
documentation and they ship with the binary. Two shorter layers set the reflexes:

- A runtime-composed instruction layer tells every existing and new agent never
  to send the user to a local workspace path and to use
  `podiom_attach_workspace_file` whenever the user needs to read, copy, review,
  or act on file content. Because it is composed per run, existing installations
  receive it without rewriting their `~/.podiom/AGENTS.md`.
- A second runtime-composed layer carries the credentials rules: look in the
  store before asking for a token, put any secret you are handed into it
  immediately, and never write a value anywhere else — not a shell profile, not
  memory, and never into a task, progress entry, or reply, all of which Podiom
  stores and displays. It is composed per run for the same reason.
- `~/.podiom/AGENTS.md` carries the other standing rules (act on the user's
  request, look before you change, destructive tools need consent, what you
  create is attributed). Podiom writes this file **only if it is absent**, so an
  existing installation keeps the copy it has.
- Goal planning and review sessions get a much fuller contract, because they run
  with nobody watching. See [goals.md](goals.md).
