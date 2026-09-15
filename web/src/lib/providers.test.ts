import { describe, expect, it, vi } from "vitest";

// providers.ts imports the logo Svelte components purely for display; this
// suite only exercises the plain-TS lookup logic, and the .svelte files
// aren't transformable under vitest.config.ts's plain node environment (no
// svelte plugin registered there — see capabilities.test.ts's ./providers
// mock for the same reason).
vi.mock("./logos/ClaudeLogo.svelte", () => ({ default: {} }));
vi.mock("./logos/CodexLogo.svelte", () => ({ default: {} }));

import { DEFAULT_PROVIDER, PROVIDERS, isProvider, providerMeta, questionEndsTurn } from "./providers";

describe("isProvider", () => {
  it("is true for every known provider id", () => {
    for (const p of PROVIDERS) {
      expect(isProvider(p.id)).toBe(true);
    }
  });

  it("is false for an unknown id", () => {
    expect(isProvider("nope")).toBe(false);
  });
});

describe("providerMeta", () => {
  it("returns the matching entry for a known id", () => {
    expect(providerMeta("codex")).toEqual(PROVIDERS.find((p) => p.id === "codex"));
  });

  it("falls back to PROVIDERS[0] for an unknown id", () => {
    // isProvider("nope") is false, but providerMeta("nope") still returns a
    // real entry — that disagreement is the documented, load-bearing fallback.
    expect(providerMeta("nope")).toEqual(PROVIDERS[0]);
  });

  it("falls back to PROVIDERS[0] for null", () => {
    expect(providerMeta(null)).toEqual(PROVIDERS[0]);
  });

  it("falls back to PROVIDERS[0] for undefined", () => {
    expect(providerMeta(undefined)).toEqual(PROVIDERS[0]);
  });

  it("PROVIDERS[0] matches DEFAULT_PROVIDER", () => {
    expect(PROVIDERS[0].id).toBe(DEFAULT_PROVIDER);
  });
});

describe("questionEndsTurn", () => {
  it("reads the flag through providerMeta for a known provider", () => {
    expect(questionEndsTurn("claude")).toBe(true);
    expect(questionEndsTurn("codex")).toBe(false);
  });

  it("inherits the PROVIDERS[0] fallback for an unknown provider", () => {
    expect(questionEndsTurn("nope")).toBe(PROVIDERS[0].questionEndsTurn);
  });

  it("inherits the PROVIDERS[0] fallback for null/undefined", () => {
    expect(questionEndsTurn(null)).toBe(PROVIDERS[0].questionEndsTurn);
    expect(questionEndsTurn(undefined)).toBe(PROVIDERS[0].questionEndsTurn);
  });
});