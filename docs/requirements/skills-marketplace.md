# Podiom — Skill Search & Installation

**Specification 07 — Requirements Document**
Status: Draft · Author: Marcus Schmidt · Date: 2026-07-05

---

## 1. Overview

Podiom agents (Claude Code, Codex CLI, and future providers) support the open Agent Skills standard (`SKILL.md`, agentskills.io). Today, skills must be placed manually on disk. This feature adds a **skill marketplace experience to the Podiom dashboard**: users can search public skill registries, inspect a skill before installing, and install it with one click into the shared skills directory.

All installed skills land in `~/.agents/skills/`, which is the directory shared by all Podiom agents regardless of provider. A skill installed once is available to every agent.

### 1.1 Goals

- Give users access to the largest possible catalog of skills out of the box, without configuration.
- Make installation a one-click operation with predictable, inspectable results on disk.
- Treat third-party skills as untrusted input: the user must be able to see exactly what will be installed before it runs anywhere near an agent.

### 1.2 Non-goals (out of scope for v1)

- Authoring or editing skills in the dashboard.
- Publishing skills to external registries.
- Per-agent skill scoping (all skills are global in v1; per-agent enable/disable is a candidate for v2).
- Automatic background updates of installed skills.
- Paid/commercial skill distribution.

---

## 2. Terminology

| Term | Meaning |
|---|---|
| **Skill** | A directory containing a `SKILL.md` manifest (YAML frontmatter + Markdown body) and optionally `scripts/`, `references/`, `assets/`. |
| **Registry** | An external index of skills exposing search and metadata (e.g. SkillsMP, skills.sh). |
| **Source** | Where a skill's files actually live — almost always a GitHub repository (registries index GitHub, they don't host files). |
| **Skills directory** | `~/.agents/skills/` on the Podiom host. One subdirectory per installed skill. |
| **Lockfile** | Podiom's local record of installed skills: `~/.agents/skills/.podiom-lock.json`. |

---

## 3. Skill Sources

### 3.1 Source strategy

Podiom MUST implement a **source abstraction** (`SkillSource` interface in the Go backend) so registries can be added, removed, or reprioritized without touching search or install logic. v1 ships with three sources:

| Priority | Source | Type | Rationale |
|---|---|---|---|
| 1 | **SkillsMP** (skillsmp.com) | Registry, REST API | Largest public catalog (2M+ indexed skills), documented REST API with keyword search and pagination. Primary search backend. |
| 2 | **anthropics/skills** (GitHub) | Curated repo | Official Anthropic skills. Small but verified and high quality. Indexed directly via the GitHub API and flagged **Verified** in the UI. |
| 3 | **Direct GitHub URL** | Escape hatch | User pastes any GitHub repo/subdirectory URL containing a `SKILL.md`. Covers anything not indexed anywhere. |

Candidates for later versions (behind the same abstraction): skills.sh, ClawHub, LobeHub, SkillsDirectory, private/company-internal registries.

### 3.2 Source requirements

- **SRC-1 (MUST):** Each source implements: `Search(query, page) → []SkillSummary`, `Fetch(id) → SkillDetail`, `Download(id) → archive/tree`.
- **SRC-2 (MUST):** Search results from multiple sources are merged and deduplicated by canonical GitHub location (`owner/repo/path`). When the same skill appears in several registries, the highest-priority source wins for metadata display.
- **SRC-3 (MUST):** Registry outages degrade gracefully: if SkillsMP is unreachable, search still returns results from remaining sources, with a non-blocking warning in the UI.
- **SRC-4 (SHOULD):** Registry responses are cached server-side (suggested TTL: 15 min for search, 24 h for skill detail) to respect rate limits and keep the UI snappy.
- **SRC-5 (MAY):** Users can toggle individual sources on/off in Podiom settings.

---

## 4. Functional Requirements

### 4.1 Search & discovery

- **FR-1 (MUST):** The dashboard has a **Skills** section with a search field. Free-text search queries all enabled sources and returns a merged, ranked result list.
- **FR-2 (MUST):** Each result card shows: skill name, one-line description, author/owner, source badge (SkillsMP / Verified / GitHub), popularity signal (stars or installs where available), and last-updated date.
- **FR-3 (MUST):** Results are paginated or infinitely scrolled; the UI never blocks on slow sources (results stream in per source).
- **FR-4 (SHOULD):** Filtering by source, and sorting by relevance / popularity / recency.
- **FR-5 (SHOULD):** A curated "Featured" tab showing anthropics/skills content when the search field is empty, so the section isn't blank on first visit.
- **FR-6 (MAY):** Tag/category browsing where the registry exposes tags.

