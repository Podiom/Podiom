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

The delivery artifact depends on the provider:

| Provider | Workspace artifact | Contents |
| --- | --- | --- |
| Claude | `workspace/CLAUDE.md` | A generated file with `@` imports for each instruction source. |
| Codex | `workspace/AGENTS.md` | A generated bundle concatenating the instruction sources in order. |

Claude wiring landed in Phase 2 and Codex wiring landed in Phase 5. The
workspace artifacts are generated and disposable; users edit only the canonical
base, per-agent, and `SOUL.md` sources.

The Podiom-owned base `AGENTS.md` also tells agents to pause before risky,
broad, destructive, security-sensitive, or architecturally comprehensive code
implementation. In those cases, agents write a Markdown plan under the active
project's `$PODIOM_HOME/projects/<project>/plans/` directory (defaulting to
`~/.podiom/projects/<project>/plans/`) and ask the user to approve it before
making code changes.
