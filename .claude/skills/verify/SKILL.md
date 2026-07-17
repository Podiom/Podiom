---
name: verify
description: Build and drive a scratch podiomd daemon to verify changes at the real REST/WS surface without touching the user's live daemon or ~/.podiom.
---

# Verifying Podiom changes against a live daemon

The user's real daemon usually occupies 127.0.0.1:8787 — always run a scratch
daemon on another port with an isolated `PODIOM_HOME`.

## Build & launch

```bash
SCRATCH=$(mktemp -d)
go build -o $SCRATCH/podiomd ./cmd/podiomd     # embeds web/dist — run `cd web && npm run build` first if the SPA changed
mkdir -p $SCRATCH/home
PODIOM_HOME=$SCRATCH/home $SCRATCH/podiomd &   # first run scaffolds config.yaml + gateway.token, then FAILS on port 8787
```

First launch scaffolds `$SCRATCH/home/config.yaml` then dies on the port
conflict. Edit the scaffolded config's existing `server:` block (do NOT append
a second `server:` key — duplicate keys are a YAML parse error) to
`port: 18787`, then relaunch.

## Drive it

- Token: `TOKEN=$(cat $SCRATCH/home/gateway.token)`; every /api call needs
  `Authorization: Bearer $TOKEN`.
- REST: `curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:18787/api/...`
- Create an agent: `POST /api/agents {"name":"x","provider":"claude"}`;
  a session: `POST /api/sessions {"agent_name":"x","origin":"web"}`.
  Session creation composes instructions without running a turn — inspect
  `$SCRATCH/home/agents/<name>/workspace/CLAUDE.md` (or AGENTS.md for codex)
  to verify instruction-layer changes.
- WebSocket: dial `ws://127.0.0.1:18787/api/ws` with the bearer header. A
  tiny `go run` script using `nhooyr.io/websocket` (already in go.mod) works;
  send `{"type":...,"request_id":...}` JSON and read replies.
- SPA: fetch `/` and the hashed `/assets/index-*.js` to confirm new UI strings
  made it into the embedded bundle.

## Gotchas

- Don't drive turns with a real provider from a scratch daemon — the claude
  CLI uses the user's real login and burns their quota. Turn flows are covered
  by `internal/server/ws_test.go` patterns with `adapter.NewFake()`.
- Kill the scratch daemon when done; it starts schedulers and pollers.
