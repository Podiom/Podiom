# Podiom MCP Servers — Requirements

*Standalone implementation spec for the MCP-server feature of Podiom.
Self-contained: a developer can implement from this document without reading the
full Podiom requirements. Cross-references to the main doc (e.g. §5.2, §8.7) and
to the skills spec are for context only.*

Status: v1.0 — ready for implementation. Codex mechanism is **test-verified**
(see §9); Claude mechanism is documented-and-verified via CLI flags.

---

## 1. Purpose & philosophy

MCP (Model Context Protocol) servers give agents access to external tools,
databases, and services. Claude and Codex both consume MCP servers, but they
**configure them completely differently** (format, location, transport support).
This feature lets a user manage MCP servers in one place and guarantees that an
agent gets its **assigned MCP servers regardless of which provider backs a given
turn** — so a Claude→Codex fallback mid-session does not silently drop tool
access.

Guiding principles:

1. **Podiom owns a canonical definition, not the native configs.** Podiom never
   edits the user's `~/.claude.json` or `~/.codex/config.toml`. It holds its own
   canonical MCP catalogue and *projects* it into each backend per invocation.
2. **Podiom never handles credentials.** The canonical definition references
   secrets by **environment-variable name**, never by value. Actual secrets live
   in the user's environment / keychain, exactly as both CLIs already require.
3. **Per-agent assignment (deliberately unlike skills).** Skills are a shared
   union with no per-agent mapping. MCP servers are the opposite: **each agent is
   explicitly assigned which servers it may use.** This asymmetry is intentional
   and risk-based — see §2.
4. **Bridge across providers where possible.** A server that natively works for
   only one provider should, where feasible, be made to work for the other
   (e.g. `mcp-proxy` to bridge streamable-HTTP↔stdio). See §7.
5. **Strict, not additive.** An agent sees exactly its assigned servers — no
   leakage of the user's native servers. On Claude this is declarative; on Codex
   it must be constructed (see §5/§9).

---

## 2. Why MCP is per-agent while skills are a shared union

This is a deliberate design asymmetry and should not be "corrected" for the sake
of consistency:

- **Skills are passive capabilities.** An available-but-unused skill is inert —
  it sits until a task matches its description. Low risk → shared union, no
  per-agent control (see the skills spec).
- **MCP servers are active, often sensitive capabilities.** A server can grant
  database access, API write scopes, filesystem operations, or third-party
  service reach — often carrying credentials. A calendar agent holding GitHub
  write access it never needs is both needless attack surface and a source of
  accidental side effects.

The risk gradient justifies the difference: skills are low-risk-and-shared, MCP
is higher-risk-and-scoped. The requirement is therefore **explicit per-agent
assignment** of MCP servers.

---

## 3. Data model

### 3.1 Canonical MCP catalogue (`~/.podiom/mcp.yaml`)

Podiom holds a provider-neutral definition of every known MCP server. Secrets are
referenced by env-var **name** only (Principle 2).

```yaml
mcp_servers:
  - name: github
    transport: http                 # http | stdio
    url: https://api.githubcopilot.com/mcp/
    auth_env: GITHUB_PAT            # NAME of the env var, never the value
  - name: filesystem
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "~/projects"]
  - name: google-calendar
    transport: stdio
    command: npx
    args: ["-y", "@some/gcal-mcp"]
    auth_env: GCAL_TOKEN
```

- **M1** The catalogue is the single canonical, provider-neutral definition of
  known MCP servers.
- **M2** Secrets are referenced by env-var name only. Podiom MUST NOT store,
  read, or write secret values. Missing env vars are a user/runtime concern,
  surfaced (§8) but never resolved by Podiom storing a value.

### 3.2 Catalogue population (read from natives, add via Podiom)

- **M3** Podiom builds the catalogue from two sources, deduped by server name:
  1. **Imported** — servers already defined in the user's `~/.claude.json`
     (JSON `mcpServers`) and `~/.codex/config.toml` (`[mcp_servers.*]`). Reading
     these is **also a prerequisite for strict parity on Codex** — Podiom must
     know the native servers in order to explicitly disable them (§5.2/§9).
  2. **User-added** — servers the user creates through Podiom's UI/CLI, written
     to `~/.podiom/mcp.yaml`.
