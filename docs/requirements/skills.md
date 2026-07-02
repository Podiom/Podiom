# Podiom Skills — Requirements

*Standalone implementation spec for the Skills feature of Podiom. Self-contained:
a developer can implement from this document without reading the full Podiom
requirements. Cross-references to the main doc (e.g. §5.2, §8.7) are for context
only and are not required to build this feature.*

Status: v1.0 — ready for implementation.

---

## 1. Purpose & philosophy

Skills are reusable `SKILL.md` capability folders (the open cross-agent skill
format used by Claude Code and Codex CLI). This feature lets a user **see every
skill available on their machine in one place** and ensures that **a Podiom agent
sees the same skill set regardless of which provider (Claude or Codex) backs a
given turn**.

Guiding principles:

1. **Podiom owns no skills.** Podiom never authors, installs, or stores skills of
   its own. It discovers what already exists and exposes a unified view.
2. **Standalone CLIs keep working untouched.** Running `claude` or `codex`
   directly (outside Podiom) must still see exactly its own skills — Podiom must
   not permanently fuse the two providers' skill sets on disk.
3. **Unification happens through Podiom, not on the filesystem globally.** The
   cross-provider "same skills everywhere" guarantee applies to turns Podiom
   launches, not to standalone CLI use. (This is "Stance 2".)
4. **Observational, not controlling (v1).** No per-agent skill mapping, no
   enable/disable. Every agent sees every skill. The UI is a catalogue to browse,
   not a control panel.

---

## 2. Directory model

### 2.1 The three locations

| Path | Role | Owner |
| --- | --- | --- |
| `~/.agents/skills/` | **Canonical unified source** — the single view Podiom reads/displays | Podiom-managed union |
| `~/.claude/skills/` | Claude's native personal skills | Claude CLI (real dir) |
| `~/.codex/skills/` | Codex's native personal skills | Codex CLI (real dir) |

- **S1** `~/.agents/skills/` is the **single source of truth** for "what skills
  exist". Podiom reads and displays only this directory.
- **S2** `~/.claude/skills/` and `~/.codex/skills/` **remain real, independent
  directories**. Podiom MUST NOT replace them with directory-level symlinks.
  Standalone `claude`/`codex` continue to see only their own skills.
- **S3** `~/.agents/skills/` is a **union view built from per-skill symlinks** —
  one symlink per skill, each pointing to wherever that skill actually lives:
  - a real folder under `~/.claude/skills/<name>/`, or
  - a real folder under `~/.codex/skills/<name>/`, or
  - a genuinely shared real folder living directly in `~/.agents/skills/<name>/`.
- **S4** Linking is **per-skill, never per-directory**, so no two directories are
  mutual mirrors (which would be a cycle) and each provider dir stays clean.

### 2.2 What lives where (illustration)

```
~/.claude/skills/
  skill-a/              # real, Claude's own
    SKILL.md
~/.codex/skills/
  skill-b/              # real, Codex's own
    SKILL.md
~/.agents/skills/
  skill-a -> ~/.claude/skills/skill-a    # union symlink
  skill-b -> ~/.codex/skills/skill-b     # union symlink
  skill-c/                               # real, genuinely shared
    SKILL.md
```

---

## 3. Cross-provider exposure (Stance 2)

The union is made available to each backend **only for turns Podiom launches**.
Standalone CLI use is unaffected.

- **S5 — Codex (native).** Podiom points Codex's skill scanning at
  `~/.agents/skills/` via `CODEX_HOME` / the Codex scan root, so a Podiom-launched
  Codex process sees the union natively. (Codex natively scans its skills roots;
  pointing it at the union folder is sufficient.)
- **S6 — Claude (`--add-dir`, verified).** Claude does **not** auto-load
  `~/.agents/skills/`. Podiom therefore:
  1. Places a `.claude/skills/` directory **inside the agent's `workspace/`**,
     populated with per-skill symlinks to the union (or a single link to the union
     root — see S7), and
  2. Launches Claude with **`--add-dir <workspace>`** so Claude discovers those
     skills via its project / added-directory scope.

  This was **test-verified**: a `SKILL.md` placed under an added directory's
  `.claude/skills/` is discovered and invoked. Confirm the exact flag name against
  the installed Claude version during implementation (`claude --help | grep -i
  dir`); fall back to a native `~/.claude/skills/` link only if `--add-dir` proves
  unreliable on a given version.
