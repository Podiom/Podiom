# Sessions

A Podiom session is the durable conversation unit. It stores the bound agent,
current settings, immutable origin, provider resume handle, rolling summary area,
and the full ordered message history in SQLite.

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

New web sessions may be created with draft model, effort, permission mode, and
project settings. If a project is selected before the first message, the session
stores `project_id` and receives the same project context used by project-linked
roadmap sessions.

| Command | Effect |
| --- | --- |
| `/model <name>` | Set the model for subsequent turns. |
| `/effort <level>` | Set a provider-supported reasoning effort for subsequent turns. |
| `/profile <name|default>` | Switch auth profile; `default` clears the profile and uses the provider's normal login. The next turn replays history into a fresh backing session/thread. |
| `/permission approve|yolo` | Override permission mode for subsequent turns. |
| `/name <text>` | Set the session display name. |
| `/describe <text>` | Set the session description. |
| `/help` | Show available commands. |
