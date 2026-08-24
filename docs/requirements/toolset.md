# Podiom Toolset — Requirements

*Implementation spec for the shared agent toolset. Supersedes
`workspace-tool-installs.md` (v1.0, per-agent, approval-gated), whose §7
"out of scope" list this largely closes.*

Status: v2.0 — implemented.

---

## 1. Purpose & philosophy

Agents need command-line tools. Under v1 they filed a `cli_tool` access
request, waited for the user, and the tool landed in that one agent's
directory. In practice goal agents — which run with full autonomy — skipped it
and shelled out `npm install -g` instead, which pollutes a standalone host and
is *erased* on the next Home Assistant app update, since only `/data` survives.

v2 gives agents one shared, persistent place to install into,
`$PODIOM_HOME/toolset/`, and lets them do it without asking.

Guiding principles:

1. **The mechanism is the control, not the prompt.** An agent describes an
   install declaratively; Podiom builds the argv. The agent never authors a
   string Podiom executes. This is what makes an unattended install safe — the
   same property v1 relied on, now carrying the weight the approval used to.
2. **Shared, not per-agent.** One copy of a tool serves everyone. This is not
   only economy: an agent-independent path is the *only* kind that can be
   injected into the long-lived Codex app-server, which is why v1's §2.2
   provider limitation disappears here.
3. **Everything is attributed, inspectable, reversible.** The manifest records
   which agent installed what, in which session, and when. The UI lists it and
   can remove it. Nothing an agent adds is anonymous.
4. **Persistence is the point.** `$PODIOM_HOME` is `/data/podiom` under Home
   Assistant, so the toolset survives app updates by construction.

## 2. Concepts & layout

### 2.1 The toolset directory

```
$PODIOM_HOME/toolset/
  bin/            ← binaries, go (GOBIN), cargo (--root), uv shims, archive links
  npm/            ← npm prefix; executables in npm/bin
  uv/             ← uv tool environments (UV_TOOL_DIR)
  pkg/<tool>/     ← extracted archives, one directory per tool
  boot/           ← bootstrapped installers (§5); NOT on any agent PATH
  manifest.json   ← installed-tool manifest (§2.3)
```

Scaffolded with the rest of the storage root; absent subdirectories are
created lazily on first install.

### 2.2 PATH exposure

`bin` and `npm/bin` are **prepended** to the PATH of every provider
subprocess, so a toolset tool wins over a same-named host tool. Because the
path is fixed for the life of the daemon, it is supplied at adapter
construction (`ClaudeOptions`/`CodexOptions.ToolsetPathDirs`) rather than per
request, and applies to:

- **Claude** — per-turn processes, PATH set at spawn.
- **Codex** — the app-server, one long-lived process per profile. v1 could not
  reach this because the path varied per agent; this one does not.
- **The HA terminal and s6 services** — via the image `ENV PATH` and
  `/etc/profile.d/podiom.sh`, which `podiomd` does not spawn and therefore
  cannot inject into.

`boot/` is excluded from PathDirs on purpose: a bootstrapped `uv` is Podiom's
own copy and must not shadow the host's `uv` for agent commands.

**Reserved names.** Because that shadowing is global, `Spec.Validate` refuses a
set of executable names: the provider CLI names (read from the provider
registry, so a new provider protects its own binary by existing), the runtimes
those CLIs resolve through PATH (`node` above all), the installers this package
runs, Podiom's own binaries, and shell basics. Rejection happens at validation,
before anything executes.

### 2.3 The manifest

One entry per tool, keyed by `tool`:

| Field | Notes |
|---|---|
| `tool` | executable name, the manifest key |
| `installer` | `npm \| uv \| go \| cargo \| binary \| archive` |
| `package`, `version` | package/module spec as installed |
| `url`, `sha256`, `path` | download-based installers; `path` is the executable inside an archive |
| `installed_by`, `session_id` | which agent, in which session |
| `request_id`, `goal_id` | approval-era provenance, kept for migrated entries (§7) |
| `installed_at` | RFC3339 |
| `version_output` | first line of `<tool> --version` at install time |
| `needs_reinstall` | a migrated entry Podiom knows the spec for but has no files for |

Podiom never changes the directory without updating the manifest and vice
versa. An entry whose files were removed out of band is reported `broken`, not
dropped. `needs_reinstall` suppresses `broken` — it is one pending action, not
two problems.

## 3. Installers

| `installer` | Required | Command (argv only — never a shell) |
|---|---|---|
| `npm` | `package` | `npm install -g --prefix <toolset>/npm <package>@<version>` |
| `uv` | `package` | `uv tool install <package>==<version>`, `UV_TOOL_DIR=<toolset>/uv`, `UV_TOOL_BIN_DIR=<toolset>/bin` |
| `go` | `package` | `go install <package>@<version>` (default `@latest`), `GOBIN=<toolset>/bin` |
| `cargo` | `package` | `cargo install --root <toolset> [--version <v>] <crate>` — `--root` already puts executables in `<toolset>/bin` |
| `binary` | `url`, `sha256` | download → verify → `chmod 0755` → `<toolset>/bin/<tool>` |
| `archive` | `url`, `sha256` | download → verify → extract to `pkg/<tool>/` → link executable into `bin/` |

Validation (at filing time, not execution):

- `tool` matches `^[A-Za-z0-9][A-Za-z0-9._-]*$` and is not reserved (§2.2).
- `package`/`version` match a conservative character set — defense in depth,
  since these are single argv elements and never touch a shell.
