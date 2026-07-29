# Sessions

A Podiom session is the durable conversation unit. It stores the bound agent,
current settings, immutable origin, provider resume handle, rolling summary area,
and the full ordered message history in SQLite. User messages may also own
durable [photo attachments](photo-attachments.md).

## Origin

Every session has one origin set at creation:

| Origin | Meaning |
| --- | --- |
| `web` | Created by the web UI. |
| `cli` | Created by the CLI. |
| `schedule` | Created by a scheduled run. |
| `roadmap` | Created from a roadmap task. |

Origin is provenance only. A session can later be continued from another
channel, but its origin does not change.

## Schedule Linkage

Sessions have nullable `schedule_id` and `run_id` fields so scheduled sessions
can link back to the schedule/run that produced them. The scheduler itself is
implemented in a later phase.

## History

Message history is stored as strictly ordered `user` and `assistant` messages.
The provider's own session or thread is treated as a resumable backing resource;
Podiom's SQLite history is the canonical truth that survives daemon restarts.

When a profile switch or fallback changes the provider target, Podiom clears the
provider handle, starts a fresh backing session/thread on the next live turn, and
replays canonical history. If a rolling summary is available, replay sends the
summary plus the most recent turns verbatim instead of the full transcript.

Photo attachment metadata is part of canonical message history. Each attachment
row belongs to its session and user message, while its original and normalized
JPEG live below `$PODIOM_HOME/attachments/<session-id>/<attachment-id>/`. Fresh
provider replay preserves attachment names and readable normalized paths but
does not automatically resend every historical photo as a current visual input.
Rolling-summary prompts, automatic naming, and copied/exported transcripts retain
photo names so the visual context does not disappear silently during compaction.

Agent and completed-roadmap-task archives copy the associated attachment files.
Deleting a session cascades its attachment metadata and removes its live
attachment directory. Backing up `$PODIOM_HOME` includes both canonical SQLite
metadata and live photo files; restore both together.

When an **interactive** turn hits a provider session limit, Podiom does not fall
back silently: it blocks the turn and prompts the user (a `fallback_request` over
the WebSocket) to either advance their configured fallback chain or switch to a
specific provider/profile. The prompt makes clear that continuing recreates the
history on the new target via the replay described above. **Non-interactive** runs
(schedules, goals, dreams) have no user to prompt and keep falling back
automatically along the configured chain.

## Naming

After the first user/assistant exchange, Podiom starts a non-blocking naming job.
It asks the session's own provider/model at low effort for a concise name and
description, then stores them on the session. If the provider output cannot be
parsed, Podiom falls back to a short deterministic title from the first user
message.

Manual `/name <text>` and `/describe <text>` commands override auto-generated
metadata and mark it as user-authored.

## Slash Commands

Slash commands are session-scoped controls and are not appended to canonical chat
history.

Slash commands cannot carry photo attachments. Ordinary user turns may contain
text, one to four photos, or photos without text.

New web sessions may be created with draft model, effort, permission mode, and
project settings. If a project is selected before the first message, the session
stores `project_id` and receives the same project context used by project-linked
roadmap sessions.

| Command | Effect |
| --- | --- |
| `/model <name>` | Set the model for subsequent turns. |
| `/effort <level>` | Set a provider-supported reasoning effort for subsequent turns. |
| `/profile <name|default>` | Switch auth profile; `default` clears the profile and uses the provider's normal login. The next turn replays history into a fresh backing session/thread. |
| `/permission approve|auto|yolo` | Override permission mode for subsequent turns. |
| `/name <text>` | Set the session display name. |
| `/describe <text>` | Set the session description. |
| `/compact` | Summarize older history and free the context window. Forces a rolling summary, clears the provider handle, and replays the summary plus recent turns into a fresh backing session/thread on the next turn. |
| `/help` | Show available commands. |