- **S7 — Independence from auth.** The Claude exposure mechanism (S6) MUST NOT use
  or depend on `CLAUDE_CONFIG_DIR`. Auth/profiles and skill exposure are
  **independent levers**: skill exposure works identically whether or not the agent
  has a profile. (In Podiom, most agents have no profile; tying skills to
  `CLAUDE_CONFIG_DIR` would break the common case and is explicitly disallowed.)
- **S8 — Standalone guarantee.** Because S5/S6 only affect Podiom-launched
  processes (a scoped `CODEX_HOME`/scan root for Codex; a workspace `--add-dir` for
  Claude), a standalone `claude` or `codex` invocation outside Podiom sees only its
  own native skills. A `.claude`-origin skill is available to a Codex **Podiom**
  turn, but **not** to a standalone `codex` run. This is intended.

---

## 4. Symlink lifecycle

Two distinct symlink operations exist; implementers must keep them separate.

- **S9 — Union links (skill lifecycle, NOT agent-bound).** The per-skill symlinks
  that populate `~/.agents/skills/` are created/refreshed by:
  - the **install script** (§6), and
  - any Podiom **re-scan** of skills (`podiom skills scan`/`relink`, §5).

  They have nothing to do with agents and exist once per machine.
- **S10 — Workspace skill link (agent-bound).** The `.claude/skills/` link inside
  an agent's `workspace/` (S6) is created **when the agent is created**, as part
  of workspace scaffolding. It is per-agent and lives for the life of the agent.
- **S11 — Deduplication.** When building the union, Podiom dedupes on **skill
  name** (the directory name / frontmatter `name`). It records which source
  location(s) a given skill name appears in and flags when same-named skills have
  **differing content**. No automatic merging — the union surfaces one entry per
  name and the UI/CLI exposes the conflict honestly (§5).
- **S12 — Re-scan triggers.** Podiom rebuilds/refreshes union links on demand
  (CLI/dashboard action) and SHOULD refresh on daemon start. (Live filesystem
  watching is optional, not required for v1.)

---

## 5. Surfaces (CLI + dashboard)

The feature is **observational**: it shows what exists and where it lives. No
per-agent assignment, no enable/disable in v1.

### 5.1 CLI

- **S13** `podiom skills list` — deduplicated list, one row per skill name, each
  with its one-line `description` and **source badge(s)** (`agents`, `claude`,
  `codex`). Supports `--source claude|codex|agents` to filter.
- **S14** `podiom skills show <name>` — prints the skill's `SKILL.md` and its
  source path(s); flags a conflict if same-named skills differ across sources.
- **S15** `podiom skills paths` — prints the canonical dir and the resolved
  symlink topology (debugging aid for the union).
- **S16** `podiom skills scan` / `podiom skills relink` — rebuild the union links
  (S9), e.g. after the user has added a skill to `~/.agents/skills/`.
- **S17** No `enable` / `disable` / `assign` verbs in v1 (consistent with the
  no-mapping model).

### 5.2 Dashboard — single "Skills" page

Layout and behaviour (this is the brief for Claude Design):

- **S18** A **clean list/grid**, one row/card per **deduplicated skill name**.
  Each item shows:
  - skill **name**,
  - the one-line **`description`** from its frontmatter,
  - a small **source badge** — `agents` (shared), `claude`, or `codex` —, with
    **multiple badges** if the skill appears in more than one source.
- **S19** A **conflict indicator** when same-named skills differ across sources
  (S11), expandable to reveal the differing source paths.
- **S20** **Search/filter** by name/description, plus a filter by source badge.
- **S21** A persistent, quiet **helper line**: *"Skills live in
  `~/.agents/skills/`. Add a SKILL.md folder there to make it available to all
  agents."* — teaches the add path (S22) without implying Podiom installs skills.
- **S22** **Read-only in v1.** No toggles and no per-agent assignment UI. A row
  may **expand to show the full `SKILL.md`** (read-only) for inspection.
- **S23** Optional but recommended: a small **"available to all agents"** note,
  reinforcing the v1 model so users don't hunt for a per-agent control that does
  not exist.

### 5.3 Design intent (for Claude Design)

