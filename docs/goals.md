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
- **Project** (optional) — binds the goal's whole chain to a project: the
  planning/review conversation, the roadmap tasks the agent creates, and the
  schedules it creates all work in the project's directory and receive its
  standing instructions. The agent may still put an individual task or schedule
  in a different project when that piece of work belongs elsewhere.
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

## Next step

Every review ends with the agent stating its **next step**: the single most
important move it will make before the next review, plus one sentence on why.
It appears near the top of the goal detail so you can see where the goal is
heading at a glance — *"Post the launch thread on r/selfhosted"*, not a
restatement of the tasks and schedules on the Roadmap and Schedules pages.

Note the difference from **next review** in the goal header: that is *when* the
agent wakes up, this is *what it intends to do*.

The next step is written by the agent, not you — there is no edit box. To steer
it, add feedback (above); the agent reads your notes at its next review and
revises. Each review also shows the agent its own previous next step and asks it
to report whether that happened, so the line stays current rather than going
stale. It clears when the agent proposes completion or the goal is closed;
pausing keeps it, so resuming shows the intent you paused on.

## Goals run in yolo mode

A goal exists to reach an outcome **without** you in the loop, so the whole goal
chain runs with full autonomous access (yolo): the lead agent's planning and
review sessions, **and every roadmap task and schedule the goal creates**,
execute with no per-action approval prompts (Claude `--permission-mode
bypassPermissions`; Codex `approvalPolicy: never` + `sandbox:
danger-full-access`). The agent can run shell commands, edit files, install
tools, and reach the network directly.

This is deliberate and clearly disclosed: the goal-creation form shows a
full-access warning before you create the goal, and the goal detail view carries
a persistent **autonomous · full access** badge. Tasks a goal creates carry its
`goal_id` (their runs are forced yolo even if the task was plan-gated), and
schedules a goal creates are written with `run_permission: yolo`.

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

## Action items for you

Some steps only you can take: posting from your personal account, signing
something, making a call, anything off-machine. When the agent hits one of
those it hands it back as an **action item** — a title, instructions written so
you can act without knowing anything about its plan, and one sentence on why it
needs you. Filing one sends a push notification and shows the item on the goal.

This is a different thing from its three neighbours, and the agent is told to
pick between them deliberately:

| Channel | What it means | Does it pause the goal? |
| --- | --- | --- |
| **Action item** | Work only you can carry out | **No** |
| Access request | A capability Podiom can wire (MCP server, skill, credential) | No |
| Question (`podiom_ask_user`) | A decision that is yours to make | **Yes** — reviews stop until you answer |
| Next step | The single move the *agent* will make next | No |

**Reviews keep running.** An action item is a hand-off, not a gate: the agent
carries on with the rest of the goal and plans around the item. Every review
shows it the items still open and when they were filed, so it can chase one in
its progress entry or find another route — but it is told never to file the same
ask twice.

### Answering

Open items appear in a card carousel on the goal page, oldest ask first — swipe
sideways on a phone, use the arrows or the dots on a desktop. Each card shows
the agent's instructions and gives you a note box and three verdicts:

| Verdict | What the agent does with it |
| --- | --- |
| **Done** | Treats the step as complete and builds on it |
| **Couldn't do** | Looks for another route to the same outcome |
| **Not doing** | Drops that approach and says so at the next review |

The note is free text — a link, the outcome, why you couldn't. A verdict is
given **once**; after that the card stays on the goal read-only, behind the open
ones, as the record of what you said.

### When the agent reads it

Your response is stored, not delivered. It lands in the agent's next planning or
review prompt, exactly like [feedback](#adding-feedback) — responding never
starts a run on its own. If you want it acted on now, respond and then use
**Review now**.

Both sides land on the timeline: `action_requested` when the agent files one and
`action_responded` when you answer, so the hand-off is auditable like everything
else the goal does. A goal with an open action item counts as **Needs you** in
the goals list and on the Goals nav badge.

## The timeline (audit trail)

Every goal has an append-only activity timeline: planning and review runs,
your feedback, progress entries with evidence, metric changes (old → new), plan
changes, access requests and your decisions, action items and your verdicts on
them, status changes, and the completion proposal. Each agent-produced entry links to the exact run that produced it.
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
filed, when the agent hands you an action item, and when completion is proposed;
tapping the notification deep-links to the goal. In-app, the Goals nav entry
shows a badge whenever a goal needs you (pending or failed requests, an
unanswered question, an open action item, or proposed completion), and the goals
list sorts those goals first under **Needs you**.

## HTTP API

- `GET  /api/goals` (`?status=`), `POST /api/goals`
- `GET  /api/goals/<id>` — goal + recent timeline + access requests
- `PATCH /api/goals/<id>` — edit fields; status transitions ride the same body
- `DELETE /api/goals/<id>`
- `GET  /api/goals/<id>/events` (`?limit=&before=`) — timeline pagination
- `GET  /api/goals/<id>/runs/<run-id>` — exact run metadata, events, and bounded transcript
- `POST /api/goals/<id>/events` — record progress / metric updates / next step (agent tools)
- `POST /api/goals/<id>/feedback` — add user feedback for the next goal run
- `PATCH /api/goals/<id>/feedback` — edit feedback by `event_id` until a later planning/review session has read it
- `POST /api/goals/<id>/propose-completion`
- `POST /api/goals/<id>/review` — trigger a review now
- `GET  /api/access-requests` (`?goal_id=&status=`), `POST /api/access-requests`
- `POST /api/access-requests/<id>/approve`, `POST /api/access-requests/<id>/deny`
- `POST /api/goal-action-items` — file an action item (agent tool)
- `POST /api/goal-action-items/<id>/respond` — your verdict and note

Agents drive goals through `podiom_*` tools on the built-in `podiom_manage`
MCP server (`podiom_get_goal`, `podiom_record_goal_progress`,
`podiom_request_access`, `podiom_request_user_action`,
`podiom_propose_goal_completion`, …) — see
[agent-tools.md](agent-tools.md) for the whole surface. Tool calls are
stamped with the calling session's identity server-side, so timeline
provenance never depends on the model identifying itself. `podiom_create_task`
takes a `goal_id` (also on `POST /api/tasks`) — the lead agent passes it when a
task is part of the goal's plan, which links the task's runs to the goal (forced
yolo, audited on the goal timeline). The lead agent starts a planned task with
`podiom_start_task`, which runs it immediately in the background under that same
yolo posture; setting a task's status to `in_progress` with `podiom_update_task`
only moves the card and starts nothing.

## Limitations (v1)

- One lead agent per goal; no shared ownership.
- No spend/token budgets or caps on how many tasks/schedules a goal may spawn —
  the audit trail is the control.
- Tasks and schedules the agent creates for the goal carry its `goal_id`, so
  their runs are autonomous (yolo) and recorded on the goal timeline. Work the
  agent creates without a `goal_id` is not linked and runs under the normal
  (stricter) unattended policy.
- Reviews only fire while `podiomd` is running; an overdue review fires on the
  next scheduler tick after restart.