- **M4** Podiom **reads** the native config files to import and to enable strict
  parity, but **never writes** them (Principle 1).

### 3.3 Per-agent assignment

- **M5** Each agent has an explicit list of assigned MCP server names drawn from
  the catalogue:

  ```yaml
  agents:
    - name: jared
      mcp_servers: [google-calendar]
    - name: gilfoyle
      mcp_servers: [github, filesystem]
  ```

- **M6** An agent with no `mcp_servers` list gets **no** MCP servers (empty, not
  "all") — assignment is opt-in, consistent with the scoping intent of §2.

---

## 4. Consolidation guarantee

- **M7** An agent's assigned server set is delivered **identically regardless of
  which provider backs the turn.** A Claude→Codex (or reverse) switch mid-session
  — including via the fallback chain — MUST preserve the same assigned MCP set,
  so tool access is not lost on failover. This is the core reason the feature
  exists.

---

## 5. Projection to backends (per-invocation, non-invasive)

Podiom projects the agent's assigned set into each backend **at launch, without
touching native config files or `CLAUDE_CONFIG_DIR` / `CODEX_HOME`.** Verified
mechanisms:

### 5.1 Claude — `--mcp-config` + `--strict-mcp-config` (clean)

- **M8** Podiom generates JSON for the assigned servers (in `mcpServers` shape)
  and passes it via **`--mcp-config <file-or-inline-json>`** on the `claude -p`
  invocation.
- **M9** Podiom adds **`--strict-mcp-config`** so Claude uses **only** the
  supplied servers and ignores all other MCP config sources. This gives strict
  parity (Principle 5) declaratively. Both flags work in non-interactive `-p`
  mode.

### 5.2 Codex — generated profile overlay (test-verified, §9)

Codex has no per-invocation "add a new server" flag equivalent to Claude's
(`-c` overrides cannot reliably introduce a server absent from base config). The
working mechanism is a **generated profile file**:

- **M10** Podiom generates a profile file `~/.codex/podiom-<agent>.config.toml`
  containing the agent's assigned `[mcp_servers.*]` tables, and launches Codex
  with **`--profile podiom-<agent>`**. This introduces new servers per
  invocation without editing base `config.toml` and without changing
  `CODEX_HOME`. **(Verified: §9 Step 1.)**
- **M11 — Approval mode is mandatory in `codex exec`.** Each server table in the
  generated profile MUST set `default_tools_approval_mode = "approve"` (or an
  appropriate per-tool mode), otherwise non-interactive `codex exec` tool calls
  are aborted by the approval flow. **(Verified: §9 Step 1.)**

  ```toml
  [mcp_servers.podiomprobe]
  command = "npx"
  args = ["-y", "@some/mcp"]
  default_tools_approval_mode = "approve"
  ```

- **M12 — Strict parity is constructed, not declarative.** A Codex profile is
  **additive**: base-config servers leak through alongside the profile's servers.
  **(Verified: §9 Step 2.)** The declarative allowlist (`allowed_mcp_servers`)
  lives in **`requirements.toml`** (managed/admin policy) and is **not** available
  in a normal profile — so Podiom cannot use it. **(Verified: §9 Step 3.)**

  To achieve strict parity, Podiom's generated profile MUST **explicitly disable
  every known base server** it does not want, using `enabled = false`. This is
  why importing native servers (M3) is a prerequisite: Podiom can only disable
  the base servers it knows about.

  ```toml
  # Assigned server(s):
  [mcp_servers.google-calendar]
  command = "npx"
  args = ["-y", "@some/gcal-mcp"]
  default_tools_approval_mode = "approve"

  # Explicitly disable known base servers for strict parity:
  [mcp_servers.node_repl]
  enabled = false

  # Plugin-bundled servers use their fully-qualified table path:
  [plugins."computer-use@openai-bundled".mcp_servers.computer-use]
  enabled = false
  ```

  **(Verified: the strict profile above yielded only the assigned server, with
  the assigned tool still working — §9 Step 3.)**