- `binary`/`archive`: `url` must be `https://`, `sha256` 64 hex chars.
- `archive`: `path`, when given, must be relative and must not traverse out.

### 3.1 Archive extraction

`.tar.gz`/`.tgz` and `.zip`, detected from the file's **magic bytes** rather
than the URL, so a mislabelled download fails cleanly. No `.tar.xz` — the Go
standard library has no xz decoder.

Every entry of every format passes the same guard:

- absolute paths and `..` traversal rejected (zip-slip);
- symlinks allowed only when the target resolves inside the extraction
  directory; absolute link targets rejected;
- anything that is not a regular file, directory, or symlink rejected;
- total extracted bytes and entry count capped, with the partial directory
  removed on breach.

The whole archive is extracted, not just the executable, so tools that load
adjacent files keep working. The executable is then linked into `bin/` —
a symlink on Unix so it still runs from its own directory, a copy on Windows,
which has no dependable unprivileged symlink.

## 4. Lifecycle

1. **Install** — `podiom_install_tool` → `POST /api/toolset`. Synchronous: the
   caller is an agent that wants to use the tool next, so it waits, bounded by
   `tools.InstallTimeout` (10 minutes — a cargo or go install compiles).
2. **Verify** — the install only succeeds if the expected executable exists and
   is executable *inside the toolset directories*; a host binary of the same
   name must never satisfy the check. `<tool> --version` is then run
   best-effort (5 s) as evidence; a tool without `--version` still passes.
3. **Record** — the manifest entry is written with provenance. A failed
   install records nothing and leaves no partial files.
4. **Use** — on PATH immediately. PATH resolves at exec time, so no process
   restarts, not even the Codex app-server.
5. **Remove** — `podiom_remove_tool` (needs `confirm=true`) or the UI. The
   installer-appropriate uninstall runs, shims and any `pkg/<tool>/` directory
   are deleted, and the manifest entry goes even if the uninstaller fails —
   the manifest must never claim a tool the user asked to remove.

## 5. Bootstrapping a missing installer

An installer binary may simply not exist: `uv` on a bare host, `go`/`cargo` in
the HA container unless the matching toolchain is ticked.

- Pins live in Go (`internal/tools/bootstrap.go`) — installer → GOOS/GOARCH →
  URL + sha256 — because this must work on standalone hosts, where there is no
  image build to pass build-args to.
- **Only `uv` is pinned.** It is a dependency-free static binary that unlocks
  the `uv` installer and CPython (`uv python install`). `go` and `cargo` are
  hundreds of MB; the HA app already has a first-class path for them and a
  standalone user owns their host, so they get an error naming the fix instead
  of a surprise download.
- The bootstrap uses the same checksum verification and the same guarded
  extractor as any agent-requested install — it is no more trusted.
- Installed into `boot/<name>-<version>/`, reused across installs, and
  re-provisioned when a pin changes.

## 6. Surfaces

**HTTP**

- `GET /api/toolset` — manifest with per-entry health
- `POST /api/toolset` — install (body is the declarative spec plus provenance)
- `DELETE /api/toolset/{tool}` — uninstall + manifest removal

**MCP** (in `manage_tools.go`, beside the self-service `podiom_install_skill`)

- `podiom_list_toolset`, `podiom_install_tool`, `podiom_remove_tool`
  (destructive → `confirm=true`).

Provenance is stamped by the MCP helper from its own launch flags, never by the
model — the same rule credentials follow.

**UI** — Settings → **Toolset**, beside Credentials: tool, installer, source,
version evidence, installing agent (linking to that session), install date, a
`broken`/`needs reinstall` badge, **Reinstall**, and a confirmed remove.

**`cli_tool` access requests** remain, reduced to their honest meaning: a tool
the *user* must install host-wide. Approving acknowledges; Podiom installs
nothing. Installer fields on such a request are inert.

## 7. Migration from per-agent tools

At daemon start, each `agents/<name>/tools/manifest.json` is folded into the
toolset manifest with `needs_reinstall: true` and `installed_by` set to that
agent, skipping names the toolset already has (a live install wins over a stale
record). The per-agent manifest is then deleted, which makes the migration
idempotent; the leftover directory paths are logged once.

Files are deliberately not moved. npm prefixes and uv environments record
absolute paths and would break in a new location, and a half-moved install is
worse than none. The install spec is what carries over, so restoring a tool is
one click in the UI or one `podiom_install_tool` call.

## 8. Security considerations

- **No shell, ever.** All installers run as argv arrays; display and execution
  are built by the same function.
- **Post-install scripts run as the daemon user.** `npm`/`uv`/`go`/`cargo` can
  execute package code at install time; the toolset directory confines the
  *artifacts*, not the *process*. This trade-off is inherited from v1 and is
  **wider** here, because the result is shared by every agent and there is no
  human approval in the path. The mitigations are the declarative payload, the
  reserved-name list, full attribution, and the checksum-pinned
  `binary`/`archive` installers as the stricter option. Real process sandboxing
  remains out of scope.
- **PATH precedence is global.** §2.2's reserved-name list is what stops an
  install from shadowing something Podiom or the host depends on.
- **Network fetches** happen with the daemon's network identity; downloads are
  https-only and checksum-pinned, and extraction is guarded (§3.1).
- Secrets never appear in install fields.

## 9. Out of scope

- Automatic upgrades of installed tools; disk quotas.
- Process sandboxing / network egress control for installers.
- Bootstrapping `go` or `cargo` (§5).
- `.tar.xz` (§3.1).
- Per-agent tool overrides — one shared copy is the model.
