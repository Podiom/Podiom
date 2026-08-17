# Scheduling

Podiom runs agent routines from an embedded scheduler inside `podiomd` (R7.1).
A routine fires on a clock, when an outside service calls its webhook, or both.
A **schedule is a single self-describing markdown file** under
`~/.podiom/schedules/<name>.md`: YAML frontmatter declares the job, the markdown
body is the task the agent is prompted with. The files are the source of truth —
there is no `schedules:` block in `config.yaml`. Drop a file in the folder and it
registers within ~15 seconds (or immediately on the next `podiom schedules`
command / daemon restart).

## File format

```markdown
---
agent: jared              # required — the agent that runs the task
model: ""                 # optional — overrides the agent default
effort: low               # optional — provider-supported reasoning effort
cron: "0 7 * * *"         # 5-field cron OR `every: 6h` (at most one)
webhook: false            # optional — also fire when an external POST arrives
webhook_secret: ""        # required when webhook is true; Podiom generates it
run_permission: preapproved   # preapproved (default) | yolo
allowed_tools: []         # preapproved allow-list (empty = deny all side effects)
enabled: true             # off switch — a disabled file stays but does not fire
goal_id: ""               # optional — set when a goal's plan created this schedule
project: ""               # optional — project id the runs work in
created_by_session: ""    # optional — the agent session that created this file
created_by_agent: ""      # optional — the agent that created it
---

Summarise today's calendar and add a short note to the "daily-briefs" project.
Keep it to three lines.
```

- The schedule **name** is the filename without `.md`.
- A schedule needs **at least one trigger**: `cron:`, `every:`, or `webhook:`.
  Use either `cron:` (standard 5-field expression) **or** `every:` (a Go duration
  like `6h`, `30m`, `90s`), not both. `webhook:` is independent of the cadence —
  a schedule can be clock-driven, webhook-driven, or both. A webhook-only file
  has no cadence at all and reports no next-run time.
- `enabled` defaults to `false` when omitted — set `enabled: true` to let a
  routine fire. A disabled file is kept and listed but never fires automatically.
- `goal_id` links this schedule back to a goal (see `goals.md`): the lead
  agent sets it when creating a schedule as part of a goal's plan, and the
  Schedules page highlights and links any schedule that carries one. Leave it
  unset for schedules you create yourself.
- `project` binds the runs to a project: each run works in the project's
  directory and receives its standing instructions, exactly like a roadmap task
  session. A schedule created for a goal inherits the goal's project — Podiom
  writes it into the file, so the workspace the runs will use is visible on disk
  rather than only derived when the schedule fires — unless the creator named a
  different one. A goal-linked file written before this field existed falls back
  to the goal's project at run time, so nothing needs migrating.
- `created_by_session` / `created_by_agent` record which agent decided this
  schedule should exist and the conversation it came out of. Podiom writes them
  from the agent's own session identity — the agent never supplies them — and the
  Schedules page shows a **created by** chip that opens that conversation. Both
  are absent on a schedule you wrote yourself, which is how a human-authored file
  is told apart from an agent's. An edit never rewrites them. See
  [agent-tools.md](agent-tools.md).

## Webhook triggers

Set `webhook: true` and the schedule also fires when something POSTs to it. Use
it for work that should react to an event rather than to the clock: a push to a
repository, a finished CI run, an automation step, a button in a home
controller.

**Getting the URL.** Podiom generates `webhook_secret` when the trigger is
created — you never write it yourself, and a file that sets `webhook: true`
without a secret is rejected rather than left callable by anyone. The Schedules
page shows a **Copy** button for the full URL on any schedule that has one;
`GET /api/schedules/<name>` and `podiom_get_schedule` return the secret too.

```
POST http://<your-podiom-host>/api/schedules/<name>/webhook
```

**Presenting the secret.** Any one of these works — pick whichever your sender
can produce:

```sh
curl -X POST "$PODIOM/api/schedules/on-push/webhook?secret=$SECRET" -d '{"ref":"main"}'
curl -X POST "$PODIOM/api/schedules/on-push/webhook" -H "X-Podiom-Webhook-Secret: $SECRET" -d '{}'
curl -X POST "$PODIOM/api/schedules/on-push/webhook" -H "Authorization: Bearer $SECRET" -d '{}'
```

