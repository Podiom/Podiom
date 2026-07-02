# Podiom Agent Memory ("Dreaming") — Requirements

*Standalone implementation spec for per-agent persistent memory in Podiom.
Self-contained: a developer can implement from this document without reading the
full Podiom requirements. Cross-references to the main doc (e.g. §5.2, §5.4, §7,
D6) are for context only.*

Status: v1.0 — ready for implementation.

---

## 1. Purpose & philosophy

Podiom agents should **feel like they live through time** — accumulating an
understanding of the user and of how the two work together, rather than
resurrecting with amnesia each session. This feature gives every agent a
**persistent, self-curated memory** (`MEMORY.md`) that grows from actual
interaction, plus a nightly consolidation process — **"dreaming"** — in which the
day's sessions are distilled into durable memory.

Guiding principles:

1. **SOUL is design; MEMORY is experience.** `SOUL.md` is the user-authored,
   static identity (who the agent *is*). `MEMORY.md` is the agent-authored,
   growing record of what it has *learned* (about the user, preferences, working
   patterns). The user owns the soul; the agent owns the memory — under the
   user's full visibility and veto.
2. **Podiom owns the memory, provider-neutrally.** Memory lives in the agent's
   Podiom directory and is delivered to whichever backend runs a turn. It is
   **not** stored in Claude's or Codex's native memory systems, because those are
   per-CLI, machine-local, region-restricted (Codex native Memories are
   unavailable in the EEA/UK/CH), and would split an agent's learning across
   providers — breaking the guarantee that a Podiom agent is the *same agent*
   regardless of which provider backs a turn.
3. **Memory grows only from real interaction.** No sessions on a given day →
   nothing to dream about → memory unchanged. An agent you don't talk to does not
   drift.
4. **Unfiltered in topic, curated in volume.** The agent may remember anything
   (no subject bans — see §6), but dreaming distills only what is *durably
   significant*, keeping `MEMORY.md` within an injection budget. Unfiltered in
   category; curated in amount.
5. **User-editable and authoritative.** The user can read, edit, and clear
   `MEMORY.md` at any time, and the user's edits win over the dream (§4.4).

---

## 2. Where memory lives

- **MEM1** Each agent has a `MEMORY.md` at its agent root, beside `SOUL.md`:

  ```
  ~/.podiom/agents/<name>/
    SOUL.md          # who the agent IS       (user-authored, static identity)
    MEMORY.md        # what the agent LEARNED  (agent-authored, growing)
    AGENTS.md        # per-agent instructions  (optional, user-authored)
    workspace/       # agent-local cwd
  ```

- **MEM2** `MEMORY.md` is created (empty) when the agent is created, alongside
  `SOUL.md`. An empty memory is valid and simply contributes nothing until the
  first dream.

## 3. Delivery — the fourth composition layer

- **MEM3** `MEMORY.md` becomes the **fourth layer** in the instruction
  composition Podiom already builds (see main §5.4). Effective standing context =
  base `AGENTS.md` + per-agent `AGENTS.md` + `SOUL.md` + **`MEMORY.md`**, in that
  order (memory last, so the agent's learned context sits closest to the live
  turn).
- **MEM4** Delivery reuses the existing, verified mechanism — **no new delivery
  path**: `@`-import on Claude (generated context file), concatenated bundle on
  Codex. Adding memory is one more file in a pipeline that already exists.
- **MEM5** **Injection budget.** Only the first ~200 lines (or an equivalent
  token cap) of `MEMORY.md` are composed into context, mirroring the proven
  Claude subagent-memory limit. Keeping memory within this budget is a goal of
  dreaming (§4). Memory that isn't distilled is just logs.

## 4. Dreaming — nightly consolidation (built-in, Alternative A)

- **MEM6** Dreaming is a **Podiom built-in maintenance routine** (not a
  user-authored schedule file). It runs on a daily cadence.
- **MEM7 — No sessions, no dream.** A dream runs for an agent only if there are
  **un-dreamed sessions** for it in Podiom's session store (SQLite, main D6)
  since the last dream. If none exist, the agent does not dream and `MEMORY.md`
  is untouched.
- **MEM8 — Robust to downtime.** A dream processes **all un-dreamed sessions
  since the last successful dream**, not strictly "today's". If `podiomd` was
  down at the nominal dream time, the next run catches up the missed sessions.
  (Consistent with the daemon-uptime limitation, main §7.6; memory must not
  silently lose a day's learning to a missed cycle.)
- **MEM9 — Source material.** The dream reads the day's session transcripts from
  the session store (the same durable history Podiom keeps for replay, main §8.5)
  as its raw material. This is a second payoff on already-stored data.
- **MEM10 — What the dream does.** For the agent, over its un-dreamed sessions,
  the dream:
  1. reads the current `MEMORY.md` as the authoritative starting point (MEM12),
  2. reflects over the new sessions to identify durably significant learnings
     (user preferences, working patterns, recurring facts, how the user and agent
     collaborate),
  3. integrates them into `MEMORY.md` — adding, merging, reorganising, and
     pruning stale entries to stay within the budget (MEM5),
  4. marks those sessions as dreamed.
- **MEM11 — Execution model.** The dream runs against **the agent's own model /
  profile**, at **low-to-medium effort** (distillation doesn't need peak
  reasoning). This mirrors the naming-model decision (main D3) and its trade-off:
  it consumes the agent's own rate limit / account. A dedicated cheap
  "dream model" is a possible future option.

### User edits vs. the dream
- **MEM12 — User edits are authoritative.** The user may edit `MEMORY.md` at any
  time (MEM15). The dream treats the user's current `MEMORY.md` as its starting
  point and MUST NOT overwrite user edits: content the user removed is not
  re-added on the next dream, and content the user changed is respected. The
  dream augments and curates; it does not reset.

## 5. Surfaces (UI + CLI)

### 5.1 What the UI must expose (design done separately in Claude Design)
- **MEM13** The UI must let the user, per agent: **view** the current `MEMORY.md`,
  **edit** it, **clear** it, and see **when the last dream ran** (and, ideally,
  whether a dream is pending because un-dreamed sessions exist).
- **MEM14** This spec defines only *what* must be exposed and editable; visual
  design (a "Memory" view/tab on the agent page) is handled in Claude Design.

### 5.2 CLI
- **MEM15** `podiom memory show <agent>` — print the agent's `MEMORY.md`.
- **MEM16** `podiom memory edit <agent>` — open `MEMORY.md` in `$EDITOR`.
- **MEM17** `podiom memory clear <agent>` — empty the agent's memory (with
  confirmation).
