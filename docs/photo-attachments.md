# Photo attachments

Attach photos to a web-chat message so Claude or Codex can inspect them. Photos
remain attached to the durable user message and reappear when you reload its
history.

## Add and send photos

- Select the paperclip in the chat composer and choose one or more photos.
- Paste an image from the clipboard into the message field.
- Drag image files onto the composer.
- Review the thumbnails and select × to remove any photo before sending.
- Add an optional question or instruction, then send. A message containing only
  photos is also valid.

Starting from an empty new chat works the same way: Podiom creates the session,
uploads the photos, then starts the turn. If an upload is interrupted, leave the
composer open and retry; drafts that already reached the daemon are reused.

## Formats and limits

| Limit | Value |
| --- | --- |
| Formats | JPEG, PNG, GIF, WebP |
| Photos per message | 4 |
| Original size | 10 MiB per photo |
| Provider-facing dimensions | 2000 px maximum long edge |
| Provider-facing encoding | JPEG, quality 0.85 |

The browser converts every selected photo to a normalized JPEG. Transparency is
placed on white. For an animated GIF, the original animation is retained but the
agent sees only its rendered first frame.

Video, audio, PDFs, and arbitrary files are unsupported. The CLI has no photo
attachment flags in this release.

## What the agent receives

Codex receives the normalized photos as ordered `localImage` inputs alongside
the message text. Claude receives explicit normalized-image paths and permission
to read the session's attachment directory. The original file is retained for
you but is not sent as the provider-facing image.

For a photo-only message, Podiom gives the provider a short instruction to
inspect the photos without inventing or storing user text. Photos cannot be
combined with slash commands.

If Podiom must start a fresh provider session after compaction, a profile switch,
or fallback, replay identifies historical photos by name and makes their paths
readable. It does not automatically resend every historical image as a new
visual input. Compaction summaries and transcript copies preserve attachment
names.

## Retention, archives, and privacy

Podiom stores the original and normalized JPEG locally under
`$PODIOM_HOME/attachments/<session-id>/<attachment-id>/`. They are included when
you back up `$PODIOM_HOME`, copied into agent/task archives, and removed from live
storage when the session is deleted. Abandoned uploads are removed after 24
hours; orphan cleanup also runs at startup and daily.

The normalized JPEG is re-encoded without embedded metadata before it is shown
to the agent. The retained original is intentionally unchanged and may still
contain EXIF location, camera, author, or other metadata. Original and thumbnail
downloads require the same gateway token as the rest of the API. Filesystem paths
are not exposed in message or attachment API objects.

## View a retained original

Photos in user-message history are authenticated thumbnails. Select one to open
the retained original in a new browser view.

## Troubleshooting

| Symptom | Cause / fix |
| --- | --- |
| “use JPEG, PNG, GIF, or WebP” | The browser-reported format is unsupported; export the image to one of the listed formats. |
| “photos must be 10 MiB or smaller” | Reduce the original file size before selecting it. |
| “up to 4 photos” | Remove a draft or send the photos across separate messages. |
| “browser could not decode this image” | The file is damaged or its extension does not match its contents. |
| Upload fails after selection | Keep the drafts in the composer, restore connectivity, and send again. |
| A history thumbnail is unavailable | The live attachment files are missing or the gateway session needs to be unlocked again. |

