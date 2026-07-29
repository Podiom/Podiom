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
| `created_at`, `updated_at` | RFC3339 | |

`GoalMetric` = `{name string, target float64, current float64, unit string}`.
The agent updates `current` over time with evidence; metric history is
derivable from `metric_update` events (§2.2).

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
`rate_limit_resolved`.

`metric_update` events are the single write path for metric values: appending
one applies its payload (`{name, current}` deltas) to `goals.metrics_json`
in the same transaction.

`user_feedback` events are user-authored notes for strategy, constraints, or
next-step guidance. They have an empty `session_id`, are included in future
goal planning/review prompts, and do not start a chat, trigger a review, notify
the agent immediately, or change goal status/cadence.

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
  recent `user_feedback` events + instructions to (a) decompose into roadmap
  tasks (`podiom_create_task`, delegating to other agents where sensible)
  and/or schedules (`podiom_create_schedule`), (b) record the plan via
  `podiom_record_goal_progress` (kind `plan_change`), and (c) file
  `podiom_request_access` for any missing capability. Feedback is guidance, not
  a direct conversation, and must not override explicit success criteria.
- **Review session** — fired on the goal's cadence (§5) or manually
  ("Review now"). Prompt contract: goal definition + recent `user_feedback`
  events + recent timeline + decided access requests **including
  `decision_note` texts** + duties: assess progress against criteria, adjust
  tasks/schedules while considering feedback as guidance (starting a planned task
  is `podiom_start_task`, which runs it in the background; `podiom_update_task`
  to `in_progress` only moves the card), record a `progress`
  event with evidence and metric updates, file access requests if blocked, and
  call `podiom_propose_goal_completion` when the success criteria are met.
- Both run **unattended** with the scheduled-run permission posture:
  pre-approved allow-list (read-only file tools + the `podiom_*` management
  tools), *not* yolo — unless the agent's own permission mode is yolo (e.g.
  granted via a `permission_mode` request).

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
| `cli_tool` | `{"tool","install_hint"}` (+ optional installer fields) | **yes, when installer fields are present** | With `installer` (`npm\|uv\|go\|binary`) the tool is installed into the requesting agent's own workspace and exposed on its PATH — see the workspace-tool-installs spec. Without `installer` (host-wide tools): approval acknowledges and the UI surfaces `install_hint` for the user to run manually. |
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
  completion". Payload carries `goal_id`; tapping the notification deep-links
  to the goal.
- **In-app** — a `goal_event` WebSocket broadcast on every appended event and
  request decision. The frontend raises toasts for `access_requested` and
  `completion_proposed`, keeps a goal-attention set (goals with pending
  requests or in `review`) for the Goals nav badge, and live-refreshes an
  open goal without polling.

The Goals list is **triaged for the returning user**: goals needing attention
(review status, pending or failed requests) sort first and are visually
distinct.

## 8. Audit

- The timeline (§2.2) is the audit surface: reverse-chronological, paginated,
  each event visually differentiated by kind, each linking to the producing
  session's transcript.
- Metric progress is auditable: every change is a `metric_update` event with
  old → new values; current values on the goal are a projection of these
  events.
- Access decisions are auditable: `access_requested` and `access_decided`
  events bracket every grant, including denials and execution failures.
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
| `podiom_record_goal_progress` | append `progress` or `plan_change` event; optional metric updates |
| `podiom_list_goal_events` | paginated timeline |
| `podiom_propose_goal_completion` | closing report → status `review` + notification |
| `podiom_request_access` | file a typed access request |
| `podiom_list_access_requests` | see prior decisions incl. `decision_note` |

Deliberately absent: create/delete goal (goals are user-created), and any
approve/deny surface (decisions are human-only).

User feedback is also human-only: `POST /api/goals/<id>/feedback` appends a
`user_feedback` event, and there is deliberately no agent tool for creating
one.

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