- **MEM18** `podiom memory dream <agent>` — trigger a dream on demand (respecting
  MEM7: no-op if no un-dreamed sessions). Useful for testing and for users who
  want to consolidate immediately.
- **MEM19** `podiom memory status [<agent>]` — show last-dream time and whether a
  dream is pending (un-dreamed sessions present).

## 6. What is remembered — unfiltered in topic (chosen posture)

- **MEM20** The agent may remember **anything** relevant that arises in
  interaction — working patterns, preferences, and also personal context the user
  shares. There are **no subject bans** in v1. This is the chosen posture: a
  richer, more genuinely "living" agent.
- **MEM21 — Curated in volume (not in topic).** "Unfiltered" governs *which kinds*
  of things may be remembered, not *how much*. The dream still selects only
  durably significant items and keeps `MEMORY.md` within the injection budget
  (MEM5). Unfiltered in category; curated in amount.

### 6.1 Security note (honest flagging, not a restriction)
- **MEM22** `MEMORY.md` is **plaintext on disk**, and unfiltered memory can
  therefore persist sensitive personal content (health, family, third parties,
  finances) that the user mentioned in a session. Persistence changes the
  sensitivity: a fleeting mention becomes a durable file. Implementers and the UI
  should treat memory files as sensitive (redact from any user-facing logs;
  never transmit off-machine).
- **MEM23** Because agent directories are shared across agents (main D11), one
  agent's `MEMORY.md` is readable by the user's other agents. This is consistent
  with the single-user, fully-trusted model but should be understood: unfiltered
  personal memory in one agent is reachable by all.
- **MEM24** The **mitigation is user editing** (MEM12/MEM15): the filter exists,
  but it is manual and after-the-fact rather than automatic and up-front. The
  user can always open `MEMORY.md` and remove anything. This posture — unfiltered
  capture, user-curated removal — is a deliberate choice for a single-user,
  fully-trusted, local deployment and should be revisited before any multi-user
  or shared deployment (out of scope, main §2).

## 7. Out of scope (v1) / future

- **Semantic / vector recall** (Mem0, Hindsight-style embedding search). Markdown
  distillation with an injection budget is sufficient for "the agent understands
  me more over time". Semantic retrieval is a future refinement if memory grows
  past what injection can carry — not a v1 need.
- **Native provider memory** (Claude subagent `memory:`, Codex `~/.codex/
  memories/`) — deliberately not used (Principle 2): per-CLI, machine-local,
  region-restricted, and not provider-neutral.
- **Cross-agent shared memory** (a common bank several agents draw from). v1
  memory is strictly per-agent.
- **A dedicated cheap "dream model"** separate from the agent's own model (MEM11)
  — future option, mirroring the naming-model question (main D3).
- **Automatic topic filtering / redaction** of sensitive content at capture time
  (v1 is unfiltered-with-manual-removal, §6).
- **Boot-persistent dreaming** (dreams only fire while `podiomd` runs; catch-up
  via MEM8 mitigates but does not remove this).

## 8. Acceptance checks

A correct implementation satisfies all of:

1. Creating an agent produces an empty `MEMORY.md` beside `SOUL.md` (MEM1/MEM2).
2. `MEMORY.md` content appears in the agent's standing context on **both** a
   Claude turn and a Codex turn, as the fourth composition layer (MEM3/MEM4).
3. Only up to the injection budget is composed in; a large `MEMORY.md` does not
   blow the context (MEM5).
4. After a day with ≥1 session, a dream updates `MEMORY.md` with distilled
   learnings and marks those sessions dreamed (MEM7/MEM10).
5. On a day with **no** sessions, no dream runs and `MEMORY.md` is unchanged
   (MEM7).
6. If `podiomd` was down at dream time, the next dream processes the missed,
   un-dreamed sessions (MEM8).
7. A user edit to `MEMORY.md` (removing an item) survives the next dream — the
   item is not re-added (MEM12).
8. `podiom memory show/edit/clear/dream/status` behave per §5.2; `dream` is a
   no-op when no un-dreamed sessions exist (MEM18).
9. The dream runs against the agent's own model/profile at reduced effort
   (MEM11).
10. Memory files are treated as sensitive: never logged in the clear, never sent
    off-machine (MEM22).
