# Podiom Goals — Requirements

*Standalone implementation spec for the Goals feature of Podiom.
Self-contained: a developer can implement from this document without reading
the full Podiom requirements. Cross-references to the MCP spec and skills spec
are for context only.*

Status: v1.0 — ready for implementation.

---

## 1. Purpose & philosophy

Every unit of work in Podiom today is user-initiated: the user writes a task,
authors a schedule, or drives a chat session. **Goals invert this.** The user
states an *outcome* — not a task list — assigns it to one lead agent, and
walks away. The agent autonomously plans how to reach the outcome, works on it
over days or weeks, and comes back to the user only when it needs a decision
or believes it is done.

Guiding principles:

1. **The goal is the contract; the agent owns the plan.** The user specifies
   *what done means* (success criteria, optional metrics). The agent decides
   *how*: it decomposes the goal into roadmap tasks and schedules using the
   same primitives a human would, and revises that plan as it learns.
2. **Capability gaps become structured, auditable requests — never silent
   failure.** When the agent is missing an MCP server, a skill, a host CLI
   tool, a credential, or a permission level, it files a typed **access
   request**. The user is notified asynchronously and can approve or deny
   with one click. Approval *performs the grant* where automatable.
3. **The user is the approver — of grants and of completion.** The agent may
   *propose* completion; only the user marks a goal done. Access decisions
   are human-only; agents cannot approve their own requests.
4. **Every autonomous action is attributable.** Each timeline event links to
   the agent session that produced it, so the user can audit the full
   transcript behind any claim of progress.
5. **Goals orchestrate; they do not duplicate.** Tasks, schedules, MCP
   assignment, and marketplace installs are reused as-is. A goal is a durable
   record plus a driving loop on top of existing primitives.

## 2. Concepts & data model

### 2.1 Goal

| Field | Type | Notes |
|---|---|---|
| `id` | UUID string | |
| `title` | string | required |
| `description` | string | context a teammate would need |
| `success_criteria` | string | free text; what "done" means |
| `metrics` | []GoalMetric | optional; JSON column |
| `review_every` | duration string | e.g. `24h`; `""` disables automatic reviews; floor **15m** |
| `lead_agent` | string | exactly one agent name; required |
| `project_id` | string | optional link to a project |
| `status` | enum | `active \| paused \| review \| done \| abandoned` |
| `next_review_at` | RFC3339 | empty when paused/terminal or reviews disabled |
| `closing_report` | markdown | set when the agent proposes completion |
| `next_step` | string | agent-stated action it will take before the next review |
| `next_step_why` | string | one sentence on why that is the right move now |
| `next_step_at` | RFC3339 | when the agent stated it; empty when never stated |
| `created_at`, `updated_at` | RFC3339 | |

`GoalMetric` = `{name string, target float64, current float64, unit string}`.
The agent updates `current` over time with evidence; metric history is
derivable from `metric_update` events (§2.2).

The **next step** is the agent's statement of strategic intent — *"Post the
launch thread on r/selfhosted"*, not a restatement of a task or schedule it
created (§1 principle 5: goals orchestrate, they do not duplicate). It is
**agent-authored only**: the user has no edit affordance and steers through
`user_feedback` instead, because the agent owns the plan (§1 principle 1). It is
written solely by `podiom_record_goal_progress` — so intent cannot change
without a timeline entry saying what moved — and is cleared when completion is
proposed or the goal goes terminal. Pausing preserves it, so resuming shows the
same intent the user paused on. History is derivable from the `next_step` /
`next_step_why` keys echoed in the `payload` of the `progress` / `plan_change`
event that stated it.

### 2.2 Goal events (audit timeline)

An **append-only** journal, one row per event, `goal_events` table. Updates
are rejected at the schema level (trigger); rows are removed only by cascade
when the goal itself is deleted.

| Field | Notes |
|---|---|
| `id`, `goal_id`, `created_at` | |
| `session_id` | session that produced the event; empty for user actions from the UI |
| `kind` | see below |
| `body` | human-readable markdown |
| `payload` | kind-specific JSON (metric deltas, request id, old/new status…) |

Kinds: `created`, `planning_started`, `review_started`, `progress`,
`metric_update`, `plan_change`, `user_feedback`, `access_requested`,
`access_decided`, `status_change`, `completion_proposed`, `rate_limited`,
`rate_limit_resolved`, `action_requested`, `action_responded`.

`metric_update` events are the single write path for metric values: appending
one applies its payload (`{name, current}` deltas) to `goals.metrics_json`
in the same transaction.

