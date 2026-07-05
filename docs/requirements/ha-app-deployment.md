# Podiom Home Assistant App — Requirements

*Standalone implementation spec for deploying Podiom as a Home Assistant app
(add-on). Self-contained: a developer can implement from this document without
reading the full Podiom requirements. Cross-references to the main doc (e.g.
Principle 7, D2, D5, D6, §7.6) and to the skills/MCP specs are for context only.*

Status: v1.2 — ready for implementation.

> Revision v1.2: Home Assistant installs now use a HA-only pre-dashboard
> onboarding page with an embedded terminal. The page guides CLI login,
> `podiom onboard`, gateway-token copy, and then opens the dashboard. Completed
> installs skip onboarding on later visits, and HA dashboards expose a
> persistent Terminal sidebar item. Token rotation remains an add-on
> Configuration action.

> Naming: the project is **Podiom** (`$PODIOM_HOME`, `~/.podiom/`). Home
> Assistant recently renamed "add-ons" to "apps"; this document uses **app**,
> with "(add-on)" where the underlying HA mechanism still uses that term.

---

## 1. Purpose & philosophy

Podiom currently installs standalone via a bash script. This feature adds a
**second, additive installation method**: Podiom as a Home Assistant app. The
bash-script path remains unchanged and fully supported.

**Why HA at all:** Home Assistant can expose the app to the internet in a safe,
already-solved fashion. If the user's HA is internet-exposed (e.g. via Nabu Casa
cloud), the Podiom web UI becomes reachable from their phone outside the LAN —
with HA handling TLS, remote routing, and user authentication. Podiom itself
opens no ports to the internet.

Guiding principles:

1. **Packaging, not rewrite.** The core was built deployment-target-neutral
   (main Principle 7): `PODIOM_HOME` is overridable, the web bind is
   configurable, and the daemon assumes nothing about how it was started. The HA
   app is that investment paying off — a container and manifest around the same
   binaries.
2. **Two auth layers, one primitive.** HA Ingress authenticates the
   *browser → HA* hop. A **Podiom gateway token** authenticates the
   *client → podiomd* hop. The token is not an HA-specific bolt-on: it is the
   forward-compatible primitive that also enables the future remote mode (§2.2)
   and safe LAN exposure of standalone installs.
3. **v1 is embedded; remote is designed-for, not built.** In v1 the full Podiom
   stack runs inside the app container. A future version lets the app point at a
   `podiomd` elsewhere on the LAN (e.g. a dedicated LLM server). Nothing in v1
   may preclude that.

---

## 2. Deployment model

### 2.1 v1 — embedded mode
- **HA1** The app container runs the **full Podiom stack**: `podiomd` (daemon +
  embedded web UI), the `podiom` CLI, the **`claude` and `codex` CLIs** (with a
  Node runtime, since both are npm-distributed), **`mcp-proxy`** (required by
  the MCP spec's bridging — on the host it is assumed present; in the container
  it must be bundled), and a **web terminal (`ttyd`)** (§7).
- **HA2** The app is distributed and versioned as a normal HA app; installing it
  is an alternative to the bash script, not a replacement (the script remains
  the standalone method).

### 2.2 Future — remote mode (out of scope v1, must not be precluded)
- **HA3** A future app version offers a mode choice: **`embedded`** (as v1) or
  **`remote`** — the app acts as a thin Ingress-fronted proxy forwarding all
  HTTP + WebSocket traffic to a `podiomd` on another LAN host (e.g. the user's
  LLM server), authenticated with that server's **gateway token**. This is how a
  user runs Podiom on big hardware while still getting HA's safe internet
  exposure on their phone.
- **HA4** v1 requirements that keep HA3 possible: the web UI is served by
  `podiomd` (so a proxy can front it); all client↔daemon traffic authenticates
  with the gateway token (so the remote hop — which Ingress never sees — is
  covered); and the SPA builds its API/WS URLs relative to its own origin/path
  (so it works equally behind the embedded server or a proxy). (§3, §4.)

---

## 3. Authentication model (answers "do we need Podiom auth?")

