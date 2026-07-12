# Codex Integration

Podiom drives OpenAI Codex through the experimental app-server transport. The
daemon owns durable Podiom sessions and maps each Codex-backed session to a
persisted Codex `threadId`.

## Launch

Base command:

```text
codex app-server --listen stdio://
```

The transport is newline-delimited JSON-RPC over stdin/stdout. Podiom sends:

```json
{"id":1,"method":"initialize","params":{"clientInfo":{"name":"podiom","title":"Podiom","version":"dev"},"capabilities":{"experimentalApi":true,"requestAttestation":false,"mcpServerOpenaiFormElicitation":false}}}
{"method":"initialized"}
```

Podiom keeps the process alive and restarts it on transport failure. Because
`CODEX_HOME` is process-scoped, the adapter keeps one app-server per Codex
profile directory. When no profile is set, `CODEX_HOME` is unset and Codex uses
its normal global login.

## Lifecycle

| Podiom action | Codex method |
| --- | --- |
| Create session backing resource | `thread/start` |
| Rejoin persisted backing resource | `thread/resume` |
| Send user message | `turn/start` |

`thread/start` and `thread/resume` include the agent `workspace/` as `cwd`,
`runtimeWorkspaceRoots`, model when set, and the current permission posture.
`turn/start` includes the user text input, `cwd`, `runtimeWorkspaceRoots`, model
when set, and effort when set.

The returned `thread.id` is stored as `sessions.provider_handle`. If the
app-server restarts, Podiom clears its in-memory loaded-thread set, calls
`thread/resume`, and then retries `turn/start` for the persisted `threadId`.

## Instructions

Before a session starts, core composes the agent instructions and writes
`agents/<name>/workspace/AGENTS.md`. The file is Podiom-managed and concatenates
the instruction layers in this order:

1. `$PODIOM_HOME/AGENTS.md`
2. `$PODIOM_HOME/agents/<name>/AGENTS.md` when present
3. `$PODIOM_HOME/agents/<name>/SOUL.md`
4. `$PODIOM_HOME/agents/<name>/MEMORY.md` when non-empty, capped to the memory
   injection budget

Current Codex app-server behavior was checked against `codex-cli 0.142.4` by
starting a thread with both a parent `agents/<name>/AGENTS.md` and a workspace
`AGENTS.md`. The `thread/start` response reported only
`workspace/AGENTS.md` in `instructionSources`, so the parent per-agent file is
not double-loaded in that version. Podiom also has a runtime guard: if a future
Codex response reports both the generated workspace file and the parent
per-agent file, session startup fails instead of delivering duplicated
instructions.

## Native Agent Projection

Podiom may also expose Podiom agents to Codex as custom-agent entries in the
generated profile overlay. The overlay points to disposable custom-agent TOML
files under Podiom agent workspaces and is passed through the same app-server
configuration override path as generated MCP settings.

This projection is best-effort and complementary. The root Codex thread still
uses Podiom's generated workspace `AGENTS.md` for authoritative instructions;
native custom agents only help Codex recognize Podiom agents and use them as
native delegation targets when the installed Codex version supports that.

Podiom does not edit user `~/.codex/config.toml` or user-owned Codex agent
files. If Codex rejects the generated native-agent overlay, Podiom logs the
failure and retries with the same MCP/profile overlay minus native-agent
entries.

## Streaming

Podiom correlates server notifications by `threadId` and `turnId`.

| Codex event | Podiom behavior |
| --- | --- |
| `item/agentMessage/delta` | Stream as assistant delta. |
| `turn/completed` | Use the final `agentMessage` item as durable assistant text and finish the turn. |
| `error` | Surface a Codex error message and finish the turn. |

## Permissions

Podiom keeps Codex sandbox and approval settings separate.

| Podiom mode | Codex settings |
| --- | --- |
| `approve` | `approvalPolicy: "on-request"` and `sandbox: "read-only"` on thread start; per-turn `sandboxPolicy.type: "readOnly"`. |
| `yolo` | `approvalPolicy: "never"` and `sandbox: "danger-full-access"` on thread start; per-turn `sandboxPolicy.type: "dangerFullAccess"`. |

Podiom intentionally does not use Codex's lower-friction `workspace-write` Auto
preset for `approve`, because Claude prompts before workspace writes. Keeping
Codex in `read-only` makes the user-facing `approve` promise aligned across
providers: reads may proceed, writes ask.

When Codex sends `item/commandExecution/requestApproval`,
`item/fileChange/requestApproval`, or `item/permissions/requestApproval`, Podiom
relays the request through the daemon permission broker. User `allow` maps to
Codex `accept`; user `deny` maps to Codex `decline`. Permission expansion
requests grant the requested profile only when the user allows the prompt; a
denial returns an empty turn-scoped grant.

If no decision arrives before `global.permission_timeout`, the broker returns a
deny decision so the Codex request cannot hang indefinitely.