`user_feedback` events are user-authored notes for strategy, constraints, or
next-step guidance. They have an empty `session_id`, are included in future
goal planning/review prompts, and do not start a chat, trigger a review, notify
the agent immediately, or change goal status/cadence.

A feedback note may be **pinned** (`goal_events.pinned`), which promotes it from
a note about this moment to a **standing directive**: binding for the goal's
whole life. The two channels differ deliberately on every axis that matters:

| | Ordinary feedback | Standing directive (pinned) |
|---|---|---|
| Reach | lead planning/review prompts | those **plus every goal-linked task and schedule run** (§4) |
| Retention | newest 20, older ones fall out | all of them, always |
| Rendering | truncated at 500 chars | in full |
| Authority | guidance; success criteria win | binding; a conflict must be escalated, not resolved silently |
| Editable | until the next planning/review run reads it | for the goal's whole life |
| Ordering in the prompt | newest first | oldest first, so a later amendment reads last |

Because a directive is rendered uncapped and untruncated, both bounds are
enforced at the pin instead: at most 10 directives per goal, each at most 2000
characters. Exceeding either is an error the user sees, never a silent drop —
nothing the agent is told to obey may vanish without someone being told.

A directive that would make a success criterion unreachable must be escalated
with `podiom_ask_user` naming both sides, rather than the agent quietly picking
a winner.

Pinning does not change the append-only contract in spirit: `pinned` is simply
absent from the `goal_events_append_only` trigger's equality list, so toggling it
is permitted on `user_feedback` rows and still aborts on every other kind.

`GET /api/goals/<id>` returns the directives as their own `directives` field
rather than leaving the client to filter `events`, which carries only the newest
`goalDetailEvents` entries. A directive pinned long ago binds every run but
would have fallen outside that window — and a rule the agent is obeying that the
user cannot see is precisely the failure the pin exists to prevent.

### 2.3 Access requests

| Field | Notes |
|---|---|
| `id`, `goal_id`, `created_at` | |
| `agent_name` | requesting (lead) agent |
| `session_id` | session that filed it |
| `kind` | `mcp_server \| skill \| cli_tool \| env_var \| permission_mode` |
| `payload` | kind-specific JSON, §6 |
| `reason` | agent-written justification |
| `status` | `pending → approved \| denied`; automatable kinds continue `approved → executed \| failed` |
| `decision_note` | optional user note, **relayed to the agent at its next review** — this is how the user talks back |
| `execution_error` | set when an automatic grant fails |
| `decided_at`, `executed_at` | RFC3339 |

**Requests never carry secrets.** An `env_var` request carries the variable
*name* and purpose only; validation rejects anything value-shaped. The user
may supply the value once at approval (human-only decide endpoint): it is
stored in `credentials.yaml` (0600) and injected into agent subprocess
environments — never persisted on the request row, never returned by any
API response, and never logged. Approval without a value remains
acknowledge-only (the user sets the variable on the host themselves).

### 2.4 Action items

A step the agent decided only a human can carry out — posting from the user's
personal account, signing or paying for something, a phone call, anything
off-machine. Without this, such a step has nowhere to live: an access request is
a capability Podiom can wire, a question (§9) is a *decision*, and `next_step`
(§2.1) is the agent's own move. The agent would otherwise bury the ask in a
`progress` body, where the user cannot respond to it.

| Field | Notes |
|---|---|
| `id`, `goal_id`, `created_at` | `goal_id` cascades on goal delete |
| `session_id`, `run_id`, `agent_name` | the run that filed it, for attribution |
| `title` | the one-line imperative ask; required |
| `instructions` | markdown the user can act on without knowing the plan |
| `why` | one sentence on why it needs a human |
| `status` | `open → done \| blocked \| declined` |
| `response` | the user's free-text note, written with the verdict |
| `responded_at` | RFC3339 |

Design constraints, each deliberate:

- **Non-blocking.** An open item never suppresses a review — it is a hand-off,
  not a gate. This is the opposite of a question (§9), which does pause reviews,
  and the distinction is what stops one un-actioned errand from freezing a goal
  for days. `ListDueGoalReviews` therefore excludes goals on pending *questions*
  and rate limits, but never on open action items.
- **Store-and-replay, not delivery.** Responding appends `action_responded` and
  nothing else: no run is started and the review clock is untouched, the same
  contract as `user_feedback` (§2.2) and access-request decision notes. The
  verdict and note reach the agent in its next planning or review prompt (§4).
  "Review now" is the user's lever if they want it acted on immediately.