The page should feel like a **catalogue you browse**, not a **control panel you
operate**. Calm, scannable, source-transparent. The single most important visual
job is making *"where does this skill live — shared, Claude-only, or
Codex-only?"* instantly legible via the source badges, and making the add-path
(`~/.agents/skills/`) discoverable **without** a primary action button that would
overpromise installation. Empty state should gently point the user to add a
`SKILL.md` folder to `~/.agents/skills/`.

---

## 6. Install-script responsibilities

The installer provisions the skill topology so the feature works regardless of
install order.

- **S24 — Provision even if the CLIs are absent.** The script creates the needed
  directories even if Claude or Codex are not installed yet, so a later CLI install
  inherits a correct setup:
  - ensure `~/.agents/skills/` exists (canonical source),
  - ensure `~/.claude/` and `~/.codex/` exist,
  - build the union links in `~/.agents/skills/` from whatever sources are present.
- **S25 — Do not clobber existing real skill dirs.** If `~/.claude/skills/` or
  `~/.codex/skills/` already exist as **non-empty real directories**, the script
  MUST NOT overwrite them. It links their skills into the union (with name-dedup,
  S11) and **reports** what it did. (Migrating the real folder into the union and
  leaving a link behind is acceptable, but must be explicit and logged — never
  silent data loss.)
- **S26 — Idempotent.** Re-running the installer is safe: existing correct links
  are left alone; only missing/incorrect ones are repaired.
- **S27 — Windows symlinks.** Symlinks may require developer mode or elevation on
  Windows. The script falls back to a **junction** (`mklink /J`) where directory
  symlinks are unavailable, or **warns clearly** rather than failing silently.
  (Cross-platform support matters because Podiom targets Windows/Linux/macOS.)

---

## 7. Out of scope (v1) / future

- **Per-agent skill mapping** (assigning different skill sets to different agents).
  The architecture leaves room for it, but v1 is "all agents see all skills".
- **Enable/disable** of individual skills from Podiom.
- **Skill installation/authoring** from within Podiom (users add `SKILL.md`
  folders to `~/.agents/skills/` manually in v1).
- **Project-scoped skills** (e.g. `.claude/skills/` inside a project). v1 concerns
  the global/personal level only.
- **Live filesystem watching** of skill directories (manual `scan`/`relink` is
  sufficient for v1).
- **Stance 1** (making the union visible to standalone CLIs too) — deliberately
  not chosen, to preserve clean standalone operation.
- **Codex cross-provider exposure (S5) — deferred.** Implementation found that the
  installed Codex (0.142.4) scans only `$CODEX_HOME/skills` with no config key for
  additional roots. Linking the union into the real `~/.codex/skills` would leak
  `.claude`-origin skills to standalone Codex (violating S8), and repointing
  `CODEX_HOME` wholesale would orphan Codex's auth/session/cache state. The clean
  fix is a Podiom-managed "overlay" `CODEX_HOME` (mirror every entry as a symlink
  except `skills`, which composes the union + `.system` built-ins). This is left as
  a follow-up. Acceptance checks #1/#2 are therefore met for the **Claude** side
  (a Codex/agents-origin skill is usable by a Podiom Claude turn) but not yet for
  the **Codex** side. Skill discovery, the catalogue (CLI + dashboard), and Claude
  exposure all ship in v1.

---

## 8. Acceptance checks

A correct implementation satisfies all of:

1. A skill folder placed in `~/.claude/skills/` appears in `podiom skills list`
   with a `claude` badge, and is usable by a Podiom **Codex** turn.
2. A skill folder placed in `~/.codex/skills/` appears with a `codex` badge, and
   is usable by a Podiom **Claude** turn.
3. A skill in `~/.agents/skills/` (real folder) appears with an `agents` badge and
   is usable by both providers through Podiom.
4. Running `codex` **standalone** (outside Podiom) does **not** see a
   `.claude`-origin skill (Stance 2 / S8).
5. Creating a new agent produces a `workspace/.claude/skills/` link, and a
   Podiom-launched Claude turn for that agent discovers the union via `--add-dir`.
6. Skill exposure works for an agent **with no profile** (no `CLAUDE_CONFIG_DIR`
   dependency — S7).
7. The installer run on a machine **without** Claude/Codex still creates
   `~/.agents/skills/`, `~/.claude/`, `~/.codex/`, and the union (S24).
8. The installer run against a **pre-existing non-empty** `~/.claude/skills/`
   preserves those skills and reports what it did (S25).
9. Two same-named skills with differing content surface a **conflict** indicator
   rather than silently merging (S11/S19).
