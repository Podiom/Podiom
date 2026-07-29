# Podiom Photo Attachments — Requirements

*Standalone, decision-complete implementation specification for durable photo
attachments in web chat.*

Status: v1.0 — implemented.

---

## 1. Scope

- **PA1** Web chat users can attach photos to user messages and the active agent
  can inspect their visual content.
- **PA2** JPEG, PNG, GIF, and WebP originals are supported. A message may contain
  at most four photos, and each original may be at most 10 MiB.
- **PA3** Video, audio, PDF, arbitrary-file, CLI attachment, and animated-image
  understanding are explicitly out of scope. An animated GIF is retained intact,
  but only its browser-rendered first frame is sent to the provider.

## 2. Browser behavior

- **PA4** The chat composer accepts photos through the paperclip picker,
  clipboard paste, and drag/drop. Draft thumbnails can be removed before send.
- **PA5** Before upload, the browser decodes each photo, scales its long edge to
  at most 2000 px, composites transparency onto white, and encodes a JPEG at
  quality 0.85. Re-encoding removes embedded metadata from the provider-facing
  visual.
- **PA6** A photo-only turn is valid. The durable user message content stays
  empty; Podiom supplies a provider-only instruction asking the agent to inspect
  the photos.
- **PA7** Slash commands cannot carry photos.
- **PA8** If photos are sent from a new-chat composer, the browser creates the
  session with REST, uploads the drafts, then sends the WebSocket turn with the
  returned attachment IDs. A failed/interrupted upload keeps completed drafts so
  retry does not upload them again.

## 3. Persistence and API

- **PA9** SQLite `attachments` rows belong to a session and optionally to one
  message. Public metadata consists of ID, session/message IDs, display name,
  detected MIME type, original byte size, normalized dimensions, and creation
  time. Filesystem paths are never serialized.
- **PA10** Originals and normalized visuals live at
  `$PODIOM_HOME/attachments/<session-id>/<attachment-id>/`; the normalized file
  is `visual.jpg` and the original uses a server-selected extension.
- **PA11** `POST /api/sessions/{id}/attachments` accepts multipart fields `file`
  and `visual`. `GET /api/attachments/{id}` returns the original,
  `GET /api/attachments/{id}/thumbnail` returns the normalized JPEG, and
  `DELETE /api/attachments/{id}` deletes only an unbound draft. All routes use
  the normal gateway-token authentication.
- **PA12** WebSocket `send_turn` and REST `POST /api/chat` accept
  `attachment_ids`. Stored and streamed `Message` objects expose `Attachments`.
- **PA13** Binding happens in the same SQLite transaction as insertion of the
  user message. IDs must be unique, unbound, owned by the target session, and no
  more than four may be bound to one message. A failed validation inserts
  neither the message nor any binding.

## 4. Validation and security

- **PA14** The server ignores client-declared MIME types and detects the original
  signature. It accepts only JPEG, PNG, GIF, and WebP signatures, and requires a
  decodable JPEG derivative with positive dimensions no greater than 2000 px.
- **PA15** The daemon caps the original and normalized parts at 10 MiB each and
  the whole multipart request at 22 MiB. Empty parts are rejected.
- **PA16** Client filenames are reduced to a display-only basename, stripped of
  control characters, and never used to select a filesystem path. Daemon UUIDs
  select storage directories.
- **PA17** Retrieval is authenticated and sent with `nosniff`, `private`, and
  `no-store` response headers. The retained original may still contain EXIF or
  other source metadata; only `visual.jpg` is metadata-stripped.

## 5. Provider delivery and replay

- **PA18** `adapter.TurnRequest` carries provider-neutral ordered image inputs.
- **PA19** Codex receives the normalized visuals as ordered `localImage` entries
  after the text entry in `turn/start`.
- **PA20** Claude receives an explicitly delimited list of normalized absolute
  paths. The session attachment directory is included in its permitted read
  roots. The same extra root is available after fallback or fresh replay.
- **PA21** Provider capability responses expose `input_modalities`. Live Codex
  and Claude catalog parsers retain advertised image support, and registry
  fallback models declare text and image support.
- **PA22** Historical photo names and paths are represented in a fresh provider
  replay, but historical images are not automatically reattached as current
  image inputs. Compaction, naming, and transcript exports retain photo names.

## 6. Lifecycle

- **PA23** Originals and visuals are durable parts of a live session. Session
  deletion removes their database rows and live files.
- **PA24** Agent and completed-task archives copy attachment trees alongside
  session artifacts. Normal Podiom home backups include live attachments.
- **PA25** Unbound drafts older than 24 hours and filesystem directories without
  database rows are removed at daemon startup and once per day.

## 7. Verification

- **PA26** Store tests cover creation, atomic binding, ordering, ownership,
  reuse, cascade deletion, and migration compatibility.
- **PA27** Server, core, adapter, capability, and web checks cover validation,
  lifecycle, delivery ordering, text-only compatibility, and attachment-only
  turns. Release verification runs `go test ./...`, `npm run check`, and
  `npm run build`.