Short answer: **HA's auth covers the browser→HA hop via Ingress; it does not
cover the client→podiomd hop.** Podiom therefore adds a gateway token. Two
layers:

### 3.1 Layer 1 — HA Ingress (browser → HA)
- **HA5** The app uses **Ingress** (`ingress: true`). HA handles user
  authentication (including HA MFA) and the secure connection; this works with
  Nabu Casa's Remote UI without any port forwarding. Podiom appears in the HA
  sidebar (panel icon + title).
- **HA6** Per HA's Ingress security requirements, the app's web server accepts
  connections **only from the Ingress proxy address (`172.30.32.2`)** and denies
  all other sources. No direct port is exposed by default in v1 (no `ports`
  mapping enabled).

### 3.2 Layer 2 — Podiom gateway token (client → podiomd)
- **HA7** `podiomd` gains a **gateway token**: a cryptographically random secret
  that **all API and WebSocket connections must present** (HTTP header on API
  calls; on WS, a token in the connection handshake). This is a core `podiomd`
  capability introduced by this feature; it applies to both deployment methods
  (HA app and standalone).
- **HA8 — Lifecycle (decided).** The token is **auto-generated by `podiomd` on
  first start** and stored under `$PODIOM_HOME`. No user-chosen tokens —
  auto-generation guarantees strength. Retrieval and rotation differ by
  deployment method:
  - **Standalone:** via the CLI commands **`podiom token show`** and
    **`podiom token rotate`**, run in any shell.
  - **HA app:** after `podiom onboard` completes, a narrow HA-only
    pre-dashboard surface exposes a one-time browser copy/store step. Rotation
    remains available through the add-on Configuration page's `rotate_token`
    toggle. The CLI commands remain available inside the container for parity.

  The invariant is that the token appears only on an HA-authenticated,
  actively-read surface or in an explicit CLI command, and never in the app log
  (HA21).
- **HA9 — Client behaviours.** The `podiom` CLI reads the token from
  `$PODIOM_HOME` automatically (zero friction — same machine, same trust
  domain). **Browsers** store it once per browser. In HA mode the first-run
  copy step stores it; in standalone the token screen verifies and stores a
  value shown by `podiom token show`.
- **HA10 — Unauthenticated surface is minimal.** Static SPA assets and the
  token-entry view are served without the token; **every API/WS endpoint
  requires it**, except the HA-only onboarding state/token-copy endpoints used
  before the dashboard. Those endpoints are unavailable outside HA mode, and
  the token-copy endpoint is only available after onboarding is complete.
  Nothing about sessions, agents, plans, or memory is reachable pre-token. In
  standalone, the token-entry view points to `podiom token show`.
- **HA11 — Why a token even behind Ingress.** (a) Defense in depth — Ingress
  authenticates *who reaches HA*, the token authenticates *who may operate
  Podiom*; (b) it is **the** auth primitive for future remote mode (HA3), where
  the app→remote-podiomd hop is raw LAN traffic Ingress never touches; (c) it
  lets standalone installs safely bind beyond loopback (phone on home LAN)
  without inventing auth later.
- **HA12 — Rotation invalidates.** Rotating the token invalidates prior tokens;
  connected clients must re-authenticate. Browsers are prompted for the new
  token; the CLI picks it up from disk automatically.

---

## 4. Ingress integration (technical requirements)

- **HA13 — WebSocket through Ingress.** Podiom's UI streams over WebSocket;
  Ingress supports WS and this must be verified end-to-end in implementation
  (historical browser-specific caveats exist). Live token streaming, permission
  prompts, and plan-mode updates must all work through the Ingress path,
  including remotely via Nabu Casa.
- **HA14 — SPA under a sub-path (classic pitfall — hard requirement).** Ingress
  serves the app under a rewritten path (`/api/hassio_ingress/<ingress-token>/…`),
  **not** at the origin root. The Svelte/Vite build MUST therefore use
  **relative asset paths** (no absolute `/assets/...`), client-side routing must
  tolerate a base path, and the **WebSocket URL must be derived from the page's
  own location/ingress path** (honouring `X-Ingress-Path` where needed) rather
  than hard-coded. An SPA that assumes it lives at `/` will render broken
  through Ingress — this must be caught by an acceptance check (§11), not
  discovered in production.
