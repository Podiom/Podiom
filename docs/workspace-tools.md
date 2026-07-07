# Workspace tools

Agents can ask for command-line tools they are missing, and — on your approval —
Podiom installs the tool **into that agent's own workspace**, never host-wide.
The tool appears on that agent's PATH only, every install is recorded in a
per-agent manifest, and everything is removable from the UI. The full behavior
is specified in
[requirements/workspace-tool-installs.md](requirements/workspace-tool-installs.md).

## How a tool gets installed

1. While working a [goal](goals.md), an agent files a `cli_tool` access request
   naming the executable and a declarative installer payload (see the table
   below), plus a reason you can act on.
2. You get a notification; the request card on the goal shows the **exact
   command that will run** — the displayed command and the executed command are
   built by the same code, and the agent can never author a shell string that
   Podiom executes.
3. Approving starts the install in the background (the request shows
   **installing…**). The grant only succeeds if the expected executable
   actually appears in the agent's tool directories; `tool --version` output is
   captured as evidence on the goal timeline. Failures land on the request with
   the installer's output and can be re-approved after you fix the cause.
4. The agent's next session sees the tool on its PATH.

## Installers

| `installer` | Payload | What runs |
| --- | --- | --- |
| `npm` | `package`, optional `version` | `npm install -g --prefix <tools>/npm <package>@<version>` |
| `uv` | `package`, optional `version` | `uv tool install <package>==<version>` into agent-scoped `UV_TOOL_DIR`/`UV_TOOL_BIN_DIR` |
| `go` | `package` (module path) | `go install <package>@<version>` (default `@latest`) with `GOBIN=<tools>/bin` |
| `binary` | `url` (https only), `sha256` | download → verify the pinned checksum → `<tools>/bin/<tool>` |

A `cli_tool` request **without** installer fields is a host-wide tool (brew,
apt, …): Podiom does not install those. Approving acknowledges the request and
shows you the suggested command to run yourself; the agent re-detects the tool
at its next review.

## Where things live

```text
$PODIOM_HOME/agents/<name>/tools/
  bin/            # binary downloads, go installs, uv shims
  npm/            # npm prefix (executables in npm/bin)
  uv/             # uv tool environments
  manifest.json   # what was installed, by which request, for which goal, when
```

`tools/bin` and `tools/npm/bin` are prepended to the PATH of that agent's
provider subprocesses, so a workspace-installed tool wins over a same-named
host tool **for that agent only**. Deleting the agent removes its tools with
the rest of its directory.

## Inspecting and removing

The agent's detail page (**Agents** → select an agent) lists its workspace
tools with installer, version, install date, and a **broken** badge when the
manifest claims a tool whose executable has gone missing on disk. Remove a
tool from there (or via the API); removal uninstalls it and drops the manifest
entry. Agents can check what they already have with the
`podiom_list_workspace_tools` tool instead of re-requesting.

## Security notes

- **No shell, ever.** Installers run as argv arrays built from the validated
  payload; `install_hint` free text is display-only.
- **Approval is the gate.** Nothing installs without an explicit human
  decision, and the approve dialog names the tool and shows the exact command.
- `binary` downloads require https and a pinned sha256; a mismatched digest is
  discarded.
- Package installers (`npm`/`uv`/`go`) can run package lifecycle scripts as the
  daemon user — the per-agent directory confines the *artifacts*, not the
  installer process. This is a documented v1 trade-off; the checksum-pinned
  `binary` installer is the stricter path. See the spec's §6 for the full
  posture.

## HTTP API

- `GET    /api/agents/<name>/tools` — manifest with per-entry health
- `DELETE /api/agents/<name>/tools/<tool>` — uninstall + manifest removal

Requests are filed and decided through the goals access-request endpoints —
see [goals.md](goals.md).

## Limitations (v1)

- **Codex-backed turns don't see the tools on PATH.** The Codex app-server is
  one long-lived process per profile with a fixed environment; the tools are
  on disk but not injected. Claude-backed sessions get the PATH everywhere
  (chat, tasks, schedules, goal reviews).
- Host-wide installs stay manual (acknowledge-only requests).
- No automatic upgrades, disk quotas, or install sandboxing.
- Each agent owns its copy — tools are not shared between agents.
