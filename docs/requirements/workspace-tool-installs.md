# Podiom Workspace Tool Installs — Requirements

*Standalone implementation spec extending the Goals feature (see
`goals.md` §6). Self-contained: a developer can implement from this document
without reading the goals spec end-to-end.*

Status: v1.0 — ready for implementation.

---

## 1. Purpose & philosophy

The Goals feature lets an agent file a `cli_tool` access request when it is
missing a command-line tool. In goals v1 that request is **acknowledge-only**:
the user installs the tool on the host by hand. This spec upgrades a subset of
those requests to **installable**: on the user's approval, Podiom itself
installs the tool — *into the requesting agent's own tool directory, never
host-wide* — verifies it, and exposes it on that agent's PATH.

Guiding principles:

1. **What the user approves is exactly what runs.** The install command is
   derived mechanically from a declarative, validated payload. The agent never
   authors a shell string that Podiom executes; the free-text `install_hint`
   is display-only context. This is the load-bearing security property: an
   approval must never be a one-click "run agent-authored shell as the
   daemon".
2. **Per-agent, not host-wide.** Installs land in the agent's tool directory
   (`agents/<name>/tools/`) and only that agent's subprocess PATH sees them.
   Deleting the agent deletes its tools. Host-wide package managers (brew,
   apt) remain out of scope — those requests stay acknowledge-only exactly as
   in goals v1.
3. **Same approval rails, same audit trail.** Installable requests reuse the
   access-request lifecycle (`pending → approved → executed | failed`), the
   notification path, the goal timeline, and the retry-from-failed semantics.
   Nothing installs without an explicit human approval.
4. **Everything is inspectable and reversible.** A per-agent manifest records
   what was installed, by which request, for which goal, and when. The UI
   lists an agent's workspace tools and can remove them.

## 2. Concepts & layout

### 2.1 The agent tool directory

Each agent gains a tools area next to its workspace:

```
agents/<name>/tools/
  bin/            ← binary downloads, go installs (GOBIN), uv shims (UV_TOOL_BIN_DIR)
  npm/            ← npm prefix; executables land in npm/bin
  uv/             ← uv tool environments (UV_TOOL_DIR)
  manifest.json   ← installed-tool manifest (§2.3)
```

Scaffolded lazily on first install; absent directories are not an error.

### 2.2 PATH exposure

The agent's subprocesses see `tools/bin` and `tools/npm/bin` **prepended** to
PATH, so an installed tool wins over a same-named host tool for that agent
only. This applies to every session kind (chat, roadmap task, schedule run,
goal planning/review) because the injection happens in the adapter's
environment construction.

**Provider limitation (v1):** PATH injection is implemented for the Claude
adapter (per-turn subprocesses). The Codex adapter runs one long-lived
app-server per profile whose environment is fixed at process start, so
per-agent PATH cannot be injected there; on Codex-backed turns the tools are
still on disk but not on PATH. Documented, not worked around, in v1.

### 2.3 The manifest

`manifest.json` is the source of truth for what Podiom installed. One entry
per tool:

| Field | Notes |
|---|---|
| `tool` | executable name, the manifest key |
| `installer` | `npm \| uv \| go \| binary` |
| `package` | package/module spec as installed (or the URL for `binary`) |
| `version` | requested version, `""` = latest at install time |
| `request_id`, `goal_id` | provenance back to the approval |
| `installed_at` | RFC3339 |
| `version_output` | first line of `<tool> --version` at install time, best-effort evidence |

Podiom never edits the directory without updating the manifest and vice
versa. An entry whose files were removed out-of-band is reported as broken,
not silently dropped.

## 3. The installable request

`cli_tool` access-request payloads gain optional installer fields. **When
`installer` is absent the request is host-only and behaves exactly as goals
v1 acknowledge-only.** When present:

| `installer` | Required payload | Install command (fixed shape, argv only — never a shell) |
|---|---|---|
| `npm` | `package` (+ optional `version`) | `npm install -g --prefix <tools>/npm <package>@<version>` |
| `uv` | `package` (+ optional `version`) | `uv tool install <package>==<version>` with `UV_TOOL_DIR=<tools>/uv`, `UV_TOOL_BIN_DIR=<tools>/bin` |
| `go` | `package` (module path; version via `@`, default `@latest`) | `go install <package>@<version>` with `GOBIN=<tools>/bin` |
| `binary` | `url` (https only) + `sha256` (64 hex) | download → verify checksum → `chmod 0755` → `<tools>/bin/<tool>` |

