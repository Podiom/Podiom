# The toolset

> **Not the same thing as container toolchains.** The toolset is
> agent-installed CLI tools and works on every installation type. Language
> *toolchains* (Go, Rust, Swift, …) in the Home Assistant app are whole
> language runtimes, chosen by you on the app's Configuration page — see
> [home-assistant.md](home-assistant.md#language-toolchains).

When an agent needs a command-line tool it doesn't have, it installs it itself
into **`$PODIOM_HOME/toolset/`** — one shared directory on the PATH of every
agent session. No approval, no waiting for you. What keeps it controlled is the
*mechanism*, not a prompt: the agent hands Podiom a declarative description of
the install, never a command line, and everything it adds is recorded, visible,
and removable. The full behavior is specified in
[requirements/toolset.md](requirements/toolset.md).

## Why it exists

Agents running a [goal](goals.md) have full autonomy, so before this they would
simply run `npm install -g` in a shell. That works badly in two different ways:

- On a standalone install it scatters tools across your host, outside anything
  Podiom knows about or can clean up.
- In the [Home Assistant app](home-assistant.md) it does not survive. Only
  `/data` persists, so anything installed elsewhere disappears at the next app
  update and the agent discovers it is missing all over again.

The toolset lives under `$PODIOM_HOME` — which is `/data/podiom` in the HA app —
so it persists in both places, and one copy serves every agent.

## How a tool gets installed

1. An agent calls `podiom_install_tool` with the executable name, an installer,
   and that installer's fields.
2. Podiom builds the argv from those fields and runs it. The agent never
   authors a string that Podiom executes.
3. The call returns only once the executable has actually appeared in the
   toolset and `tool --version` has been captured as evidence. A failure comes
   back with the installer's output, so the agent can fix it or tell you why it
   can't.
4. The tool is on PATH from the agent's next command — PATH is resolved at exec
   time, so nothing needs restarting.

## Installers

| `installer` | Fields | What runs |
| --- | --- | --- |
| `npm` | `package`, optional `version` | `npm install -g --prefix <toolset>/npm <package>@<version>` |
| `uv` | `package`, optional `version` | `uv tool install <package>==<version>` with `UV_TOOL_DIR`/`UV_TOOL_BIN_DIR` pointed into the toolset |
| `go` | `package` (module path) | `go install <package>@<version>` (default `@latest`) with `GOBIN=<toolset>/bin` |
| `cargo` | `package`, optional `version` | `cargo install --root <toolset> <crate>` |
| `binary` | `url` (https), `sha256` | download → verify the pinned checksum → `<toolset>/bin/<tool>` |
| `archive` | `url` (https), `sha256`, optional `path` | download → verify → extract to `<toolset>/pkg/<tool>/` → link the executable into `bin/` |

`archive` handles `.tar.gz` and `.zip`, which is how most CLI tools are
released on GitHub. The whole archive is extracted, so a tool that needs files
next to its binary still finds them; `path` names the executable inside it when
searching for one called `<tool>` would find the wrong file.

**If the installer itself is missing**, Podiom fetches a checksum-pinned `uv`
into `toolset/boot/` and uses that — so `uv` (and Python tooling generally)
works on a host that has never seen it. `go` and `cargo` are full toolchains
and are *not* auto-downloaded: they fail with what to do instead (tick the
matching toolchain in the HA app, or install it on the host). `toolset/boot/`
is Podiom's own cache and is deliberately not on any agent's PATH.

## Where things live

```text
$PODIOM_HOME/toolset/
  bin/            # binaries, go/cargo installs, uv shims, archive links
  npm/            # npm prefix (executables in npm/bin)
  uv/             # uv tool environments
  pkg/<tool>/     # extracted archives
  boot/           # bootstrapped installers — NOT on the agent PATH
  manifest.json   # what was installed, by which agent, in which session, when
```

`bin` and `npm/bin` are prepended to the PATH of every provider subprocess, so
a toolset tool wins over a same-named host tool.

Because that shadowing applies to everyone, names that would displace something
Podiom or your host depends on are refused outright: `node`, `npm`, `git`,
`python`, `go`, `uv`, the provider CLIs, the shell basics. An agent asking for
one of those gets an error explaining why.

## Both providers, and the terminal

Unlike the per-agent tool directories this replaced, the toolset path does not
depend on which agent is running. That single fact is what lets it reach
everywhere:

- **Claude-backed turns** — per-turn processes, PATH injected at spawn.
- **Codex-backed turns** — the app-server is one long-lived process shared by
  every agent, so a per-agent path could never be injected into it. One shared
  path can be, and is.
- **The HA terminal** — started by s6 rather than by `podiomd`, so it picks the
  toolset up from the image environment and `/etc/profile.d/podiom.sh`.

## Inspecting and removing

**Settings → Toolset** lists every tool with its installer, source, version,
which agent installed it (click through to that conversation), and when.
Removing one uninstalls it and drops the manifest entry.

Two badges can appear:

- **broken** — the manifest lists the tool but its executable is gone from
  disk, removed out of band. Reported, never silently dropped.
- **needs reinstall** — carried over from the older per-agent layout (see
  below). Podiom knows exactly how it was installed but has no files for it
  yet; **Reinstall** restores it, and any agent can do the same in one call.

Agents check what already exists with `podiom_list_toolset` instead of
re-installing, and `podiom_remove_tool` requires `confirm=true` because the
toolset is shared.

## Upgrading from per-agent tools

Podiom used to install tools into `agents/<name>/tools/`, gated behind an
approved `cli_tool` access request. On first start after upgrading, those
manifests are folded into the shared toolset and each entry is marked **needs
reinstall**.

The files are not moved: npm prefixes and uv environments record absolute paths
and would break somewhere new. The install *spec* is what carries over, which
is why restoring one is a single click or a single tool call. The old
`agents/<name>/tools/` directories are left in place and their paths are logged
once — nothing uses them any more, and you can delete them.

`cli_tool` access requests still exist, but they now mean one thing only: a
tool **you** must install host-wide. Approving one acknowledges it and shows
you the suggested command; Podiom installs nothing.

## HTTP API

- `GET    /api/toolset` — the manifest with per-entry health
- `POST   /api/toolset` — install one tool (runs to completion)
- `DELETE /api/toolset/<tool>` — uninstall + manifest removal

## Security notes

- **No shell, ever.** Installers run as argv arrays built from validated
  fields. The displayed command and the executed command come from the same
  function.
- **Downloads are pinned.** `binary` and `archive` require https and a matching
  sha256; a mismatch is discarded. Archive extraction rejects absolute paths,
  `..` traversal, and symlinks pointing outside the extraction directory, and
  caps how much it will unpack.
- **Reserved names.** See above — the shared PATH is why this list exists.
- **Package installers run lifecycle scripts as the daemon user.**
  `npm`/`uv`/`go`/`cargo` can execute package code during install; the toolset
  directory confines the *artifacts*, not the installer process. This is a
  documented trade-off, and a wider one than under the per-agent model, since
  the result is shared. The checksum-pinned `binary` and `archive` installers
  are the stricter path.
- Secrets never appear in install fields.

## Limitations

- No automatic upgrades, and no disk quotas.
- No process sandboxing for installers.
- `.tar.xz` archives are not supported (no xz decoder in the Go standard
  library).
- `go` and `cargo` must already exist; only `uv` is bootstrapped.
