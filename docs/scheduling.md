# Scheduling

Podiom runs recurring agent routines from an embedded scheduler inside `podiomd`
(R7.1). A **schedule is a single self-describing markdown file** under
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
cron: "0 7 * * *"         # 5-field cron OR `every: 6h` (exactly one)
run_permission: preapproved   # preapproved (default) | yolo
allowed_tools: []         # preapproved allow-list (empty = deny all side effects)
enabled: true             # off switch — a disabled file stays but does not fire
goal_id: ""               # optional — set when a goal's plan created this schedule
---

Summarise today's calendar and add a short note to the "daily-briefs" project.
Keep it to three lines.
```

- The schedule **name** is the filename without `.md`.
- Use either `cron:` (standard 5-field expression) **or** `every:` (a Go duration
  like `6h`, `30m`, `90s`), not both.
- `enabled` defaults to `false` when omitted — set `enabled: true` to let a
  routine fire. A disabled file is kept and listed but never fires automatically.
- `goal_id` links this schedule back to a goal (see `goals.md`): the lead
  agent sets it when creating a schedule as part of a goal's plan, and the
  Schedules page highlights and links any schedule that carries one. Leave it
  unset for schedules you create yourself.

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

- `GET  /api/schedules` — every schedule's state, next-run time, and recent runs.
- `POST /api/schedules/<name>/run` — trigger a manual run; returns the run record.

## Limitations (v1)

- Routines only fire while the machine is on and `podiomd` is running; boot
  persistence is deferred (R7.6).
- Routines are independent — no inter-routine dependencies (R7.4).
- Overlapping runs of the same schedule are allowed (no concurrency cap, R11.3).
