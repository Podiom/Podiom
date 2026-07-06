# Stdio MCP servers can race a "still connecting" state on every turn

## Symptom

An agent (e.g. "Dinesh", Claude provider) assigned a stdio MCP server that
proxies to a remote service (observed with a Home Assistant instance via
`mcp-proxy --transport streamablehttp http://<host>:9583/<token>`) reported
the server as "still connecting" and had no tools available, even though
Podiom's own "Test connection" button for that server showed OK. Retrying
the same question later in the same conversation worked.

## Root cause

`Claude` drives Claude Code as a **per-turn process**
([internal/adapter/claude.go:37](../../internal/adapter/claude.go#L37)):
every single user message spawns a brand-new `claude -p ... --mcp-config
<file> --strict-mcp-config` process
([internal/adapter/claude.go:88-126](../../internal/adapter/claude.go#L88-L126)),
with a freshly written MCP config file
([writeMCPConfig](../../internal/adapter/claude.go#L333-L370)). There is no
persistent, long-lived session that keeps MCP connections warm across turns.

This means that for every turn, Claude Code CLI must:

1. Spawn the configured stdio command (`mcp-proxy`) from scratch.
2. Wait for `mcp-proxy` to complete its own outbound handshake to the
   upstream endpoint (here, an HTTP/SSE connection to a Home Assistant
   instance over the local network).
3. Only then can the server's tools be considered available for that turn's
   model call.

If step 2 doesn't finish before Claude Code decides which tools exist for
that turn, the server is reported as "still connecting" and its tools are
simply unavailable for that turn — there is no retry within the same turn.
Because every subsequent message is a brand-new process, this is a fresh
race every time, not a one-time warm-up cost. "Fiddling" and asking again
didn't fix anything — it just won the race on a later attempt.

This is invisible for MCP servers that start instantly (most local stdio
servers), but any stdio server whose startup involves real network latency
(a proxy, a remote API handshake, auth, etc.) pays this cold-start tax on
every single turn and can lose the race intermittently.

## Why the "Test connection" button doesn't catch this

Podiom's connection test ([internal/mcp/test.go](../../internal/mcp/test.go#L130-L214))
is a fully separate code path: it spawns its own one-off process, waits
synchronously for a complete `initialize` + `tools/list` round trip (up to a
10s timeout,
[DefaultTestTimeout](../../internal/mcp/test.go#L20)), and reports OK. There
is no LLM call racing against it, so it has a generous, uncontended time
budget. It correctly verifies that the server *config* is valid and
reachable, but it says nothing about whether the connection will complete
in time during a real, latency-sensitive agent turn. "Test = OK" and
"agent turn = still connecting" are consistent, not contradictory — they
exercise different code paths with different timing characteristics.

## Possible mitigations (not yet implemented)

- **Give Claude Code more time to finish MCP handshakes before generating.**
  Claude Code CLI reportedly honors an `MCP_TIMEOUT` environment variable
  (milliseconds) controlling how long it waits for MCP servers to become
  ready before proceeding. Podiom's `Claude.env()`
  ([internal/adapter/claude.go:372](../../internal/adapter/claude.go#L372))
  currently does not set this — it could be raised for stdio servers known
  to proxy to a remote endpoint. Needs verification against current Claude
  Code docs/behavior before relying on it.
- **Keep the stdio bridge warm across turns** instead of respawning it per
  process. E.g. run `mcp-proxy` (or an equivalent long-lived bridge) as a
  Podiom-managed background process per assigned server, and have the
  per-turn `--mcp-config` point at an already-connected local endpoint
  instead of asking Claude Code to spawn+connect it from scratch every
  time. This would move the cold-start cost from "every turn" to "once,
  when the server is first assigned/started."
- **Surface partial-connection state to the user in Podiom's UI** so it's
  clear when an assigned MCP server is subject to this race (e.g. flag
  stdio servers whose command output/behavior implies a network handshake),
  rather than only ever showing the static test-time OK/fail.

## Open questions

- Does Claude Code actually expose `MCP_TIMEOUT` (or an equivalent) today,
  and what's its default value? Needs confirming against current docs.
- Would keeping `mcp-proxy` warm per-assigned-server conflict with the
  per-turn process model's other guarantees (e.g. clean state between
  turns, ability to change model/effort per turn)? Likely not, since the
  MCP bridge is independent of the Claude process lifecycle, but worth
  checking for resource/lifecycle edge cases (server reassigned/removed
  mid-session, Podiom restart, etc.).