- **A verdict is given once.** The status update is guarded on `open`, so a
  second response fails rather than silently rewriting what the agent may
  already have read. Answered items stay on the goal read-only.
- **The verdict is structured, the reason is prose.** `done | blocked |
  declined` is a value the agent can branch on ("couldn't do" means find another
  route; "not doing" means drop the approach); the note carries the detail.
- **Goal chain only.** Origin is derived from the filing session's `goal_id`, so
  a goal-linked task or schedule run files onto the goal it belongs to — the
  same routing precedence questions use. A standalone scheduled run has no goal
  to hand work back on and the tool is rejected there.

## 3. Lifecycle & state machines

### 3.1 Goal status

```
            ┌──────────── user: pause ───────────┐
            ▼                                     │
active ⇄ paused        active ── agent: propose ──► review
  ▲                                     │ user: mark done
  │◄──────── user: reopen ──────────────┤ user: reopen → active
  │                                     ▼
  └── user: reopen ── done / abandoned ◄┘  (user: abandon from any state)
```

- `review` is entered **only** via agent proposal (`completion_proposed`),
  which also sets `closing_report`.
- `done` is reachable **only** from `review`, and only by the user.
- `abandoned` is user-only, allowed from any non-terminal state.
- Reopening (from `review`, `done`, or `abandoned`) returns to `active` and
  recomputes `next_review_at`.
- Pausing suspends reviews (`next_review_at` cleared); resuming recomputes it.
- Every transition appends a `status_change` event.
- Agents may not change `status` or `lead_agent` through their tools (§9).

### 3.2 Access request status

`pending → approved | denied` (user decision, exactly once).
Automatable kinds (`mcp_server`, `skill`, `permission_mode`) then run grant
execution: `approved → executed | failed`. A `failed` request stays
actionable in the UI (retryable approve). Acknowledge-only cases
(host-only `cli_tool`, `env_var` approved without a value) terminate at
`approved`; the user acts manually and the agent re-detects availability at
its next review. An `env_var` approved **with** a value continues to
`executed` (credential stored and injected) or `failed` (retryable).

## 4. Planning & review sessions

- New session origin: **`goal`** (alongside `web|cli|onboarding|schedule|
  roadmap`). Goal sessions carry `goal_id` for attribution.
- **Planning session** — created immediately (asynchronously) when a goal is
  created, for the lead agent. Prompt contract: the goal's full definition +
  **standing directives** (§2.2) + recent `user_feedback` events + instructions to (a) decompose into roadmap
  tasks (`podiom_create_task`, delegating to other agents where sensible)
  and/or schedules (`podiom_create_schedule`), (b) record the plan via
  `podiom_record_goal_progress` (kind `plan_change`) **including `next_step` and
  `next_step_why`** (§2.1), (c) file `podiom_request_access` for any missing
  capability, and (d) hand any human-only step to the user with
  `podiom_request_user_action` (§2.4). Feedback is guidance, not a direct
  conversation, and must not override explicit success criteria; standing
  directives are binding and do bind the tasks and schedules the agent creates.
- **Delegated runs** — a goal-linked task (`RunTaskTurn`) or schedule
  (`RunScheduled`) is prompted with its own text alone, with one exception: the
  goal's standing directives are prefixed, under a one-line goal header giving
  them context. Without this the goal's rules reach the agent that plans and
  never the agent that does, since the lead is told to push substantial work
  into exactly those runs. A goal with no directives leaves such a prompt
  byte-identical, and a dangling `goal_id` degrades to no preamble rather than
  failing the run.
- **Review session** — fired on the goal's cadence (§5) or manually
  ("Review now"). Prompt contract: goal definition + **standing directives**
  (§2.2) + recent `user_feedback`
  events + recent timeline + decided access requests **including
  `decision_note` texts** + **every open action item with the date it was filed,
  and the recently answered ones with their verdict and note** (§2.4) + duties:
  assess progress against criteria, adjust
  tasks/schedules while considering feedback as guidance (starting a planned task
  is `podiom_start_task`, which runs it in the background; `podiom_update_task`
  to `in_progress` only moves the card), record a `progress`
  event with evidence and metric updates, file access requests if blocked, hand
  human-only steps over and act on the verdicts already given, and
  call `podiom_propose_goal_completion` when the success criteria are met.
  The prompt also carries the goal's **current `next_step` with its age**, and the
  progress duty requires the agent to report whether that step happened before
  restating it — the loop that keeps a stated intent from going stale.