- **HA15 — One Ingress endpoint, multiple surfaces under it.** An app has a
  single Ingress entry. Behind it live **two sibling surfaces**: the Podiom SPA
  (served at the Ingress base) and the **web terminal, served on dedicated
  sub-paths *outside* the SPA** — `/{ingress-base}/terminal/claude` and
  `/{ingress-base}/terminal/codex`. These are **not** SPA routes and **not** a
  Terminal view inside the UI (changed from v1.0); they are separate entries the
  app's internal router proxies to `ttyd` in the container. The rationale is
  onboarding intent: each sub-path carries the context (which CLI to
  authenticate) so the link a user opens *is* the flow they need. The SPA and
  the terminal sub-paths must both tolerate the Ingress base path (HA14) — the
  terminal surface derives its own base from the request path just as the SPA
  does.
  **2026-07 update:** dedicated Claude/Codex terminal login entries are
  superseded by onboard-integrated CLI login checks; current terminal surfaces
  are `/{ingress-base}/terminal/onboard` and `/{ingress-base}/terminal/shell`.

---

## 5. Container contents & platforms

- **HA16 — Multi-arch images.** At minimum **amd64** and **aarch64** (HAOS
  commonly runs on Raspberry Pi–class hardware). Go's static binaries and a
  pure-Go SQLite driver (main R3.2/R3.4) make this straightforward; the Node
  runtime for the CLIs must also be present per-arch.
- **HA17 — Resource honesty.** The `claude`/`codex` CLIs are cloud-backed
  clients — LLM compute is remote, so Pi-class hardware is viable. But Podiom
  has **no concurrency cap** (main D5): many parallel agent turns each spawn
  processes and consume RAM. The app documentation must state this trade-off
  for small-board users.
- **HA18 — Bundled versions.** The image pins versions of Podiom, the CLIs,
  `mcp-proxy`, and `ttyd`; app updates ship new images (§8). CLI version drift
  between image releases must be noted in the changelog, since CLI flags Podiom
  depends on (e.g. `--mcp-config`, `--add-dir`, `--profile`) are
  version-sensitive.

---

## 6. Storage & persistence

- **HA19 — Everything persistent lives on `/data`.** The app sets
  **`PODIOM_HOME=/data/podiom`** (this is exactly what the `PODIOM_HOME`
  override exists for). Additionally, the container's **`HOME` is anchored to a
  `/data`-backed path** (e.g. `HOME=/data/home`) so that *all* home-anchored
  state persists across restarts and updates: `~/.agents/skills/`,
  `~/.claude/`, `~/.codex/`, and every profile directory
  (`CLAUDE_CONFIG_DIR`/`CODEX_HOME` dirs). Losing these on update would force
  CLI re-login and skill re-linking every release — unacceptable.
