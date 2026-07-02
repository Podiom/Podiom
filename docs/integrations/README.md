# Integration contracts

This directory holds the **per-provider integration contract** — exactly how
Podiom drives each backing CLI. It is the source of truth the `internal/adapter`
layer implements against (requirements §8).

| Provider | Contract | Status |
| --- | --- | --- |
| Claude | [`claude.md`](claude.md) | implemented (final v1 contract) |
| Codex | [`codex.md`](codex.md) | implemented (final v1 contract) |

These pages describe the contract **as implemented and shipped** in v1 — the
flags, protocol messages, and permission flow above match the `internal/adapter`
code. Security behaviour common to both providers (permission modes, profile
isolation, MCP-config and credential redaction, run logging) is documented in
[`../security.md`](../security.md).

The grounding reference (process model, CLI parameters, app-server protocol, and
the OpenClaw policy values Podiom deliberately inverts) is captured in
[`../requirements.md`](../requirements.md) Appendix A. Podiom adopts the
*mechanisms* but inverts the *defaults* (`approve` over `bypassPermissions`,
inherit native MCP over strict host-only) per Principle 6 / §8.4b.

Two process models behind one interface (D7):

- **Claude — per-turn.** `claude -p` with stream-json stdin/stdout; resume via a
  persisted session ID + `--resume`.
- **Codex — long-lived app-server.** A single `codex app-server --listen
  stdio://` process; lifecycle via `thread/start` / `thread/resume` /
  `turn/start`; resume via a persisted `threadId`.