### 4.2 Skill detail view

- **FR-7 (MUST):** Clicking a result opens a detail view showing: rendered `SKILL.md` (frontmatter as a metadata table, body as rendered Markdown), file tree of the full skill directory, source link to the GitHub location, and license if detectable.
- **FR-8 (MUST):** The file tree lets the user open and read **every file** in the skill (scripts included) before installing. No file in the skill may be hidden from pre-install inspection.
- **FR-9 (MUST):** The detail view clearly indicates whether the skill contains executable content (`scripts/`, shell/Python/Node files) via a visible badge, e.g. "Contains executable scripts".
- **FR-10 (SHOULD):** Show declared frontmatter fields relevant to trust: `allowed-tools`, declared dependencies, and any tool-specific metadata.

### 4.3 Installation

- **FR-11 (MUST):** An **Install** button on both result cards and the detail view. Installation:
  1. Downloads the skill's directory tree from its GitHub source (specific commit SHA, not a moving branch ref).
  2. Validates it (see FR-14).
  3. Writes it to `~/.agents/skills/<skill-name>/`.
  4. Records the install in the lockfile.
- **FR-12 (MUST):** Directory naming: the `name` field from `SKILL.md` frontmatter, normalized to lowercase kebab-case. On collision with an existing directory **not** managed by Podiom, abort with a clear error. On collision with a Podiom-managed skill, treat as update/reinstall (FR-17).
- **FR-13 (MUST):** Installation is atomic: download and validate into a temp directory, then move into place. A failed install never leaves a partial skill in `~/.agents/skills/`.
- **FR-14 (MUST):** Pre-install validation:
  - `SKILL.md` exists at the skill root and has parseable YAML frontmatter with at least `name` and `description`.
  - Total size below a configurable cap (default: 50 MB).
  - No path traversal in the file tree (`..`, absolute paths, symlinks pointing outside the skill directory are rejected).
- **FR-15 (MUST):** The lockfile records per skill: name, source registry, canonical GitHub location, pinned commit SHA, install timestamp, and Podiom version. This distinguishes Podiom-managed skills from manually placed ones.
- **FR-16 (MUST):** Manually placed skills in `~/.agents/skills/` are **never** modified or deleted by Podiom. They appear in the Installed list marked "Unmanaged".

### 4.4 Managing installed skills

- **FR-17 (MUST):** An **Installed** tab listing all skills in `~/.agents/skills/` (managed and unmanaged), with name, description, source, installed version (SHA, short form), and install date.
- **FR-18 (MUST):** Uninstall (managed skills only): removes the skill directory and its lockfile entry, after a confirmation dialog.
- **FR-19 (SHOULD):** Update check: compare the pinned SHA against the source's current HEAD for the skill path. If newer, show an "Update available" indicator; updating shows a diff summary (changed files) before applying. Updates are always user-initiated, never automatic.
- **FR-20 (SHOULD):** Search results indicate when a skill is already installed (button becomes "Installed" / "Update").
- **FR-21 (MAY):** Export/import of the lockfile for reproducing a skill set on another Podiom host.

### 4.5 Direct URL install

- **FR-22 (MUST):** An "Install from GitHub URL" action accepting: repo root URLs, subdirectory URLs, and URLs pointing at a `SKILL.md`. Podiom resolves the skill root, then follows the same detail-view → validate → install flow as registry installs. Direct installs never skip the detail/inspection view.
- **FR-23 (SHOULD):** If a repo contains multiple skills (e.g. a `skills/` monorepo), list them and let the user pick which to install.

---

## 5. Security Requirements

Skills are untrusted third-party code **and** untrusted prompt content. `SKILL.md` is operational text injected into agent context — a malicious description or body can steer agent behavior (prompt injection), and bundled scripts execute with the agent's privileges. Ecosystem audits have found vulnerability rates around 26 % in open marketplaces. Security is therefore a first-class requirement, not hardening-later.

