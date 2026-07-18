# AGENTS.md

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

## 5. Podiom Provider Architecture (project boundary)

**Provider identity lives in registries. Never branch on `"claude"`/`"codex"` outside them.**

Podiom supports multiple AI-agent providers behind a deliberate seam:

- **Behavior** (spawning, protocol, events, permissions, credentials) lives in
  `internal/adapter/` behind the `Adapter` interface, dispatched by `Router`.
- **Identity & metadata** live in the registry `internal/config/provider.go`
  (`ProviderInfo`) and, for the UI, `web/src/lib/providers.ts` (`PROVIDERS`).
- Behavior that can't live in config uses small per-layer tables keyed by
  provider: `usage.usageProviders`, `providercheck.authProbes`,
  `mcp.nativeImports`, `skills.nativeRoots`.

Rules when changing code:

- Need a per-provider value (label, dir, color, model list, window keys,
  question semantics)? Add a field to the registry — do not write
  `if provider == "codex"` at the call site.
- Need per-provider behavior in a subsystem? Add or extend that subsystem's
  table — do not switch on provider constants in shared code.
- Adding a provider? Follow the checklist in the doc comment on
  `config.providerInfos` (one adapter + wiring block + registry entries).
- Do not re-add provider CHECK constraints to the SQLite schema; validity is
  enforced in Go via `config.KnownProvider` (migration 25 dropped them).

This boundary is enforced: `TestProviderKnowledgeStaysInRegistry`
(`internal/config/provider_drift_test.go`) fails the build when provider
literals appear outside the sanctioned locations. If it fires on your change,
route the logic through a registry — extending its allowlist is correct only
for a genuinely new sanctioned location, not for a convenient branch.

Details: `docs/integrations/README.md` ("Adding a provider").

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.