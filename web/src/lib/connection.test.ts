import { describe, expect, it, vi } from "vitest";

vi.mock("./auth.svelte", () => ({
  auth: { hydrate: vi.fn(), setToken: vi.fn(), invalidate: vi.fn() },
  readStoredToken: vi.fn(),
  TOKEN_HEADER: "x-podiom-token",
}));

import { normalizeAddress, probe } from "./connection";

describe("normalizeAddress", () => {
  it("adds http to bare hosts", () => {
    expect(normalizeAddress("podiom.local").toString()).toBe("http://podiom.local/");
  });

  it("adds http to a bare host:port", () => {
    expect(normalizeAddress("192.168.1.20:8080").toString()).toBe("http://192.168.1.20:8080/");
    expect(normalizeAddress("podiom.local:8080").toString()).toBe("http://podiom.local:8080/");
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

describe("probe", () => {
  const address = new URL("https://podiom.local/base/");
  const health = (body: unknown = { status: "ok", version: "1.2.3" }) => ({
    ok: true,
    json: vi.fn().mockResolvedValue(body),
  });

  it.each([
    ["bad status", { ok: false }, "not-podiom"],
    ["wrong body", health({ status: "bad", version: "1" }), "not-podiom"],
    ["missing version", health({ status: "ok" }), "not-podiom"],
  ])("reports %s", async (_name, response, reason) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));
    await expect(probe(address, "token")).resolves.toEqual({ ok: false, reason });
  });

  it("treats network and invalid JSON errors separately", async () => {
    const fetch = vi.fn().mockRejectedValueOnce(new Error("offline"));
    vi.stubGlobal("fetch", fetch);
    await expect(probe(address, "token")).resolves.toEqual({ ok: false, reason: "unreachable" });
    expect(fetch).toHaveBeenCalledTimes(1);

    fetch.mockReset().mockResolvedValue({ ok: true, json: vi.fn().mockRejectedValue(new Error("html")) });
    await expect(probe(address, "token")).resolves.toEqual({ ok: false, reason: "not-podiom" });
  });

  it.each([
    [{ ok: false, status: 401 }, "token-rejected"],
    [{ ok: false, status: 500 }, "unreachable"],
  ])("maps auth response %#o to %s", async (auth, reason) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(health()).mockResolvedValueOnce(auth));
    await expect(probe(address, "secret")).resolves.toEqual({ ok: false, reason });
  });

  it("returns the version after both checks succeed", async () => {
    const fetch = vi.fn().mockResolvedValueOnce(health()).mockResolvedValueOnce({ ok: true, status: 200 });
    vi.stubGlobal("fetch", fetch);
    await expect(probe(address, "secret")).resolves.toEqual({ ok: true, version: "1.2.3" });
    expect(fetch.mock.calls[0][0].href).toBe("https://podiom.local/base/healthz");
    expect(fetch.mock.calls[1][0].href).toBe("https://podiom.local/base/api/auth/check");
    expect(fetch.mock.calls[1][1].headers).toEqual({ "x-podiom-token": "secret" });
  });
});