- Both prompts constrain `next_step` to a move the **agent** will make, and
  route anything belonging to the user into `podiom_request_user_action`
  instead. Without that clause the two collapse: the canonical example of a
  strategic move ("post the launch thread") is exactly the kind of step an agent
  cannot perform, so it would claim the user's work as its own intent and the
  ask would never reach them.
- Neither prompt may re-file an action item that is still open; the agent chases
  it in its progress entry instead.
- Both run **unattended** in **yolo** (full autonomous access): Claude
  `bypassPermissions`, Codex `approvalPolicy: never` + `sandbox:
  danger-full-access`. No permission relay and no allow-list are attached — a
  goal exists to reach an outcome without the user in the loop, so there is
  nobody to answer a prompt and nothing is queued for one.
- Yolo is **unconditional and not a per-goal choice**: a goal carries no
  permission field. The lead session is created yolo and *re-asserted* yolo on
  every run, so it cannot drift. The lead agent's own standing permission mode
  is overridden, not consulted — an `approve`-mode agent still runs goal work at
  full access. Because the mode is persisted on the session, a user's later
  interactive turns in that conversation are also yolo.
- The **whole delegated chain inherits it**: tasks and schedules carrying
  `goal_id` run yolo too (a goal-linked task's plan gate is bypassed, and a
  goal-linked schedule run is forced yolo even when its file still says
  `run_permission: preapproved`).
- The chain inherits the goal's `project_id` the same way, so the delegated work
  happens in the same workspace with the same standing instructions. Unlike yolo
  this is a **default, not a force**: a task or schedule that names its own
  project keeps it, because a goal may legitimately span projects.
- The counterweight is the audit trail, not a permission gate: every tool call
  the chain makes is recorded on the goal timeline (§8), and the UI must disclose
  full access — a warning on the goal-creation form and a persistent badge on the
  goal detail view.

## 5. Scheduling

Goal reviews are driven by a **database poll**, not schedule files: the
scheduler's existing resync loop (15 s tick) additionally picks up goals
where `status = 'active' AND next_review_at <= now`.

