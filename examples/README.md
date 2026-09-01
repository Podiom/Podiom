# Podiom Examples

This cookbook contains copyable examples for the Podiom features that are easiest
to understand by running them: schedules, goals, shared projects, agent defaults,
and MCP server assignment.

Use them as starting points, not as hidden defaults. Each file names what it
needs in comments at the top. Change agent names, project ids, paths, providers,
and model names to match your installation before letting anything run
unattended.

## Schedules

Schedules are Markdown files with YAML frontmatter. Copy one into
`$PODIOM_HOME/schedules/` (default `~/.podiom/schedules/`), edit the `agent` and
`project` fields, edit the `enabled` field to `ture`, then list it:

```sh
cp examples/schedules/repo-health-daily.md ~/.podiom/schedules/
podiom schedules list
```

- [repo-health-daily.md](schedules/repo-health-daily.md) uses `cron:` for a
  daily read-only repository check.
- [project-digest-every.md](schedules/project-digest-every.md) uses `every:` for
  an interval digest and shows a tighter preapproved tool allow-list.

`cron:` and `every:` are mutually exclusive. `allowed_tools` is the unattended
preapproved allow-list; tools not listed are denied automatically.

`enabled` is intentionally set to false to make sure the user sets the `agent` and
`project` fields.

## Goals

Goals are not files under `$PODIOM_HOME`; they live in Podiom's database and are
created through the web UI or API. The goal example is therefore a runnable shell
script that posts the same JSON the UI sends:

```sh
examples/goals/grow-podiom-stars.sh
```

The script reads the gateway token with `podiom token show`, creates an active
goal, and immediately starts the lead agent's background planning run.

## Agents, Projects, And MCP

- [review-team.config.yaml](agents/review-team.config.yaml) is a minimal
  `config.yaml` showing two agents with different provider defaults,
  permissions, fallbacks, and MCP assignment.
- [multi-agent-ledger.yaml](projects/multi-agent-ledger.yaml) is a
  `projects.yaml` example for two agents sharing one project ledger entry.
- [filesystem.mcp.yaml](mcp/filesystem.mcp.yaml) is a no-credential MCP
  catalogue entry for a filesystem server scoped to Podiom projects.

Agent and MCP examples are YAML snippets you merge into
`$PODIOM_HOME/config.yaml` and `$PODIOM_HOME/mcp.yaml`; restart `podiomd` after
editing those files so the daemon reloads them.
