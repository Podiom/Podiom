# Voice input

Speak instead of typing. A microphone button sits next to the send button in
chat and beside the prompt fields for tasks and goals. Tap it, talk, tap again
— your speech is transcribed and appended to the field, ready to review and
edit before you send.

Transcription uses the **OpenAI Whisper API**. The audio is uploaded from the
browser to the Podiom daemon, which relays it to OpenAI server-side — your
OpenAI API key never reaches the browser. Whisper auto-detects the spoken
language, so dictating in Swedish, English, or anything else just works.

## Where the button appears

| Page | Field |
| --- | --- |
| Chat | the message composer, next to the send button |
| Roadmap | the "prompt for the agent" field in the new-task and edit-task dialogs |
| Goals | the Description and Success criteria fields on the new-goal form |

The transcript is **appended** to whatever is already in the field — nothing is
sent automatically, and nothing you typed is replaced.

## Setup

Voice input needs an OpenAI API key (create one at
[platform.openai.com](https://platform.openai.com/api-keys)). Provide it one of
three ways, checked in this order:

1. **Environment variable `PODIOM_OPENAI_API_KEY`** in the daemon's
   environment — takes precedence over everything else.
2. **Environment variable `OPENAI_API_KEY`** — the conventional name, if you
   already export it.
3. **`voice.openai_api_key` in `config.yaml`** — settable from the UI under
   **Settings → Credentials → Voice input** (Save key / Clear), or by editing the file:

   ```yaml
   voice:
     openai_api_key: "sk-…"
   ```

   The Settings page and the file are the same setting: saving in the UI
   writes this block (preserving your comments), and a key edited into the
   file shows up as "key set" in Settings after a daemon restart.

Note that a key stored in `config.yaml` is a **secret in plain text** — prefer
the environment variables if that concerns you. The key is never returned by
the API, shown in the UI, or written to a log; reads only expose whether a key
is set.

## Mobile

Recording works on phones (iOS Safari, Android Chrome), with one requirement:
browsers only allow microphone access in a **secure context** — HTTPS, or
`localhost`. The [Home Assistant app](home-assistant.md) path provides this
out of the box (HA terminates TLS). Accessing Podiom over plain
`http://<lan-ip>` will not get mic access; the button explains this when
tapped rather than failing silently.

Recordings are capped at 2 minutes per tap and encoded at a low audio bitrate,
so uploads stay small (well under a megabyte).

## Privacy

- Audio is sent to OpenAI for transcription and nowhere else. If that is not
  acceptable, don't configure a key — the feature stays dormant.
- The daemon holds the key; browsers upload only audio and receive only text.

## Troubleshooting

| Symptom | Cause / fix |
| --- | --- |
| "no OpenAI API key configured" | Add a key under Settings → Credentials → Voice input, or set `voice.openai_api_key` in config.yaml, or export an env var. |
| "OpenAI rejected the API key" | The key is wrong, revoked, or lacks access — replace it in Settings. |
| "Microphone needs HTTPS (or localhost)" | You're on an insecure origin (e.g. `http://<lan-ip>`). Use the HA app / an HTTPS proxy, or open the UI on the daemon machine. |
| "Microphone access denied" | The browser permission was declined — re-allow it in the browser's site settings. |
| "OpenAI rate limit" | Your OpenAI account is being throttled; wait and retry. |
