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

## Linkage

Sessions have nullable `schedule_id`, `run_id`, `task_id`, `goal_id`, and
`project_id` fields, so a session can say what produced it — the schedule and run
that fired it, the roadmap task it was started for, the goal it belongs to.

The reverse direction is recorded on the artifacts themselves. A roadmap task or
schedule an agent creates carries `created_by_session` and `created_by_agent`, so
a session's detail can list what it made and the Roadmap and Schedules pages can
link an item back to the conversation it came out of. Both halves are derived
from live data: deleting a task simply drops it from the list rather than leaving
a record of something that no longer exists. See
[agent-tools.md](agent-tools.md).

Agents read their own side of this with `podiom_session_context`, which reports
the session's origin, whether the run is unattended, its links in both
directions, and its context usage — without replaying the transcript.
They can use `podiom_update_session_project` to override the project for their
own session. Clearing that override restores a linked session's inherited
project, or leaves an ordinary session unassigned. A changed project starts a
fresh provider thread in the new workspace on the next turn; SQLite history
remains canonical and is replayed into it.

## Archive

A session carries an `archived_at` marker saying it is done with. It is the only
thing that decides whether a session belongs in the sidebar's main list or in its
collapsible Archive section — nothing is inferred at read time, so the split
survives a daemon restart.

The daemon stamps it on its own when unattended work ends:

| When | What is archived |
| --- | --- |
| A scheduled run's turn finishes | That run's session. A run that errored is as finished as one that succeeded. |
| An unattended roadmap task's turn finishes | That task's session. A task the user picked up interactively is left alone — they are still working in it. |
| A goal reaches `done` or `abandoned`, or is deleted | The goal's lead conversation. |
| A goal is reopened to `active` | Its lead conversation is unarchived. Pausing leaves the marker alone. |

The user can also archive or unarchive any session by hand from the conversation
header (`POST /api/sessions/<id>/archive`), including their own `web` and `cli`
conversations, which the daemon never archives on its own.

A turn the user sends into an archived session clears the marker: writing in a
conversation is saying it is live again. Unattended turns deliberately do not,
since that traffic is what the archive exists to keep out of the way.

The web sidebar groups the archive by goal exactly as it groups the main list,
but its goal groups start collapsed. Sessions started by an agent rather than by
the user — origin `schedule`, `roadmap`, or `goal` — also carry an `agent` chip
under their origin chip in the list.

Archiving is presentation, not deletion: history, attachments and provenance are
untouched, and no agent tool can set the marker. It is unrelated to the
`archive-done` roadmap operation, which writes tasks to disk and removes them.

## History

Message history is stored as strictly ordered `user` and `assistant` messages.
The provider's own session or thread is treated as a resumable backing resource;
Podiom's SQLite history is the canonical truth that survives daemon restarts.

Every row carries a `Kind`:

| Kind | Meaning |
| --- | --- |
| `message` | Conversation: a user turn or an agent's answer. The default. |
| `narration` | What the agent said while still working, before its answer. |
| `reasoning` | The provider's own thinking/reasoning text. |
| `error` | A durable, session-scoped diagnostic shown in the chat. |

One turn writes several rows rather than one. Each tool call ends the current
`narration` and `reasoning` rows, so a turn that writes, works, writes again and
then answers becomes a sequence of working notes followed by exactly one
`message` — the last assistant row is always the answer. Working notes are
persisted as the turn produces them, so they survive a turn that never finishes.

Chat renders `narration` and `reasoning` as visually distinct working notes, and
the `global.collapse_reasoning` setting decides whether a finished note folds
down to one line once the answer arrives. Only `message` rows replay to a
provider: working notes and diagnostics are Podiom's own record, and they are
also left out of rolling summaries, automatic naming, and copied transcripts.

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

## Context Window

The composer ring shows how full the model's context window is for the active
session. Both numbers come from the provider's own stream: the tokens are the
last request's prompt (not the turn's cumulative usage, which counts a long tool
loop's cached prompt once per call), and the window is the model's limit — Codex
reports it per thread, Claude never does, so it is looked up per model. `/compact`
resets the ring to zero.

The percentage is deliberately measured against the full model window. Claude
Code's own `/context` measures against its auto-compact window instead, which is
smaller and not exposed on the wire, so Podiom reads a little lower there; Codex
subtracts a fixed baseline from both sides of its "context left" figure, about a
point apart from Podiom's. Neither difference is a defect.

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
