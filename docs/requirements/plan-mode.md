# Podiom Plan Mode — Requirements

*Standalone implementation spec for Plan Mode in Podiom. Self-contained: a
developer can implement from this document without reading the full Podiom
requirements. Cross-references to the main doc (e.g. §5.4, §8.7, D6, D12) and to
the memory/skills/MCP specs are for context only.*

Status: v1.0 — ready for implementation.

> Note on naming: the project is **Podiom** (renamed from "Podium", which was
> taken). Paths use `$PODIOM_HOME` / `~/.podiom/`. Earlier requirement docs still
> say `~/.podium/`; that is a pending global rename, out of scope here.

---

## 1. Purpose & philosophy

When an agent is asked to implement something risky, broad, destructive,
security-sensitive, or architecturally comprehensive, it should **stop and present
an implementation plan for the user to approve or refine before any code is
written.** The UI renders the plan as formatted Markdown alongside the chat, the
user approves or gives feedback, and only after approval does implementation
begin.

The existing Podiom `AGENTS.md` already instructs agents to write such plans to
`plans/`. This spec makes that behaviour **robust and visual**: deterministic
signalling (not text-parsing), a hard mutation gate (not a polite request), a
durable session state, and an approve/feedback loop surfaced in the UI.

Guiding principle — **mechanical gate, not good behaviour.** We do not merely
*hope* the agent presents a plan before building. Where it matters, Podiom makes
building **mechanically impossible** until a plan is approved. This mirrors the
rest of Podiom: deterministic signals and gates, never heuristics over natural
language.

---

## 2. The robustness model (two layers)

An LLM cannot be *guaranteed* to emit an exact string at exactly the right moment
— that is the same probabilistic weakness as trusting `AGENTS.md` instructions
absolutely. Robustness therefore comes from two layers working together, not from
string-parsing the agent's prose.

### 2.1 Layer 1 — deterministic signal via a Podiom tool

- **P1** Podiom exposes a dedicated MCP tool, **`podiom_submit_plan`**, which the
  agent calls when its plan is ready. The call is a **structured event** (a
  `tool_use` in Claude stream-json / the Codex app-server protocol) — not prose to
  be parsed. Seeing that tool call is how Podiom knows, deterministically, that a
  plan exists and is ready.
- **P2** The agent still **writes the plan file** to
  `$PODIOM_HOME/projects/<project>/plans/` (per the existing `AGENTS.md`), for
  durability, inspectability, and git-friendliness. `podiom_submit_plan` carries
  (or references) that file. File = the durable artifact; the tool call = the
  deterministic trigger.
- **P3** Podiom never relies on filesystem-watching or natural-language parsing to
  detect a plan. (No race against a half-written file; no confusing the agent
  *explaining* plan mode with it *submitting* a plan.)

### 2.2 Layer 2 — the mutation gate

A structured signal only tells us *when the agent chooses to submit*. It does not
stop an agent from starting to build without submitting. The gate does.

- **P4** While a session is in the **awaiting-plan-approval** state (§4), Podiom's
  permission server **auto-denies every mutating tool call** (file writes, command
  execution, anything with side effects), returning an instruction to the agent:
  *"A plan must be approved before implementation — call `podiom_submit_plan` with
  your plan."*
- **P5** **Non-mutating exploration is allowed** through the gate (reading files,
  searching) so the agent can make the plan accurate — exactly what the existing
  `AGENTS.md` already permits.
- **P6** The gate makes compliance self-correcting: if the agent "forgets" and
  tries to write code, it bounces off the gate and is steered back to submitting a
  plan. The only way forward *is* to submit the plan. This is the real guarantee
  behind "nothing gets built before approval" — not the prompt.

### 2.3 Prompt + gate, not prompt alone

- **P7** The `AGENTS.md` instruction (when a plan is needed; how to signal via
  `podiom_submit_plan`) makes the agent do the right thing in the normal case, so
  the gate rarely has to fire — it is a safety net, not daily friction. The gate
  guarantees the outcome even when the prompt slips (a bad generation, a new model
  version, work riskier than the agent realised).

---

## 3. When plan mode is active (three cases)

- **P8 — Explicit plan mode (gate on from the start).** The user selected
  **"Create plan before implementation"** (available both when creating/editing a
  roadmap task and when starting a chat, §6). The session begins gated: nothing is
  built until a plan is approved, regardless of the agent's own judgement. This is
  the deterministic guarantee for "I want to be sure nothing is built until I
  confirm."
