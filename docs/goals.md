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

The moment the goal is created, the lead agent runs a **planning session** in
the background: it decomposes the goal into roadmap tasks and/or schedules
(visible on the Roadmap and Schedules pages), records its plan on the goal
timeline, and files access requests for anything it is missing. You can leave
immediately.

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
Each review is an ordinary session with `origin = goal`, so you can open its
full transcript from the timeline. In a review the agent assesses progress
against the criteria, adjusts its tasks/schedules, records a progress entry
with evidence and metric updates, reads your answers to its access requests,
and proposes completion when everything is met. **Review now** on the goal
detail triggers one immediately.

Goal sessions run under the stricter **preapproved** unattended policy (the
Podiom self-management tools plus read-only inspection — no shell). The real
work happens in the tasks and schedules the agent spawns, not in the review
itself.

## Access requests

When the agent is missing a capability, it files a typed **access request**
instead of silently working around it. You get a push notification and the
request appears on the goal:

| Kind | What approving does |
| --- | --- |
| `mcp_server` | Assigns the catalogue MCP server to the agent immediately. |
| `skill` | Installs the skill from the marketplace. |
| `permission_mode` | Changes the agent's permission mode. Granting `yolo` is security-sensitive and applies to the agent everywhere — the dialog warns you. |
| `cli_tool` | With installer fields: installs the tool into the agent's own workspace (see [workspace-tools.md](workspace-tools.md)). Without them (host-wide tools): approval acknowledges, and you run the shown install command yourself. |
| `env_var` | Acknowledges. Requests name the variable and purpose only — **the secret value never transits Podiom**; you set it where `podiomd` runs. |

Approve or deny with an optional **note to the agent** — it is relayed
verbatim at the agent's next review, so this is how you talk back ("approved,
but stay on the staging domain"). A failed automatic grant stays on the goal
with its error and can be re-approved after you fix the cause. Agents can
never decide requests: there is deliberately no agent tool for the decision
endpoints.

## The timeline (audit trail)

Every goal has an append-only activity timeline: planning and review sessions,
progress entries with evidence, metric changes (old → new), plan changes,
access requests and your decisions, status changes, and the completion
proposal. Each agent-produced entry links to the session that produced it, so
any claim of progress is one click from its full transcript. Append-only is
enforced in the database schema, not by convention.

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
- `POST /api/goals/<id>/events` — record progress / metric updates (agent tools)
- `POST /api/goals/<id>/propose-completion`
- `POST /api/goals/<id>/review` — trigger a review now
- `GET  /api/access-requests` (`?goal_id=&status=`), `POST /api/access-requests`
- `POST /api/access-requests/<id>/approve`, `POST /api/access-requests/<id>/deny`

Agents drive goals through `podiom_*` tools on the built-in `podiom_manage`
MCP server (`podiom_get_goal`, `podiom_record_goal_progress`,
`podiom_request_access`, `podiom_propose_goal_completion`, …). Tool calls are
stamped with the calling session's identity server-side, so timeline
provenance never depends on the model identifying itself.

## Limitations (v1)

- One lead agent per goal; no shared ownership.
- No spend/token budgets or caps on how many tasks/schedules a goal may spawn —
  the audit trail is the control.
- Tasks and schedules the agent creates are not hard-linked to the goal; the
  plan-change timeline entries record what was created.
- Reviews only fire while `podiomd` is running; an overdue review fires on the
  next scheduler tick after restart.
