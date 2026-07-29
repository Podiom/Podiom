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
when set, and effort when set. A turn with photos appends one ordered
`localImage` item per normalized image after the text item:

```json
{
  "threadId": "thread-1",
  "input": [
    {"type":"text","text":"Compare these","text_elements":[]},
    {"type":"localImage","path":"…/attachments/<session>/<first>/visual.jpg"},
    {"type":"localImage","path":"…/attachments/<session>/<second>/visual.jpg"}
  ]
}
```

The attachment directory is also present in `runtimeWorkspaceRoots`, including
after a fallback or fresh replay. Photo-only turns receive a provider-only text
instruction; it is not persisted as user content. Historical attachment names
and readable paths appear in fresh history replay, but historical photos are not
automatically resent as current `localImage` items. Text-only input is unchanged.

The returned `thread.id` is stored as `sessions.provider_handle`. If the
app-server restarts, Podiom clears its in-memory loaded-thread set, calls
`thread/resume`, and then retries `turn/start` for the persisted `threadId`.

## Instructions

Before a session starts, core composes the agent instructions and writes
`agents/<name>/workspace/AGENTS.md`. The file is Podiom-managed and concatenates
the agent instruction layers in this order:

1. `$PODIOM_HOME/AGENTS.md`
2. `$PODIOM_HOME/agents/<name>/AGENTS.md` when present
3. `$PODIOM_HOME/agents/<name>/SOUL.md`
4. `$PODIOM_HOME/agents/<name>/MEMORY.md` when non-empty, capped to the memory
   injection budget

For sessions bound to a Podiom project, core keeps the project as the Codex
`cwd`. To keep Podiom's base/agent/SOUL/project/memory layers available in that
project cwd, Podiom also passes the generated bundle as `developerInstructions`
on `thread/start` and `thread/resume`. The project layer comes from the
project's `instructions` field in `projects.yaml`.

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
| `item/started` / `item/completed` with `collabAgentToolCall` + `spawnAgent` | Emit best-effort native-agent activity metadata for the spawned Codex subagent. Prompt text is not surfaced. |
| `item/started` / `item/completed` with `subAgentActivity` | Use the Codex agent path/thread id to enrich the activity chip and map it back to a Podiom agent when possible. |
| `thread/started` for a child thread | Use `agentRole`, `agentNickname`, or source agent path as supplemental metadata for the active parent turn. |
| `turn/completed` | Use the final `agentMessage` item as durable assistant text and finish the turn. |
| `turn/completed` items containing native-agent activity | Backfill native-agent activity if live item events were not seen. |
| `error` | Surface a Codex error message and finish the turn. |

When Codex delegates through its native multi-agent tools, Podiom shows the same
non-intrusive chat chip used for Claude, for example `Codex delegated to
Researcher`. The Podiom agent remains authoritative; Codex-native activity is
only a provider hint and UI affordance.

## Model image capability

Codex `model/list` entries may advertise `inputModalities`; Podiom preserves
that list as `input_modalities` in its provider capability API. Bundled fallback
Codex models declare `text` and `image`, matching the native `localImage`
delivery used for web-chat photos.

## Permissions

Podiom keeps Codex sandbox and approval settings separate.

| Podiom mode | Codex settings |
| --- | --- |
| `approve` | `approvalPolicy: "on-request"` and `sandbox: "read-only"` on thread start; per-turn `sandboxPolicy.type: "readOnly"`. |
| `auto` | `approvalPolicy: "on-request"` and `sandbox: "workspace-write"`; per-turn `sandboxPolicy.type: "workspaceWrite"` scoped to the working directory alone. |
| `yolo` | `approvalPolicy: "never"` and `sandbox: "danger-full-access"` on thread start; per-turn `sandboxPolicy.type: "dangerFullAccess"`. |

Podiom intentionally does not use `workspace-write` for `approve`, because
Claude prompts before workspace writes. Keeping Codex in `read-only` makes the
user-facing `approve` promise aligned across providers: reads may proceed,
writes ask. `workspace-write` is what `auto` is for — that mode's promise is
exactly "edits proceed, the rest asks".

**Writable scope comes from `runtimeWorkspaceRoots`, not
`sandboxPolicy.writableRoots`.** Measured against app-server 0.142.4: with
`writableRoots` held fixed, a directory listed in the runtime roots was written
with no approval request, and the same write was refused once that directory was
dropped from the list. Podiom's broad root set includes the projects parent
directory so agents can read the shared ledger, so `auto` is sent the working
directory alone (`codexRuntimeRoots`) — the broad set would let one session
write into every project. Reads are unaffected. `thread/start.runtimeWorkspaceRoots`
is also gated on `experimentalApi` and is *rejected*, not ignored, without it.

When Codex sends `item/commandExecution/requestApproval`,
`item/fileChange/requestApproval`, or `item/permissions/requestApproval`, Podiom
relays the request through the daemon permission broker. User `allow` maps to
Codex `accept`; user `deny` maps to Codex `decline`. Permission expansion
requests grant the requested profile only when the user allows the prompt; a
denial returns an empty turn-scoped grant.

If no decision arrives before `global.permission_timeout`, the broker returns a
deny decision so the Codex request cannot hang indefinitely.

## Plan mode

Podiom drives Codex's own Plan collaboration mode. Every `turn/start` carries
the intended mode explicitly — plan turns send `plan`, all others send
`default` — because the setting is sticky on the thread: an implementation turn
that omitted it would keep planning after the user approved.

```jsonc
"collaborationMode": {
  "mode": "plan",
  "settings": {
    "model": "<the active model>",       // required; never hardcoded
    "reasoning_effort": "medium",         // from the preset unless the session sets one
    "developer_instructions": null        // null = use Codex's built-in plan contract
  }
}
```

Three things this depends on, all verified against codex-cli 0.142.4:

- **`capabilities.experimentalApi: true` at `initialize`.** Podiom already sends
  it. Without it, `collaborationMode/list` and `turn/start.collaborationMode`
  are both rejected with `-32600`.
- **`codex app-server generate-json-schema` hides `collaborationMode` unless run
  with `--experimental`.** Reading the reduced schema is how an earlier
  investigation wrongly concluded Codex plan mode was unreachable. Regenerate
  with `--experimental` when checking this against a new Codex release.
- **Presets and the model are discovered, not assumed.** `collaborationMode/list`
  supplies the modes; `settings.model` is required and the preset's own value is
  usually null, so Podiom falls back to the session model and then to the
  account default from `model/list` (whose catalogue is under `result.data`).
  Hardcoding a model fails the turn with an unrelated HTTP 400 when the account
  default is newer than the installed CLI.

`developer_instructions: null` selects Codex's built-in plan contract without
clearing the thread's own `developerInstructions` — verified with an identity
marker, so Podiom's composed agent instructions survive planning.

The plan arrives as a completed thread item of type `plan`, which is
authoritative; `item/plan/delta` is a streaming preview the schema warns may
differ from the final text. A plan turn may legitimately end with only an
`agentMessage` and no plan item. `turn/plan/updated` is the separate
`update_plan` progress checklist and is never the plan artifact.

Plan mode is behavioral orchestration, not a sandbox boundary — a plan turn
under `workspace-write` declined to write — but Podiom still pins
`sandboxPolicy: readOnly` while planning so non-mutation is enforced rather
than instructed.
