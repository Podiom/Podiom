# Home Assistant Add-on: Podiom

Agentic AI colleagues — powered by the `claude` and `codex` CLIs — with a web
UI for chat, permissions, plans, schedules, and memory.

## About

This add-on runs the full Podiom stack (daemon, web UI, both CLIs,
`mcp-proxy`, and a web terminal for CLI logins) inside Home Assistant:

- **Safe remote access**: reach Podiom from your phone through HA / Nabu Casa
  with HA handling TLS and login — Podiom opens no ports.
- **Free full-state backups**: HA backups capture sessions, agents, memory,
  schedules, and CLI logins.
- **Reliable schedules**: start-on-boot + watchdog means scheduled agents and
  nightly dreaming actually run.

See the **Documentation** tab for the first-run walkthrough and security
notes.