Common required field: `tool` — the executable name the agent will invoke.

Validation (rejected at filing time, not at execution):

- `tool` must match `^[A-Za-z0-9][A-Za-z0-9._-]*$` (no paths, no spaces).
- `package` must match a conservative character set
  (`[A-Za-z0-9@/:._+-]`) — inputs are passed as single argv elements, never
  through a shell, so validation is defense-in-depth, not the only barrier.
- `binary`: `url` must be `https://…`; `sha256` must be 64 hex chars. A
  download whose digest does not match is discarded and the grant fails.
- `installer` outside the table above → invalid request.

## 4. Lifecycle

1. **File** — the agent calls `podiom_request_access` with kind `cli_tool`
   and installer fields. User is notified (unchanged).
2. **Approve** — the approve dialog shows the *exact resolved command* (or
   URL + checksum) that will run, plus the target directory. Deny works as
   always; the note is relayed to the agent.
3. **Execute (async)** — installs can take minutes, so grant execution runs
   in the background: the approve response returns the request in `approved`,
   and the outcome lands later. For an installable `cli_tool`, `approved` is
   therefore the *installing* state and the UI renders it as such. Execution
   has a hard timeout (10 minutes) and captures the trailing command output.
4. **Verify** — the grant only reaches `executed` if the expected executable
   exists (and is executable) inside the agent's tool directories after the
   installer exits successfully. `<tool> --version` is then run best-effort
   (5 s timeout); its first output line is stored as evidence but a tool
   without `--version` does not fail verification.
5. **Record** — success appends the manifest entry and an `access_decided`
   timeline event carrying the version evidence; failure sets
   `execution_error` (trailing output included) and stays retryable, exactly
   like a failed MCP grant.
6. **Use** — the agent's next session (or next turn) sees the tool on PATH.
   The review prompt's decision notes tell the agent the install happened.

Host-only requests (no `installer`) terminate at `approved` with the user
acting manually — unchanged from goals v1.

## 5. Uninstall & inspection

- `GET  /api/agents/{name}/tools` — the manifest, plus a `broken` flag per
  entry when the executable is missing on disk.
- `DELETE /api/agents/{name}/tools/{tool}` — reverses the install using the
  installer-appropriate mechanism (`npm uninstall --prefix`, `uv tool
  uninstall` with the same env, file removal for `go`/`binary`) and removes
  the manifest entry. Removal of a tool whose uninstaller fails still removes
  the manifest entry after deleting whatever files are tracked — the manifest
  must never claim a tool that was asked to be removed.
- The agent detail UI lists workspace tools (name, installer, version,
  installed date, provenance link to the goal) with a remove action behind a
  confirmation.
- Agents can see their own tools via a `podiom_list_workspace_tools` manage
  tool, so they don't re-request what they already have.

## 6. Security considerations

- **No shell, ever.** All installers run as argv arrays via `exec.Command`.
  The displayed command and the executed command are constructed by the same
  function.
- **Post-install scripts run as the daemon user.** `npm`/`uv`/`go` installs
  can execute package lifecycle code; the per-agent directory confines the
  *artifacts*, not the *installer process*. This is accepted and documented
  in v1 — the mitigations are the human approval, the declarative payload,
  and the checksum-pinned `binary` installer for the paranoid path. Real
  process sandboxing is explicitly out of scope.
- **PATH precedence is per-agent.** A malicious "install `ls`" request would
  shadow a system binary *for that agent only* — and only after the user
  approved a request that plainly names the tool. The UI must always show
  the tool name prominently in the approve dialog.
- **Network fetches** happen at install time with the daemon's network
  identity; `binary` requires https and a pinned sha256.
- Secrets never appear in payloads (inherited rule from the goals spec).

## 7. Out of scope for v1

- Host-wide installs (brew/apt) — stay acknowledge-only.
- Per-agent PATH on Codex-backed turns (§2.2 limitation).
- Process sandboxing / network egress control for installers.
- Automatic upgrades of installed tools; disk quotas.
- Sharing one installed tool between agents (each agent owns its copy).