- **P9 — Agent-judged plan mode (gate activates on submit).** The user did *not*
  select the option, but `AGENTS.md` instructs the agent to judge whether the work
  is risky/comprehensive and, if so, submit a plan. Here we lean on the prompt;
  when the agent does submit, the signal is still deterministic (P1), and the gate
  engages for the approval loop.
- **P10 — No plan mode.** Routine work; no gate; the agent builds directly.
- **P11** The honest limit of P9: Podiom cannot *fully guarantee* an agent
  self-identifies every case needing a plan. Strong prompting (§7) mitigates this;
  the explicit option (P8) is the safe path when it truly matters. This limit must
  be documented, not hidden.

---

## 4. Session state & the approval loop

- **P12** Awaiting approval is **session state persisted in SQLite** (main D6), an
  agent/session-level status **`awaiting_plan_approval`**. The session is left
  **open with no end time**, labelled to indicate it is waiting for plan approval.
- **P13** State — not just a UI label — is what **activates the mutation gate**
  (§2.2). One truth, two consumers: the UI reads it to show "awaiting approval";
  the permission server reads it to block mutations.
- **P14 — Roadmap tasks.** If the session backs a roadmap task, the task moves to
  status **"Review"** while awaiting approval.
- **P15 — The loop (per user decision):**
  - **Approve the initial plan** → the gate lifts; the agent proceeds and builds.
  - **Give feedback** → the agent revises the plan, **overwriting the same file**
    (no versioning — main-doc parity), calls `podiom_submit_plan` again, and the
    session **stays** in `awaiting_plan_approval`. This repeats as a loop until the
    user approves.
  - **Reject** → the plan file is **deleted**, the session leaves plan mode, and
    the user may re-select plan mode in the chat if they want a fresh plan.
- **P16 — Survives provider/profile switch (per user decision).** Because plan
  state is session state and the plan content lives in the session history (and on
  disk), a mid-plan provider switch (e.g. rate-limit failover) preserves the plan:
  the new backing session is replayed with the history, which contains the plan and
  its state, giving the new provider full context (main §8.5 durability model).
- **P17 — Survives daemon restart.** Plan state is durable (SQLite), so a
  `podiomd` restart resumes an awaiting-approval session in the same state.

---

## 5. Plan mode × yolo (phase-separated permission)

Plan mode's gate relies on the permission server. `yolo` bypasses it
(`bypassPermissions` / `approvalPolicy: never`). Rather than degrade the guarantee
in yolo, Podiom **separates the phases**: the gate applies during *planning*; yolo
applies during *building*.

- **P18 — Planning phase runs gated even under yolo.** If a session is `yolo` when
  a plan session starts, Podiom runs the **planning phase in a synthetic
  "plan-gate" permission mode** (defined below), *not* in yolo — so the mutation
  gate works.
- **P19 — The "plan-gate" mode is its own thing, not `approve`.** During planning
  Podiom: **allows non-mutating exploration freely without prompting the user**,
  and **blocks all mutating calls** (the gate). It is *stricter* than yolo (blocks
  mutation) but *quieter* than approve (never asks the user about reads). Do not
  implement it as `approve` — the distinction is what keeps planning frictionless
  yet gated.
- **P20 — This is a process transition, not a flag flip.** `yolo` (and any
  permission posture) is set when the **CLI process starts** — it cannot be toggled
  mid-process. So "adjust during planning, restore yolo for building" means:
  run the **planning phase in a process started in plan-gate mode**, and on
  approval, **tear down and re-create the backing CLI session in yolo mode** for
  the build phase, replaying history so the Podiom session is unbroken. This reuses
  the exact backing-session-transition + replay mechanism already built for
  account/provider switching — planning→building is one more backing-session
  transition.
- **P21 — Approval is authorisation of the whole build in yolo.** On the
  planning→building transition, the session goes from "cannot mutate anything" to
  "can mutate anything without asking" (yolo). This is correct — the user approved
  the plan and chose yolo — but it means **approving the plan is implicitly
  approving everything the build does in yolo.** The approval UI, when the session
  is yolo, MUST make this explicit ("Approving releases the build to run
  unattended"), so the large privilege swing is visible, not assumed. (Same honesty
  principle as the yolo security posture in the main doc.)
- **P22 — In `approve` (non-yolo) sessions**, planning and building both run under
  the normal permission server: the gate during planning, per-action relay during
  building. No process transition is needed for permission reasons (though one may
  still occur for other reasons).

---

## 6. Activation controls (UI entry points)

- **P23** **"Create plan before implementation"** is offered in two places, both
  setting the session to explicit plan mode (P8):
  - when **creating or editing a roadmap task**, and
  - when **starting a chat**.
