# Goals

A **goal** is an outcome you hand to one agent — not a task list. You describe
what "done" means, pick a lead agent, and walk away. The agent plans the work
itself (as roadmap tasks and schedules), checks in on its own cadence, records
auditable progress, and comes back to you only when it needs a decision or
believes the goal is met. The full behavior is specified in
[requirements/goals.md](requirements/goals.md).

## Creating a goal

From the **Goals** page in the web UI, a goal has:

- **Title** and **description** — the outcome and the context a teammate would
  need.
- **Success criteria** — free text defining "done". The agent only proposes
  completion when it believes this is met.
- **Metrics** (optional) — measurable indicators, each with a name, numeric
  target, and optional unit (e.g. *Subscribers — target 500*). The agent moves
  the current values over time; every change is audited old → new.
- **Lead agent** — exactly one agent owns the goal. It may delegate by creating
  tasks assigned to other agents, but it stays accountable.
- **Project** (optional) — links the goal's sessions to a project.
- **Review cadence** — how often the agent reviews the goal unattended
  (e.g. every 24h; floor 15m). Empty disables automatic reviews.

The moment the goal is created, the lead agent starts a continuing **goal
conversation** and runs its first **planning turn** in the background: it
decomposes the goal into roadmap tasks and/or schedules
(visible on the Roadmap and Schedules pages), records its plan on the goal
timeline, and files access requests for anything it is missing. You can leave
immediately.

## Adding feedback

From a goal's detail page, use **Add feedback** in the Activity section to leave
strategy notes, constraints, or thoughts about next steps. Feedback is saved as
a normal timeline entry and is included in the next planning or review session
for the lead agent to consider.

Adding feedback does **not** start a chat, interrupt the agent, trigger an
immediate review, or create a back-and-forth. If you want the agent to consider
it right away, add the feedback and then use **Review now**.

## Lifecycle

| Status | Meaning |
| --- | --- |
| `active` | The loop is running; reviews fire on cadence. |
| `paused` | You suspended it; no reviews fire. Resume any time. |
| `review` | The agent proposed completion — the goal waits on you. |
| `done` | You confirmed completion. Reopenable. |
| `abandoned` | You gave up on it. Reopenable. |

Only you change status (pause/resume, mark done, reopen, abandon). The agent's
single transition is *proposing* completion, which attaches a closing report
that walks through each success criterion — you then **Mark done** or
**Reopen**. Deleting a goal removes its timeline and access requests but keeps
every session the agent ran.

## Reviews

Active goals are reviewed unattended on their cadence (driven by the embedded
scheduler, like scheduled task pickup — see [scheduling.md](scheduling.md)).
Planning and reviews share one lead-agent session with `origin = goal`; every
review is also recorded as its own durable goal run, so the timeline can open
the exact turn that produced an activity. Delegated roadmap tasks and schedules
keep their own execution sessions. In a review the agent assesses progress
against the criteria, adjusts its tasks/schedules, records a progress entry
with evidence and metric updates, reads your answers to its access requests,
and proposes completion when everything is met. **Review now** on the goal
detail triggers one immediately.

## Goals run in yolo mode

A goal exists to reach an outcome **without** you in the loop, so the whole goal
chain runs with full autonomous access (yolo): the lead agent's planning and
review sessions, **and every started roadmap task and schedule the goal
creates**, execute with no per-action approval prompts (Claude `--permission-mode
bypassPermissions`; Codex `approvalPolicy: never` + `sandbox:
danger-full-access`). The agent can run shell commands, edit files, install
tools, and reach the network directly.

This is deliberate and clearly disclosed: the goal-creation form shows a
full-access warning before you create the goal, and the goal detail view carries
a persistent **autonomous · full access** badge. Tasks a goal creates carry its
`goal_id` (their runs are forced yolo even if the task was plan-gated), and the
lead starts task work with `podiom_start_task` when it should begin. Schedules a
goal creates are written with `run_permission: yolo`.

The counterweight is a complete audit trail: every tool call the goal chain
makes is recorded on the goal timeline (see below).

## Access requests

When the agent is missing a capability, it files a typed **access request**
instead of silently working around it. You get a push notification and the
request appears on the goal:

