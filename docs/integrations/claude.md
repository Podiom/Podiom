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
| `auto` permission mode | `--permission-mode acceptEdits` (relay still wired for non-edit tools) |
| `yolo` permission mode | `--permission-mode bypassPermissions` |
| Plan mode | `--permission-mode plan` |

When a profile is set, Podiom exports `CLAUDE_CONFIG_DIR=<profile.config_dir>`.
When no profile is set, Podiom leaves `CLAUDE_CONFIG_DIR` unset so Claude uses
its normal global login. The variable name comes from the registry
(`ProviderInfo.ProfileEnvVar`), and it is always stripped from the inherited
environment first, so one profile's directory can never leak into another's
process.

## Sign-in

`internal/providerlogin` drives `claude auth login` over plain pipes — no pty
required. The CLI reads the authorization code from stdin with `readline` and
narrates on stdout:

```text
Opening browser to sign in…
If the browser didn't open, visit: <url>
Paste code here if prompted >
```

That URL is the **manual-redirect** variant
(`redirect_uri=https://platform.claude.com/oauth/code/callback`), so it works
from any browser on any device and ends on a page showing a `code#state` string.
Claude also mints a `http://localhost:<port>/callback` variant, but only hands
it to the browser it opens locally — useless when the daemon is on another
machine, which is why the printed one is the one Podiom surfaces.

Podiom scrapes the URL, the browser opens it in a popup, and the pasted code
goes straight to the CLI's stdin. A rejected code leaves the process running
(`Invalid code. …` on stderr) so the user can retry on the same session. The
terminal verdict is the exit status, not a success string — that stays stable
across CLI versions. Podiom never sees the token: the CLI performs the exchange
and writes `.credentials.json` (or the macOS Keychain entry) itself.

Login state is probed with `claude auth status`, which prints
`{"loggedIn": …}` and is authoritative even on a non-zero exit.

### Detecting a signed-out turn

Claude Code does **not** report a signed-out account as a stream error. It
emits a synthetic *assistant message*, which is why the raw text used to reach
the transcript as if the agent had said it:

```json
{"type":"assistant",
 "message":{"model":"<synthetic>","content":[{"type":"text","text":"Not logged in · Please run /login"}]},
 "error":"authentication_failed","is_api_error_message":true}
```

The terminal `result` line repeats it with `"is_error": true`. The adapter keys
off those explicit markers (`error: "authentication_failed"`, `<synthetic>`,
`is_api_error_message`, `is_error`) and only then consults the wording, so a
model that merely talks about logging in is never misread. Both lines produce
`adapter.EventAuthRequired`; core keeps the first and drops the duplicate, and
the process-exit path stays silent so the generic "claude exited with error"
bubble doesn't bury the sign-in card.

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

## Photo inputs

For a turn with [photo attachments](../photo-attachments.md), Podiom appends an
explicitly delimited, ordered list containing each display name and absolute path
to its normalized `visual.jpg`. The session attachment directory is passed as an
additional allowed/readable root, so Claude can use its image-path workflow
without receiving the retained original.

If the durable user content is empty, the adapter supplies a provider-only
instruction to inspect the attached photos. That fallback text is not stored in
canonical history.

When profile switching, compaction, or provider fallback creates a fresh Claude
backing session, replay retains historical photo names and normalized paths and
the attachment directory remains an additional read root. Historical photos are
not automatically promoted to new current-turn attachments. Text-only turns use
the unchanged stream-json shape.

Claude model catalogue responses may advertise
`capabilities.image_input.supported`; Podiom maps that flag to `image` in the
public model `input_modalities` list. Bundled fallback Claude models declare
both `text` and `image`.

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

## Plan mode

Podiom drives Claude's own plan mode rather than running its own gate: while a
session is in plan mode the turn adds `--permission-mode plan` and nothing else.
Claude enforces read-only in its executor and runs its own phased workflow
(including Explore subagents), which is why Podiom does not inject a plan prompt
of its own.

**Podiom observes where the plan lands; it does not configure it.** Writing
`plansDirectory` into the user's Claude settings would be intrusive and is
unnecessary — the default is `<CLAUDE_CONFIG_DIR>/plans`, and Podiom already
sets that variable per profile. Resolution order:

1. a `plansDirectory` the *user* has set (`<config dir>/settings.json`, or
   project-local `.claude/settings.json`), read-only, resolved relative to the
   project root as Claude does;
2. otherwise `<CLAUDE_CONFIG_DIR>/plans`, falling back to `~/.claude/plans` when
   Podiom leaves the variable unset.

Podiom snapshots that directory's `.md` files before the turn and takes the
new-or-modified one after. Matching is by **modification time, not filename**:
Claude derives the plan's slug from the session, so a revision overwrites the
same file and a name-only check would capture the first plan and miss every
revision. On capture Podiom writes its own copy under the project's `plans/`
directory, which becomes the canonical artifact — the provider's own file is
never deleted, even on reject.

### Why not ExitPlanMode

`ExitPlanMode` is **not available in headless `-p` runs**. Invoked anyway, the
CLI answers: *"No such tool available: ExitPlanMode. ExitPlanMode exists but is
not enabled in this context."* `--allowedTools ExitPlanMode` does not help — it
is a permission allow-list, not a registration mechanism — and no other flag
exposes it. Confirmed in the 2.1.220 binary: the tool is present in
`getAllBaseTools()` but excluded from the pool `assembleToolPool` builds for
this context. File detection is therefore not a workaround for a missing
feature; it is the only mechanism available to a CLI-driven integration.

The Node Agent SDK likely does expose the tool. That would only become relevant
if Podiom's Claude adapter ever stopped spawning the CLI binary.