- **P24** Selecting it guarantees the session starts gated (P8) — the user's
  assurance that nothing is built before they confirm.

---

## 7. Prompting commitments (AGENTS.md, strengthened)

The base `AGENTS.md` (Podiom-owned, inherited by all agents) is where the prompt
layer lives. To minimise reliance on the gate firing (and to address the concern
that an agent might build without planning), the instruction MUST:

- **P25** State clearly **when** a plan is required (risky, broad, destructive,
  security-sensitive, architecturally comprehensive work), keeping the existing
  criteria.
- **P26** State that the agent signals readiness by **calling
  `podiom_submit_plan`** (the deterministic path), in addition to writing the plan
  file to `plans/`.
- **P27** State that **non-mutating exploration is allowed** before the plan, but
  **no mutating action may be taken** until the plan is approved — matching the
  gate, so the agent's model of the rules matches Podiom's enforcement.
- **P28** Specify the plan's required contents (retained from current `AGENTS.md`):
  goal; why the work is risky/comprehensive; intended files/subsystems;
  implementation approach; tests and verification; rollback/recovery notes; open
  questions.
- **P29** Specify the file location and naming:
  `$PODIOM_HOME/projects/<project>/plans/YYYYMMDD-HHMM-<short-topic>.md`, creating
  the directory if absent; overwrite the same file on revision (P15).

---

## 8. Surfaces

### 8.1 UI (design done separately in Claude Design — see companion brief)
- **P30** The UI must, on the **same page as the chat**: detect the
  `awaiting_plan_approval` state, render the plan as **formatted Markdown**
  alongside the conversation, and present **Approve / Give feedback / Reject**
  actions. Feedback is free text that goes back to the agent as the next turn.
- **P31** When the session is yolo, the approval action must surface the
  "releases the build to run unattended" meaning (P21).
- **P32** This spec defines *what* the UI must expose and the states it reflects;
  visual/interaction design is in the companion Claude Design brief.

### 8.2 CLI
- **P33** `podiom plan show [<session>]` — print the current plan Markdown for a
  session awaiting approval.
- **P34** `podiom plan approve <session>` — approve; lifts the gate (and, in yolo,
  triggers the planning→building transition, P20).
- **P35** `podiom plan feedback <session> "<text>"` — send feedback; agent revises
  (loop, P15).
- **P36** `podiom plan reject <session>` — reject; deletes the plan file, leaves
  plan mode (P15).
- **P37** `podiom plan status [<session>]` — show whether a session is awaiting
  approval and since when.

---

## 9. Out of scope (v1) / future

- **Plan versioning** — revisions overwrite the same file (user decision, P15).
- **Partial approval** of a plan — v1 is binary approve/reject + free-text
  feedback; per-section approval is deliberately excluded as disproportionate
  complexity.
- **Full guarantee in agent-judged mode (P9)** — cannot be mechanically
  guaranteed; mitigated by prompting and by the explicit option (P8).
- **Auto-detection of "risky" work by Podiom itself** — classification stays with
  the agent (prompt) and the user (explicit option); Podiom does not attempt its
  own risk classifier in v1.

---

## 10. Acceptance checks

A correct implementation satisfies all of:

1. Starting a chat / roadmap task with "Create plan before implementation" gates
   the session from the start; no mutating action succeeds until approval
   (P8/P4).
2. The agent's `podiom_submit_plan` tool call — not any text string — is what
   moves the session to `awaiting_plan_approval` and renders the plan (P1/P12).
3. While awaiting approval, non-mutating reads succeed and mutating calls are
   denied with the steering message (P4/P5).
4. Approve → gate lifts, build proceeds (P15). Feedback → same file overwritten,
   agent re-submits, still awaiting (P15). Reject → plan file deleted, session
   leaves plan mode (P15).
5. A mid-plan provider/profile switch preserves the plan and its state; the new
   provider has the plan in context (P16).
6. A `podiomd` restart resumes an awaiting-approval session unchanged (P17).
7. A yolo session runs the planning phase gated (plan-gate mode: reads free,
   mutations blocked, user not prompted), then on approval transitions the backing
   session to yolo for building via teardown+replay (P18–P20).
8. The approval UI, in a yolo session, makes explicit that approval releases the
   build to run unattended (P21).
9. `podiom plan show/approve/feedback/reject/status` behave per §8.2.
10. The base `AGENTS.md` instruction matches the gate's rules (non-mutating
    allowed, mutating blocked until approval) so the agent's model and Podiom's
    enforcement agree (P27).