- **SEC-1 (MUST):** Install always requires an explicit user action. Agents themselves MUST NOT be able to trigger skill installation through the Podiom API without user confirmation in the dashboard.
- **SEC-2 (MUST):** The install confirmation for any skill containing executable files shows a prominent warning and requires the user to acknowledge it ("This skill contains scripts that agents may execute").
- **SEC-3 (MUST):** Installs are pinned to a commit SHA. A registry or repo owner cannot silently change what an already-installed skill contains.
- **SEC-4 (MUST):** Downloaded content is written with user-only permissions and never executed during install. Validation is static only.
- **SEC-5 (MUST):** Reject skills failing path-traversal / symlink validation (FR-14) with no partial write.
- **SEC-6 (SHOULD):** A lightweight static heuristic scan at install time flagging: network calls in scripts, `curl | sh` patterns, base64 blobs, credential-file paths (`~/.ssh`, `~/.aws`, keychain access), and instructions in `SKILL.md` that direct the agent to exfiltrate data or fetch remote instructions. Findings are shown as warnings — the user decides, Podiom informs.
- **SEC-7 (SHOULD):** Display registry-provided trust signals where available (scanner verdicts, verified badges) but never present them as a guarantee.
- **SEC-8 (MAY):** A settings-level "curated only" mode restricting installs to Verified sources.

---

## 6. Backend API (Podiom server)

All endpoints under the existing Podiom API namespace, dashboard-authenticated.

| Method & path | Purpose |
|---|---|
| `GET  /api/skills/search?q=&source=&page=` | Merged registry search (FR-1–FR-4) |
| `GET  /api/skills/detail?source=&id=` | Full skill detail incl. file tree (FR-7) |
| `GET  /api/skills/detail/file?source=&id=&path=` | Raw file content for inspection (FR-8) |
| `POST /api/skills/install` | Install by source+id or GitHub URL (FR-11, FR-22) |
| `GET  /api/skills/installed` | Installed list, managed + unmanaged (FR-17) |
| `DELETE /api/skills/installed/{name}` | Uninstall (FR-18) |
| `POST /api/skills/installed/{name}/update` | Check/apply update (FR-19) |

- **API-1 (MUST):** `POST /api/skills/install` is synchronous for small skills but returns a job ID for larger downloads; the dashboard polls or subscribes for progress.
- **API-2 (MUST):** All registry traffic goes through the Podiom backend. The Svelte frontend never calls SkillsMP/GitHub directly (keeps tokens server-side, enables caching, avoids CORS).
- **API-3 (SHOULD):** Optional GitHub token in Podiom config to raise API rate limits; anonymous access must still work.

---

## 7. Non-functional Requirements

- **NFR-1:** Search results begin rendering within 1 s on a warm cache; cold multi-source search completes within 5 s or streams partials.
- **NFR-2:** Install of a typical skill (< 1 MB) completes within 5 s on a normal connection.
- **NFR-3:** All filesystem operations respect the Podiom host user's permissions; no elevation.
- **NFR-4:** The feature works fully offline for the Installed view (search obviously requires network).
- **NFR-5:** Registry API keys/tokens, if any, live in Podiom server config — never in the frontend bundle.

---

## 8. UX Notes (Svelte dashboard)

- New top-level nav item: **Skills**, with tabs **Discover** / **Installed**.
- Discover: search bar, source filter chips, streamed result cards, Featured content on empty query.
- Detail: two-pane layout — rendered `SKILL.md` left, file tree + file viewer right. Install button pinned, executable-content badge adjacent to it.
- Installed: table with update indicators; uninstall behind a confirm dialog; unmanaged skills visually distinct (muted badge, no uninstall button).

---

## 9. Open Questions

1. **Per-agent scoping (v2):** should the lockfile grow an `enabledFor` field now to avoid a migration later, even if v1 UI ignores it?
2. **SkillsMP API terms:** confirm rate limits and ToS for embedding their catalog in a third-party dashboard before shipping.
3. **Home Assistant deployment:** does `~/.agents/skills` resolve sensibly in the HA add-on container, or does that deployment need a volume-mapped path? (Cross-reference Spec 06.)
4. **Skill name collisions across registries:** kebab-case normalization can collide for genuinely different skills — is suffixing with owner (`<owner>--<name>`) acceptable as fallback?

---

*End of document.*
