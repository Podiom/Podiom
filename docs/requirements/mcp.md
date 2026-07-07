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
2. **Env values are optional, stored per-server.** Each env var has a name and
   an *optional* value. A blank value falls back to the daemon's own OS
   environment (the original pass-through model — actual secret lives outside
   Podiom). A stored value is kept in the user's own `~/.podiom/mcp.yaml` (a
   private, single-user file the daemon already treats as sensitive) and
   injected directly into that server's subprocess/config at launch — this
   lets a server-specific credential (e.g. a stdio server's own username/
   password) live with the server definition instead of requiring a
   daemon-wide env var. See §3.1.
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

Podiom holds a provider-neutral definition of every known MCP server. Each
stdio server's `env_vars` is an ordered **name → value mapping**; the value is
optional per entry (Principle 2).

```yaml
mcp_servers:
  - name: github
    transport: http                 # http | stdio
    url: https://api.githubcopilot.com/mcp/
  - name: filesystem
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "~/projects"]
  - name: unifi-network
    transport: stdio
    command: /usr/local/bin/uvx
    args: ["unifi-network-mcp@latest"]
    env_vars:
      UNIFI_NETWORK_HOST: "192.168.1.7"
      UNIFI_NETWORK_PORT: "8443"
      UNIFI_NETWORK_USERNAME: "mar-schmidt"
      UNIFI_NETWORK_PASSWORD: "D69H3rmgY7"   # stored value, injected at launch
  - name: google-calendar
    transport: stdio
    command: npx
    args: ["-y", "@some/gcal-mcp"]
    env_vars:
      GCAL_TOKEN: ""                # blank = pass through from the daemon's OS env
```

- **M1** The catalogue is the single canonical, provider-neutral definition of
  known MCP servers.
- **M2** Each env var has a name and an optional value. A blank value is a
  pass-through reference (resolved from the daemon's own OS environment at
  projection time, §5); a non-blank value is stored by Podiom in
  `~/.podiom/mcp.yaml` and injected directly into that server's
  command/config at projection time. Podiom never sends stored values
  anywhere except the server's own subprocess env / generated config entry.

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
- **M33** Each generated stdio server entry includes an `env` object with the
  server's resolved env vars (stored value if set, else the daemon's own
  `os.Getenv(name)`; entries that resolve to nothing are omitted). This is the
  only place a stored value ever leaves `~/.podiom/mcp.yaml` — written to a
  per-turn `0600` temp file alongside `command`/`args`, never to
  `~/.claude.json`.

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

- **M34** An assigned server's table gets the same resolved-env treatment as
  M33, rendered as an inline TOML table: `env = { KEY = "value", ... }`.
  Disabled base-server tables (the `enabled = false` stanzas above) never get
  an `env` line — they exist only to shut the native server off.

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
- **M25** **Credential status, by name.** For each env var, show whether it's
  *resolved* (set / unset) — a stored value counts as set, otherwise it falls
  back to presence in the daemon's own OS environment. The status indicator
  never displays the value itself.
- **M26** **Provider-availability / bridge indicator.** Show whether an assigned
  server is native, bridged (`mcp-proxy`), or unavailable per provider (§6/§7).
- **M27** Adding a server: a form writing to `~/.podiom/mcp.yaml` (name,
  transport, command/args or url, and one row per env var with a name field
  and an optional value field). A blank value field is a pass-through
  reference to the daemon's own environment, exactly as before; a filled-in
  value is stored with the server. Value inputs are masked (type=password)
  with a per-row reveal toggle, not shown in plaintext by default.

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
- Encryption at rest for stored env var values — `~/.podiom/mcp.yaml` is
  `0600`, single-user, same trust boundary as `~/.claude.json`/`~/.codex/`
  today; revisit if Podiom ever becomes multi-user.
- Secret values for **HTTP-transport** servers — only stdio servers' env vars
  are ever injected anywhere (§5); an HTTP server's `env_vars` still only
  drives the credential-status indicator (M25).
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
6. A stdio server's stored env var values are injected into its actual
   Claude/Codex config projection (M33/M34), not just tracked as a status
   indicator — a server whose credential Podiom stores connects successfully
   without that credential being present in the daemon's own OS environment.
7. An assigned HTTP server reaches a Codex turn via a generated `mcp-proxy` stdio
   bridge (M16), or is honestly surfaced as unavailable if no bridge is possible
   (M15/M19).
8. Assignment edited in the agent editor and in the "Skills & MCP" page stay
   consistent (same matrix, M22).