Rationale — single source of truth (pausing/completing a goal stops reviews
atomically with the status write), correct provenance (`goal` origin +
`goal_id`, not a mislabeled schedule), interval-anchored semantics ("24h
since the last review", not wall-clock cron), and downtime catch-up for free
(an overdue `next_review_at` fires on the next tick after restart).

Firing discipline: `next_review_at` is advanced (`now + review_every`) and
persisted **before** the review session runs, so a long or crashed review can
neither double-fire nor stall the cadence. Cadence strings use the same
duration vocabulary as schedules' `every:` field; the API enforces a 15 m
floor.

## 6. Access requests & grants

| Kind | Payload | Automatable | Grant execution on approve |
|---|---|---|---|
| `mcp_server` | `{"server_name"}` | **yes** | Assign the catalogue server to the lead agent — same validated path as the manual per-agent MCP assignment (see MCP spec §2: assignment is deliberately per-agent). |
| `skill` | `{"registry","id","url"}` | **yes** | Install from the skills marketplace via the existing install path. |
| `permission_mode` | `{"mode"}` | **yes** | Set the agent's permission mode. Approving `yolo` is security-sensitive: the UI must show explicit warning copy. |
| `cli_tool` | `{"tool","install_hint"}` | no | Host-wide tools only: approval acknowledges and the UI surfaces `install_hint` for the user to run manually. Podiom installs nothing from an approval — agents provision their own tools without approval through the shared toolset (`podiom_install_tool`; see the toolset spec), so any installer fields on the request are inert. |
| `env_var` | `{"var_name","purpose","target"}` | **yes, when the user enters the value at approval** | The value (supplied once in the approval dialog, human-only) is stored in `credentials.yaml` and injected into agent subprocess environments on later runs. Approving without a value acknowledges only; the user sets the variable themselves. The *request* never carries the value. |

Both approve and deny accept an optional `decision_note`, relayed verbatim to
the agent in its next review prompt.

Failure handling: if execution fails (e.g. unknown server, marketplace
error), the request lands in `failed` with `execution_error` set, remains
actionable in the UI, and the failure is visible in the goal timeline.

## 7. Notifications

Two layers, mirroring the existing permission/question notifications:

- **Push (web push)** — fired off the hot path when (a) an access request is
  filed → kind `goal_access_request`, "\<agent\> requests access"; (b)
  completion is proposed → kind `goal_review`, "\<agent\> proposes goal
  completion"; (c) an action item is filed → kind `goal_action_item`,
  "\<agent\> needs you to do something". Payload carries `goal_id`; tapping the
  notification deep-links to the goal.
- **In-app** — a `goal_event` WebSocket broadcast on every appended event and
  request decision. The frontend raises toasts for `access_requested` and
  `completion_proposed`, keeps a goal-attention set (goals with pending or
  failed requests, an unanswered question, an open action item, or in `review`)
  for the Goals nav badge, and live-refreshes an open goal without polling.

The Goals list is **triaged for the returning user**: goals needing attention
(review status, pending or failed requests, open action items) sort first and
are visually distinct.

Action items are surfaced on the goal detail as a horizontally scrolling card
carousel rather than a stacked list: a goal accumulates them over its life, and
each card carries markdown instructions plus a response control, so stacking
would bury everything below it. Open items lead, oldest ask first; answered ones
follow read-only.

## 8. Audit

- The timeline (§2.2) is the audit surface: reverse-chronological, paginated,
  each event visually differentiated by kind, each linking to the producing
  session's transcript.
- Metric progress is auditable: every change is a `metric_update` event with
  old → new values; current values on the goal are a projection of these
  events.
- Access decisions are auditable: `access_requested` and `access_decided`
  events bracket every grant, including denials and execution failures.
- Hand-offs to the user are auditable the same way: `action_requested` and
  `action_responded` bracket every action item, so what the agent asked for and
  what the user answered are both on the record. Like `access_decided`, the
  response event carries no `session_id` — it is the user acting.
- Append-only is enforced in the schema, not by convention.

## 9. Agent tool surface

Exposed through the internal `podiom_manage` MCP server every agent already
receives. Tool calls are stamped with the calling session/agent identity
server-side, so provenance never depends on the model remembering to pass it.

| Tool | Purpose |
|---|---|
| `podiom_list_goals` | list goals (filter by status) |
| `podiom_get_goal` | goal + recent events + pending/decided access requests |
| `podiom_update_goal` | description / success criteria / cadence only — **status and lead agent are rejected** |
| `podiom_record_goal_progress` | append `progress` or `plan_change` event; optional metric updates; optional `next_step` / `next_step_why` (§2.1) |
| `podiom_list_goal_events` | paginated timeline |
| `podiom_propose_goal_completion` | closing report → status `review` + notification |
| `podiom_request_access` | file a typed access request |
| `podiom_list_access_requests` | see prior decisions incl. `decision_note` |
| `podiom_request_user_action` | hand a human-only step to the user (§2.4); non-blocking |
| `podiom_ask_user` | ask a blocking decision question; **pauses reviews** until answered |

The last two are the agent→user channels for anything a capability grant cannot
fix, and the tool descriptions must draw the line between them explicitly:
`podiom_ask_user` is a *decision* and stops the review loop,
`podiom_request_user_action` is *work* and does not. Open action items are
returned by `podiom_get_goal` so the agent can see what it is already waiting on
and avoid re-filing.

Deliberately absent: create/delete goal (goals are user-created), and any
approve/deny surface (decisions are human-only). `podiom_update_goal` also
deliberately cannot set `next_step` — routing it through
`podiom_record_goal_progress` alone keeps the stated intent tied to a timeline
entry explaining what moved.

User feedback is also human-only: `POST /api/goals/<id>/feedback` appends a
`user_feedback` event, and there is deliberately no agent tool for creating
one. **Pinning is human-only for a sharper reason**: a standing directive is a
guardrail, and a guardrail the agent can unpin is not a guardrail. No tool maps
to the pin toggle; agents see `Pinned` on the events they read and nothing more. Responding to an action item is human-only for the same reason: the agent
files it via `podiom_request_user_action`, and no tool maps to
`POST /api/goal-action-items/<id>/respond` so an agent can never report on the
user's behalf.

## 10. Security considerations

- **Provenance stamping is convention, not authentication.** All manage-tool
  traffic already runs with the daemon's authority; the stamp exists for
  attribution, not access control.
- **Delegation is unbounded in v1.** A goal session may create tasks and
  schedules for any agent. The audit trail is the control; per-goal caps and
  budgets are explicitly out of scope (§11) and should be revisited if goals
  misbehave in practice.
- **`permission_mode` grants change the agent globally**, not per-goal. The
  approve dialog must say so, with strong warning copy for `yolo`.
- **No secrets in payloads**, enforced at validation (§6).

## 11. Out of scope for v1

- Spend / token budgets per goal, and caps on spawned tasks/schedules.
- Multi-agent shared ownership of one goal.
- Structured metric history charts (derivable later from events).
- Notification action buttons (approve from the push notification itself).
- Agent-created goals.
