# Claude Integration

Podiom drives Claude Code as a per-turn process. The daemon owns the durable
Podiom session and launches `claude` for each turn with the agent workspace as
`cwd`.

## Launch

Base command:

```text
claude -p \
  --input-format stream-json \
  --output-format stream-json \
  --include-partial-messages \
  --verbose \
  --replay-user-messages
```

Optional flags:

| Setting | Claude flag |
| --- | --- |
| Session provider handle | `--resume <claude-session-id>` |
| Model | `--model <name>` |
| Effort | `--effort <level>` |
| Best-effort Podiom agent projection | `--agents <json>` and `--agent <generated-name>` |
| `yolo` permission mode | `--permission-mode bypassPermissions` |

When a profile is set, Podiom exports `CLAUDE_CONFIG_DIR=<profile.config_dir>`.
When no profile is set, Podiom leaves `CLAUDE_CONFIG_DIR` unset so Claude uses
its normal global login.

## Instructions

Before a turn, core composes the agent instructions and writes
`agents/<name>/workspace/CLAUDE.md`. The file is Podiom-managed and contains
absolute `@` imports in this order:

1. `$PODIOM_HOME/AGENTS.md`
2. `$PODIOM_HOME/agents/<name>/AGENTS.md` when present
3. `$PODIOM_HOME/agents/<name>/SOUL.md`
4. the project ledger entry's `instructions` field when the session is bound to
   a project and that field is non-empty, through a generated snapshot
5. `$PODIOM_HOME/agents/<name>/MEMORY.md` when non-empty, through a generated
   capped snapshot

Claude auto-discovers `CLAUDE.md` because the workspace is the process cwd.

## Native Agent Projection

On each turn, Podiom may also pass generated Claude native agent definitions
with `--agents` and select the active Podiom agent with `--agent`. This is a
provider hint only: the generated definitions are built from Podiom's canonical
agent layers, but Podiom still relies on the generated `CLAUDE.md` instruction
path for correctness.

Podiom does not edit Claude user settings, profile directories, or user-owned
subagent files. If Claude rejects the native-agent flags or generated
definitions, Podiom logs the native-agent failure and retries the same turn
without `--agent`/`--agents`.

## Streaming

Podiom writes stream-json user input on stdin and parses Claude stream-json on
stdout. The adapter handles:

| Claude event | Podiom behavior |
| --- | --- |
| `system.session_id` / `result.session_id` | Persist as provider handle. |
| nested `stream_event.content_block_delta.delta.text` | Stream as assistant delta. |
| `assistant.message.content` / `result.result` | Use as final assistant text for durable history. |

When resuming with `--resume`, Podiom sends only the new user turn. It does not
also replay canonical history into an already-resumed Claude session.

## Permissions

`approve` mode generates a temporary MCP config in `workspace/.podiom/` and adds:

```text
--mcp-config <generated-json>
--permission-prompt-tool mcp__podiom_permission__prompt
```

The generated MCP server command is:

```text
podiomd permission-mcp --addr <daemon-addr> --turn <turn-id> --timeout <duration>
```

The hidden MCP helper exposes one tool, `prompt`. For each Claude permission
request it POSTs to the daemon permission broker. The CLI receives the request
on the live chat stream, prompts the user, and POSTs the decision back.

Decision payloads:

```json
{"behavior":"allow","updatedInput":{...}}
{"behavior":"deny","message":"Denied by user"}
```

The MCP `tools/call` response is a single text block containing that JSON. This
shape was verified against Claude Code during Phase 2.

If no decision arrives before `global.permission_timeout`, the daemon returns
`{"behavior":"deny"}` so Claude does not block indefinitely.