| Kind | What approving does |
| --- | --- |
| `mcp_server` | Assigns the catalogue MCP server to the agent immediately. |
| `skill` | Installs the skill from the marketplace. |
| `permission_mode` | Changes the agent's standing permission mode everywhere. Rarely relevant to goal work — goal runs already have full access — but still used to change the agent's mode outside goals. Granting `yolo` is security-sensitive and the dialog warns you. |
| `cli_tool` | With installer fields: installs the tool into the agent's own workspace (see [workspace-tools.md](workspace-tools.md)). Without them (host-wide tools): approval acknowledges, and you run the shown install command yourself. Since goal runs are yolo, a goal agent can usually install a tool directly instead of requesting it. |
| `env_var` | Requests name the variable and purpose only — **the request never carries the value**. In the approval dialog you may enter the value once: Podiom stores it in `credentials.yaml` (readable only by your OS user) and injects it into agent environments on later runs; it is never shown again and never returned by the API. Leave the field empty to just acknowledge and set the variable yourself where `podiomd` runs. Stored credentials are managed under Settings → Credentials. |

Approve or deny with an optional **note to the agent** — it is relayed
verbatim at the agent's next review, so this is how you talk back ("approved,
but stay on the staging domain"). A failed automatic grant stays on the goal
with its error and can be re-approved after you fix the cause. Agents can
never decide requests: there is deliberately no agent tool for the decision
endpoints.

## The timeline (audit trail)

Every goal has an append-only activity timeline: planning and review runs,
your feedback, progress entries with evidence, metric changes (old → new), plan
changes, access requests and your decisions, status changes, and the completion
proposal. Each agent-produced entry links to the exact run that produced it.
**View run** opens that activity, the other events from the same turn, and only
that turn's transcript; opening the full continuing conversation remains a
secondary action. Append-only is enforced in the database schema, not by
convention.

Because the goal chain runs in yolo mode, the timeline also records a
`tool_use` entry for **every tool call** the goal, its tasks, and its schedules
make — shell commands, file edits, installs, web fetches, and MCP calls — so you
can see exactly what the goal did while you were away. Runs of consecutive
read-only calls (reads, greps, web fetches) are collapsed into a single
expandable row to keep the timeline scannable; side-effecting calls (shell
commands, file writes) are shown individually with the command or file path.
Large tool inputs are truncated — command text and file paths are kept, but file
contents written are elided — so the audit trail stays readable and never stores
whole files.

## Notifications

You get web push (see the notification settings) when an access request is
filed and when completion is proposed; tapping the notification deep-links to
the goal. In-app, the Goals nav entry shows a badge whenever a goal needs you
(pending or failed requests, or proposed completion), and the goals list sorts
those goals first under **Needs you**.

## HTTP API

- `GET  /api/goals` (`?status=`), `POST /api/goals`
- `GET  /api/goals/<id>` — goal + recent timeline + access requests
- `PATCH /api/goals/<id>` — edit fields; status transitions ride the same body
- `DELETE /api/goals/<id>`
- `GET  /api/goals/<id>/events` (`?limit=&before=`) — timeline pagination
- `GET  /api/goals/<id>/runs/<run-id>` — exact run metadata, events, and bounded transcript
- `POST /api/goals/<id>/events` — record progress / metric updates (agent tools)
- `POST /api/goals/<id>/feedback` — add user feedback for the next goal run
- `PATCH /api/goals/<id>/feedback` — edit feedback by `event_id` until a later planning/review session has read it
- `POST /api/goals/<id>/propose-completion`
- `POST /api/goals/<id>/review` — trigger a review now
- `GET  /api/access-requests` (`?goal_id=&status=`), `POST /api/access-requests`
- `POST /api/access-requests/<id>/approve`, `POST /api/access-requests/<id>/deny`

Agents drive goals through `podiom_*` tools on the built-in `podiom_manage`
MCP server (`podiom_get_goal`, `podiom_record_goal_progress`,
`podiom_request_access`, `podiom_propose_goal_completion`, …). Tool calls are
stamped with the calling session's identity server-side, so timeline
provenance never depends on the model identifying itself. `podiom_create_task`
takes a `goal_id` (also on `POST /api/tasks`) — the lead agent passes it when a
task is part of the goal's plan, which links the task's runs to the goal (forced
yolo, audited on the goal timeline).

## Limitations (v1)

- One lead agent per goal; no shared ownership.
- No spend/token budgets or caps on how many tasks/schedules a goal may spawn —
  the audit trail is the control.
- Tasks and schedules the agent creates for the goal carry its `goal_id`, so
  their started runs are autonomous (yolo) and recorded on the goal timeline.
  Creating a task does not start it; the lead starts task work with
  `podiom_start_task` when execution should begin. Work the agent creates
  without a `goal_id` is not linked and runs under the normal (stricter)
  unattended policy.
- Reviews only fire while `podiomd` is running; an overdue review fires on the
  next scheduler tick after restart.
