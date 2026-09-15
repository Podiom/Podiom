import { describe, expect, it, vi } from "vitest";

vi.mock("./auth.svelte", () => ({
  auth: { hydrate: vi.fn(), setToken: vi.fn(), invalidate: vi.fn() },
  readStoredToken: vi.fn(),
  TOKEN_HEADER: "x-podiom-token",
}));

import { normalizeAddress } from "./connection";

describe("normalizeAddress", () => {
  it("adds http to bare hosts", () => {
    expect(normalizeAddress("podiom.local").toString()).toBe("http://podiom.local/");
  });

  it("keeps explicit schemes", () => {
    expect(normalizeAddress("https://podiom.local").toString()).toBe("https://podiom.local/");
  });

  it("preserves a reverse proxy directory", () => {
    expect(normalizeAddress("http://podiom.local/podiom").toString()).toBe("http://podiom.local/podiom/");
  });

  it("trims input and removes query/hash", () => {
    expect(normalizeAddress("  https://podiom.local/path?x=1#token  ").toString()).toBe(
      "https://podiom.local/path/",
    );
  });

  it("rejects an empty address", () => {
    expect(() => normalizeAddress("   ")).toThrow("empty address");
  });
});