- **HA20 — Free backups.** Because HA app backups include `/data`, the entire
  Podiom state — SQLite (sessions, history, memories' bookkeeping), agents
  (SOUL.md, MEMORY.md), plans, projects, schedules, skills, profiles, CLI auth,
  and the gateway token — is covered by **HA's native backup system** with zero
  Podiom-side work. This should be stated in the app docs as a feature.
- **HA21 — Sensitivity note.** `/data` therefore contains **all profile
  credentials and the gateway token**. App documentation must recommend
  password-protected HA backups, and Podiom must never print token or credential
  values into the **app log** (log the token's existence/rotation events, never
  the value). The value's retrieval paths are the HA first-run copy step and
  `podiom token show` — deliberately *not* the app log.

---

## 7. CLI authentication (web terminal on dedicated sub-paths)

- **HA22** The app bundles a **single shared `ttyd`** process, reached through
  **dedicated terminal sub-paths** (HA15):
  `/{ingress-base}/terminal/claude`, `/{ingress-base}/terminal/codex`,
  `/{ingress-base}/terminal/onboard`, and `/{ingress-base}/terminal/shell`.
  Claude/Codex entries drop the user straight into the right CLI's login flow;
  the onboarding entry runs the shared `podiom onboard` wizard; the shell entry
  is for later maintenance. CLI login itself is a device-style flow that prints
  a URL the user opens **in their own browser** and pastes a code back, which
  works fully over a web terminal.
  - **Auto-run then drop to shell (required).** Each entry runs a small
    **wrapper script** that (a) executes the correct login command
    (`claude /login` / `codex login --device-auth`) and, (b) on completion, **drops to an
    interactive shell** rather than exiting. Exiting on completion would kill
    the `ttyd` session and leave the user at a dead terminal; the drop-to-shell
    also lets per-profile logins (HA23) continue in the same session.
  - **Manual hand-off back to Podiom (decided).** After a successful login the
    wrapper **prints a link back to the Podiom SPA** (the Ingress base). There
    is no automatic auth-completion detection or redirect — reliably detecting
    "login done" from outside the CLI process is brittle, and a manual link is
    robust. If a chrome around the terminal is used, the link may instead be
    rendered by the thin HTML page `ttyd` serves around the terminal; either
    placement satisfies this requirement.
  **2026-07 update:** Claude/Codex login now happens inside `podiom onboard`;
  the wrapper dispatches only `onboard` and `shell`.
- **HA23 — Profiles supported, selectable on the entry.** Per-profile logins
  work the same way: `CLAUDE_CONFIG_DIR=<profile-dir> claude /login` (and the
  Codex equivalent), once per profile — repeatable, matching the profile model.
  The onboarding entry **can target a specific profile**, so the same link is
  reused both for first login and for adding a profile later.
  **Implementation note:** because `ttyd` maps a start command **per path**
  (not per query-string), the profile selector must be expressed as a
  **path segment** (`/terminal/claude/<profile>`) or resolved by the wrapper
  script from an arg/env — **not** as a `?profile=…` query parameter, which
  `ttyd` would not route to a distinct command. The requirement is stated
  transport-neutrally; either mechanism is acceptable so long as the entry lands
  in a login flow scoped to the named profile dir.
  **2026-07 update:** profile-scoped terminal login entries are superseded by
  onboard-integrated login checks; later profile re-authentication is manual in
  Shell with `CLAUDE_CONFIG_DIR=<dir>` or `CODEX_HOME=<dir>`.
- **HA24 — The terminal is Podiom-root (honest note).** Whoever reaches a
  terminal sub-path reaches the whole container: `$PODIOM_HOME`, every profile's
  credentials, and the gateway token. Ingress gates these sub-paths behind HA
  login exactly as it gates the SPA, which means **the HA account's security IS
  the Podiom container's security**. Moving the terminal to its own sub-paths
  (outside the SPA) does not change this — the sub-paths sit behind the same
  single Ingress entry and inherit the same HA authentication. This is
  consistent with Podiom's single-user, fully-trusted model, but it must be
  stated plainly in the app documentation (same honesty principle as the yolo
  posture in the main doc), with a recommendation to enable HA MFA.

---

## 8. Lifecycle (boot, supervision, updates)

- **HA25 — HA supervision solves deferred boot-persistence.** The app is
  configured for **start-on-boot** with the **watchdog** enabled: HA starts
  `podiomd` at boot and restarts it on crash. This de-facto resolves, in HA
  mode, the standalone limitation that schedules and dreaming only fire while
  `podiomd` happens to be running (main §7.6 / the deferred
  `podiom service install`): under HA, the scheduler and nightly dreaming are
  reliably supervised. State this as an explicit benefit of the HA deployment.
- **HA26 — Updates.** New Podiom versions ship as new app versions through the
  app store mechanism; `/data` persists across updates (HA19), so sessions,
  memory, auth, and the token survive. The dream catch-up (memory spec MEM8) and
  durable session state (main D6) make restarts non-lossy by design.

---

## 9. Distribution & installation

- **HA27** The app is distributed via a **Podiom app repository** (a public Git
  repository with the standard HA repository manifest) that users add to their
  HA app store, plus pre-built multi-arch images in a container registry. The
  app manifest (`config.yaml`) declares at minimum: `ingress: true`,
  `ingress_port`, panel icon/title, `startup`/watchdog settings, the `/data`
  mapping, and a `rotate_token` boolean toggle (HA8), which `podiomd` reads via
  the Supervisor API for token rotation.
- **HA28** First-run experience: on first start the app generates the gateway
  token (HA8) and opens a HA-only onboarding page before the dashboard. The
  page embeds the terminal, lets the user run Claude/Codex login flows (default
  or profile-scoped), runs `podiom onboard`, then shows a HA-authenticated
  gateway-token copy step and a **Finished** button. Onboarding completion is
  persisted in `$PODIOM_HOME/onboarding.json`; future visits open the dashboard
  directly. HA dashboards also show a **Terminal** sidebar item for later
  re-authentication or shell access.

---

## 10. Out of scope (v1) / future

- **Remote mode** (app pointing at a `podiomd` on another LAN host) — designed
  for (HA3/HA4), not built.
- **Direct port exposure** of `podiomd` from the app (LAN access bypassing HA) —
  Ingress-only in v1; the gateway token makes adding an opt-in port later safe.
- **Multi-user semantics** — Ingress can pass HA user identity headers to the
  app; Podiom ignores them in v1 (single-user model). A future multi-user Podiom
  could consume them.
- **HA integration surface** (entities, services, automations triggering Podiom
  agents — e.g. an HA automation starting a scheduled agent run) — a natural and
  attractive future step, deliberately excluded from this packaging-focused v1.
- **Token-per-client / scoped tokens** — v1 has one gateway token; finer-grained
  client tokens are future work.

---

## 11. Acceptance checks

A correct implementation satisfies all of:

1. Installing the app from the repository on both amd64 and aarch64 yields a
   running `podiomd` with the UI reachable via the HA sidebar through Ingress
   (HA5, HA16, HA27).
2. The app's web server rejects connections not originating from the Ingress
   proxy; no direct port is exposed (HA6).
3. All API/WS calls without the gateway token are rejected, except the HA-only
   onboarding bootstrap endpoints; static assets and the first-run HA
   onboarding page load without it; sessions, agents, plans, and memory do not
   (HA7, HA10).
4. The token is auto-generated on first start. In the HA app the token-copy
   surface is available only after onboarding completes; toggling
   `rotate_token` on the Configuration page rotates it, forcing browser
   re-entry while the CLI recovers automatically from disk. In standalone,
   `podiom token show`/`rotate` behave equivalently (HA8, HA9, HA12).
5. The full UI — including live WebSocket streaming, permission prompts, and
   plan-mode rendering — works through Ingress **and** remotely via Nabu Casa
   (HA13).
6. The SPA renders and connects correctly under the Ingress sub-path (no
   absolute-path breakage; WS URL derived from the ingress path) (HA14).
7. The `terminal/onboard` and `terminal/shell` sub-paths (behind the single
   Ingress entry) open the shared `ttyd` directly into the right flow, drop to
   a shell when appropriate, and print a working link back to the Podiom SPA;
   provider login is checked and launched by the onboard wizard, while later
   manual `claude /login` and `codex login --device-auth` work from Shell
   (HA15, HA22, HA23 superseded by onboard-integrated logins).
8. Restarting the app and updating it to a new version preserves: sessions,
   agent SOUL/MEMORY files, skills links, profiles' CLI auth, and the gateway
   token (`/data`-anchored `PODIOM_HOME` and `HOME`) (HA19, HA26).
9. An HA backup restored onto a fresh install brings back the complete Podiom
   state (HA20).
10. Rebooting the HA host brings `podiomd` back automatically; a schedule due
    after the reboot fires, and a missed dream catches up (HA25).
11. Neither the gateway token value nor any profile credential ever appears in
    the app log (HA21).
12. Fresh HA install opens the onboarding page, lets the user authenticate
    Claude/Codex, run `podiom onboard`, copy/store the gateway token, and enter
    the dashboard. Reopening the app after completion lands directly in the
    dashboard, with a HA-only Terminal sidebar item available (HA8, HA10,
    HA22, HA28).
