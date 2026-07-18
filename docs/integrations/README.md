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

## Adding a provider

Provider knowledge is consolidated so a third provider is a handful of
registrations, not a codebase-wide sweep. The boundary:

- **Behavior** — one new adapter file in `internal/adapter/` implementing the
  five-method `Adapter` interface, plus a wiring block in
  `cmd/podiomd/main.go` (the composition root).
- **Identity & data** — one `ProviderInfo` entry in
  `internal/config/provider.go`: display name, profile-dir key, install/login
  data, instruction delivery mode, native-agent projection, plan-gate
  read-only tools, fallback model catalogue, question-turn semantics. The doc
  comment on `providerInfos` is the authoritative checklist.
- **Per-layer tables** — one-line entries where behavior can't live in config:
  `usage.usageProviders` (usage endpoint), `providercheck.authProbes` (login
  probe), and optionally `mcp.nativeImports` / `skills.nativeRoots` when the
  provider has native MCP config or skill directories.
- **Frontend** — one `PROVIDERS` entry in `web/src/lib/providers.ts` plus a
  logo component in `web/src/lib/logos/`.
- **Store** — nothing. Provider validity is Go-side (`config.KnownProvider`);
  migration 25 removed the provider CHECK constraints, so no schema change.
- **Contract doc** — add `<provider>.md` here describing the integration
  contract, and a row to the table above.

Everything else (config validation, core, server, schedule, onboarding,
tokenmeter, capabilities, CLI) derives from these registries and must not
branch on provider identity. This is enforced by
`TestProviderKnowledgeStaysInRegistry` in
`internal/config/provider_drift_test.go`, which fails when `"claude"`/
`"codex"` literals or the `Provider*` constants appear outside the sanctioned
locations — if it fires, move the logic into a registry/table rather than
extending its allowlist.