- **M13 — Verify via `codex exec`, not `codex mcp list`.** In testing,
  `codex --profile <p> mcp list` did **not** show the profile-injected server,
  while `codex exec --profile <p> ...` used it correctly. Any Podiom health-check
  / verification of Codex MCP availability MUST go through an `exec`-style run,
  never `mcp list`. **(Verified: §9 caveat.)**

### 5.3 No continuous sync

- **M14** Podiom does **not** continuously write into native configs. It holds
  the canonical catalogue and **projects per invocation** (a Claude JSON; a Codex
  profile file). This removes any risk of overwriting user config and keeps
  standalone CLI use untouched — the same non-invasive posture as the skills
  feature's "Stance 2".

---

## 6. Handling a server that only one provider supports

- **M15** If an assigned server cannot be delivered to the provider backing the
  current turn, Podiom prefers to **bridge** (§7). Where bridging is not possible,
  Podiom surfaces the limitation honestly (§8) rather than silently dropping the
  server — the user should see "available on Claude, bridged on Codex" or
  "unavailable on Codex", not a silent gap.

---

## 7. Cross-provider bridging (`mcp-proxy`)

The providers differ in transport support: Claude handles remote streamable-HTTP
(and SSE) natively; Codex historically runs MCP servers locally over stdio, so a
remote HTTP server must be bridged.

- **M16** For an **HTTP/remote** server assigned to a **Codex** turn, Podiom
  generates an `mcp-proxy`-backed **stdio** entry in the Codex profile, bridging
  streamable-HTTP → stdio. (`mcp-proxy` is assumed present on the system where
  Codex uses it.)
- **M17** For a **stdio** server assigned to a **Claude** turn, no bridge is
  needed — Claude handles stdio natively; Podiom emits the stdio entry directly.
- **M18** Podiom detects transport type from the catalogue entry (§3.1) and
  inserts the bridge only where required, per target provider.
- **M19** Bridging is best-effort (Principle 4). Where a bridge cannot be
  established, fall back to honest surfacing (M15).

---

## 8. Surfaces (UI + CLI)

Unlike skills (observational), MCP is **controlling** — the user assigns servers
to agents. Assignment is editable from **two** entry points onto the same
underlying data.

### 8.1 Two editing paths (same data, two viewpoints)

- **M20 — Agent editor (agent-centric).** When creating/editing an agent, the
  user sees the catalogue of available servers with per-agent toggles: "which
  servers does *this agent* get?" Answers the question from the agent's side.
- **M21 — "Skills & MCP" page (server-centric).** A system overview: each server
  and which agents use it, editable here too. Answers from the server's side.
- **M22** Both paths edit the **same assignment data** (the agent↔server
  mapping). The underlying mental model is an **assignment matrix** (agents ×
  servers); the two pages are two projections of that matrix and must stay
  consistent.

### 8.2 What the MCP surface shows

- **M23** The catalogue: each server with name, transport (http/stdio), and its
  **source badge** — imported-from-`claude`, imported-from-`codex`, or
  `podiom` (user-added) — mirroring the skills source-badge pattern.
- **M24** Per server, which agents are assigned it (server-centric view) and, per
  agent, which servers it has (agent-centric view).
- **M25** **Credential status, by name only.** For a server with `auth_env`,
  show whether that env var is *present in the environment* (set / unset) — never
  the value. This helps the user see "GITHUB_PAT is not set" without Podiom ever
  reading the secret.
- **M26** **Provider-availability / bridge indicator.** Show whether an assigned
  server is native, bridged (`mcp-proxy`), or unavailable per provider (§6/§7).
- **M27** Adding a server: a form writing to `~/.podiom/mcp.yaml` (name,
  transport, command/args or url, `auth_env` name). Podiom instructs that the
  secret itself goes in the environment, not here.

### 8.3 CLI

- **M28** `podiom mcp list` — catalogue with source badges and transport.
- **M29** `podiom mcp show <name>` — a server's canonical definition and which
  agents are assigned it; credential status by env-var name.
- **M30** `podiom mcp assign <server> <agent>` / `podiom mcp unassign <server>
  <agent>` — edit the assignment matrix.
- **M31** `podiom mcp add` / `podiom mcp remove` — manage catalogue entries in
  `~/.podiom/mcp.yaml` (never touching native configs).
