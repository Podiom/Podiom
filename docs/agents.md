# Agents

Agents are durable, named colleagues maintained by Podiom. Each agent has stored
defaults for provider, profile, model, effort, permission mode, fallback chain,
and optional additive MCP config.

Creating an agent scaffolds:

```text
$PODIOM_HOME/agents/<name>/
  SOUL.md
  workspace/
```

`SOUL.md` is always created from a small identity skeleton and is user-owned.
See [SOUL.md generation](soul-generation.md) for the generated shape and quality
bar.
`workspace/` is the cwd used by backing provider processes in later phases.
Podiom does not create `agents/<name>/AGENTS.md`; that file is optional and left
for the user to add when an agent needs extra standing instructions.

Deleting an agent through the UI or CLI requires exact-name confirmation. Podiom
first archives the agent's sessions as JSON files under
`$PODIOM_HOME/agents/<name>/workspace/session-archive/`, removes those sessions
from active history, then removes the durable agent row and any matching
`config.yaml` entry. The `$PODIOM_HOME/agents/<name>/` directory is preserved.

## Instruction Layers

Podiom composes agent instructions in this fixed order:

1. `$PODIOM_HOME/AGENTS.md`
2. `$PODIOM_HOME/agents/<name>/AGENTS.md` when present
3. `$PODIOM_HOME/agents/<name>/SOUL.md`
4. the project ledger entry's `instructions` field when the session is bound to
   a project and that field is non-empty
5. `$PODIOM_HOME/agents/<name>/MEMORY.md` when non-empty, capped to the
   current memory injection budget

Layer 1 is Podiom-generated, not user-owned: `podiomd` rewrites
`$PODIOM_HOME/AGENTS.md` from the copy embedded in the binary whenever the two
differ, so an upgrade's instruction changes reach existing installs rather than
only fresh ones. Edits to it are lost on the next start. Layers 2–5 are yours;
per-agent standing instructions belong in layer 2.

The delivery artifact depends on the provider:

| Provider | Workspace artifact | Contents |
| --- | --- | --- |
| Claude | `workspace/CLAUDE.md` | A generated file with `@` imports for each instruction source. Memory is imported through a generated capped snapshot. |
| Codex | `workspace/AGENTS.md` | A generated bundle concatenating the instruction sources. Project sessions also pass that bundle explicitly as developer instructions. |

Claude wiring landed in Phase 2 and Codex wiring landed in Phase 5. The
workspace artifacts are generated and disposable; users edit only the canonical
base, per-agent, project, `SOUL.md`, and `MEMORY.md` sources.

## Native Provider Agent Hints

Podiom also projects its agents into provider-native agent/subagent features
when the backing CLI supports them. These projections are best-effort hints so a
provider can recognize which Podiom agent is calling and, where supported, offer
the same Podiom agents as native delegation targets.

Podiom remains authoritative. Native provider agents do not replace Podiom's
stored agent definition, workspace, memory, tools, permissions, profiles, or
instruction composition. If Claude or Codex rejects the generated native-agent
configuration, Podiom logs the failure and continues the same run without native
agent features.

Generated native artifacts are disposable and should not be edited:

- Claude receives in-process native agent definitions for the current turn; no
  user Claude settings, profiles, or subagent files are modified.
- Codex receives a generated profile overlay that points to disposable custom
  agent files under Podiom agent workspaces; user `~/.codex/config.toml` is not
  modified.

The Podiom-owned base `AGENTS.md` also tells agents to pause before risky,
broad, destructive, security-sensitive, or architecturally comprehensive code
implementation. In those cases, agents write a Markdown plan under the active
project's `$PODIOM_HOME/projects/<project>/plans/` directory (defaulting to
`~/.podiom/projects/<project>/plans/`) and ask the user to approve it before
making code changes.

It also governs the voice of anything an agent publishes under the user's
identity. Podiom never manufactures git credentials (see [Git](git.md)), so an
issue, pull request, review, discussion reply, or commit goes out from the user's
own account and reads as their own words. Agents are told to write it in the
user's first person, never to mention an agent or Podiom in it, and never to tag
the user or leave a decision to them inside a thread posted under their name — a
decision goes back through the session, `podiom_ask_user`, or
`podiom_request_user_action` instead.