**The payload reaches the run.** The request body (up to 8KB; the request
itself is capped at 64KB) is appended to the agent's prompt under a
`## Webhook payload` heading, so the task can act on what fired it rather than
only on the fact that it fired.

**Responses.**

- `202 Accepted` with the run record. The endpoint does not wait for the agent —
  a run takes minutes and senders time out in seconds. Follow the run through
  `GET /api/schedules/<name>`.
- `401` for a wrong or missing secret, an unknown schedule name, and a schedule
  with no webhook trigger. These are deliberately indistinguishable: the
  endpoint does not require the gateway token, so it must not double as a way to
  discover which schedules exist.
- `409` when the secret is valid but the schedule is `enabled: false`. Parking a
  schedule stops its webhook as well as its cadence.

**Rotating a secret.** There is no rotate call. Set `webhook: false` and then
`webhook: true` again (via PATCH, `podiom_update_schedule`, or by editing the
file): turning the trigger off retires the old secret, and turning it back on
mints a fresh one.

**Reachability.** The endpoint is exempt from the gateway token but not from
everything else. If you set `allow_from` in `config.yaml`, or run the Home
Assistant add-on, the source-IP guard still applies and an outside sender is
refused until its address is allow-listed. The Home Assistant mobile port does
not expose this exemption: its API-only listener requires the gateway token for
every `/api/` request, including schedule webhook paths. Use a separately
secured reverse proxy if a third-party sender must reach a webhook on an HA
install. See [security.md](security.md).

## Each run is a normal session

A fired schedule executes as an ordinary Podiom session against the named agent
in its `workspace/`, with the full composed identity (base `AGENTS.md` +
per-agent `AGENTS.md` + `SOUL.md`) delivered exactly as in interactive chat
(R7.3a). The run is recorded with:

- `origin = schedule`,
- the originating `schedule_id` and `run_id`,

so you can **revisit a scheduled run's session and continue it manually**, and
filter sessions by schedule (R7.9 / R4.12).

## Unattended permissions (§7.7)

A scheduled run has no human to answer an approval prompt, so each routine
declares how it handles permission requests via `run_permission`:

- **`preapproved`** (default, stricter — R7.8): the run executes in approve mode
  with an allow-list. Tools named in `allowed_tools` are auto-approved; anything
  else is **auto-denied**, never queued for a human. An empty `allowed_tools`
  (the default) denies all side-effecting actions. On Claude this uses the native
  `--allowedTools`; on Codex the in-process allow-list relay plus a read-only
  sandbox.
- **`yolo`**: whole-machine auto-approval (§5.5). A deliberate, strong opt-in for
  trusted routines only — there is no human oversight.

## Inspecting and triggering

From the CLI (see [cli.md](cli.md)):

```sh
podiom schedules list             # timing, agent, policy, next run, run count
podiom schedules run <name>       # trigger now; prints the run + session id
```

Over HTTP (also used by the web UI):

- `GET   /api/schedules` — every schedule's state, next-run time, and recent runs.
- `GET   /api/schedules/<name>` — one schedule in full, including its body.
- `PATCH /api/schedules/<name>` — change fields in place. Only what you send is
  changed; everything else in the file survives, including its attribution. Set
  `enabled: false` to park a schedule without losing its history. Setting `cron`
  clears `every` and vice versa; `webhook` is independent of both. The name and
  `goal_id` are not patchable — the name is the filename, and the goal link
  forces `yolo`.
- `POST  /api/schedules/<name>/run` — trigger a manual run; returns the run record.
- `POST  /api/schedules/<name>/webhook` — fire a schedule from outside. The only
  schedule route that does not take the gateway token; see **Webhook triggers**
  above.
- `DELETE /api/schedules/<name>` — remove the file and its run history.

Each run records what triggered it — `cron`, `manual`, or `webhook` — alongside
the session it produced.

## Limitations (v1)

- Routines only fire while the machine is on and `podiomd` is running; boot
  persistence is deferred (R7.6).
- Routines are independent — no inter-routine dependencies (R7.4).
- Overlapping runs of the same schedule are allowed (no concurrency cap, R11.3).