- **M32** `podiom mcp check <agent>` — dry-run the projection for an agent and
  report, per provider, which servers would be native / bridged / unavailable.
  (For Codex, verification must run via an `exec`-style probe, not `mcp list` —
  M13.)

### 8.4 Design intent (for Claude Design)

Where the skills page was "a catalogue you browse", the MCP page is "a wiring
board you operate" — but a calm, legible one. The core visual job is the
**assignment matrix**: at a glance, which agents can reach which servers. Source
badges and credential-status (set/unset, by name) must be honest and immediately
readable. Never display or prompt for secret values.

---

## 9. Test-verified Codex behaviour (evidence log)

The Codex mechanism (§5.2) rests on these observed results, not assumption:

- **Step 1 — profile can introduce a new server.** `codex exec --profile <p>`
  loaded and successfully called a server (`podiomprobe` /
  `podiom_probe_ping` → `PODIOM-MCP-OK-...`) that existed **only** in the profile
  file, not in base `~/.codex/config.toml`. Confirms per-invocation injection
  without touching base config or `CODEX_HOME`.
- **Step 1 caveat — approval mode required.** Non-interactive `codex exec`
  aborted the tool call until the profile server set
  `default_tools_approval_mode = "approve"` (M11).
- **Step 2 — profiles are additive.** With the profile active, Codex saw base
  servers (`node_repl`, `computer-use`) **plus** the profile server
  (`podiomprobe`). Base servers leak through (M12).
- **Step 3 — `allowed_mcp_servers` is managed-only.** The allowlist key belongs
  to `requirements.toml` (admin/managed policy), not a normal profile/config, so
  Podiom can't use it. The working strict-parity method is explicit
  `enabled = false` for each known base server (incl. the plugin-qualified table
  path for bundled servers). With that, Codex saw **only** `podiomprobe` and the
  tool still worked (M12).
- **Caveat — `mcp list` lies, `exec` tells the truth.** `codex --profile <p> mcp
  list` did not show the profile-injected server, but `codex exec --profile <p>`
  used it correctly. Verify via `exec`, never `mcp list` (M13).

---

## 10. Out of scope (v1) / future

- Editing/enabling MCP servers *inside* the user's native configs (Podiom only
  ever projects; it never writes natives).
- Automatic secret management (Podiom never handles secret values; env-var name
  references only).
- Project-scoped MCP servers (`.mcp.json` / project `.codex/config.toml`) — v1
  concerns the global/personal level and Podiom's own catalogue.
- Discipline around **Codex remote-HTTP native support**: if a future Codex gains
  native remote-HTTP, the `mcp-proxy` bridge (§7) becomes optional for those
  servers — revisit M16 then.
- A managed/enterprise path using Codex `requirements.toml` allowlists (v1 uses
  the profile `enabled = false` construction instead).

---

## 11. Acceptance checks

A correct implementation satisfies all of:

1. A server added via Podiom (`~/.podiom/mcp.yaml`) is assignable to an agent and
   reaches that agent on **both** a Claude turn and a Codex turn (M7).
2. An agent's Claude turn sees **only** its assigned servers — a native
   `~/.claude.json` server not assigned to it does **not** appear
   (`--strict-mcp-config`, M9).
3. An agent's Codex turn sees **only** its assigned servers — known base servers
   are disabled via generated `enabled = false` entries (M12); verified through
   an `exec`-style probe, not `mcp list` (M13).
4. Non-interactive Codex runs do not stall on approval — the generated profile
   sets `default_tools_approval_mode` appropriately (M11).
5. Podiom never modifies `~/.claude.json` or `~/.codex/config.toml`, and never
   sets `CLAUDE_CONFIG_DIR` / `CODEX_HOME` for MCP purposes (M4/M14).
6. Podiom stores no secret values; `mcp.yaml` contains only env-var names, and
   the UI shows credential status as set/unset by name (M2/M25).
7. An assigned HTTP server reaches a Codex turn via a generated `mcp-proxy` stdio
   bridge (M16), or is honestly surfaced as unavailable if no bridge is possible
   (M15/M19).
8. Assignment edited in the agent editor and in the "Skills & MCP" page stay
   consistent (same matrix, M22).
