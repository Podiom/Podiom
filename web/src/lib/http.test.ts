import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { auth } = vi.hoisted(() => ({
  auth: {
    token: "",
    invalidate: vi.fn(),
  },
}));

vi.mock("./auth.svelte", () => ({
  auth,
  TOKEN_HEADER: "X-Podiom-Token",
}));

vi.mock("./base", () => ({
  apiUrl: (path: string) => new URL(path.replace(/^\//, ""), "http://podiom.test/base/"),
}));

import { request, verifyToken } from "./http";

afterEach(() => {
  vi.unstubAllGlobals();
});

beforeEach(() => {
  auth.token = "";
  vi.clearAllMocks();
});

describe("request", () => {
  it("omits the token header when no token is stored", async () => {
    const response = new Response(null, { status: 200 });
    const fetchMock = vi.fn().mockResolvedValue(response);
    vi.stubGlobal("fetch", fetchMock);

    await expect(request("api/agents")).resolves.toBe(response);

    const [, init] = fetchMock.mock.calls[0];
    expect(new Headers(init?.headers).has("X-Podiom-Token")).toBe(false);
  });

  it("sends the token and invalidates it on an API 401", async () => {
    auth.token = "secret";
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 401 }));
    vi.stubGlobal("fetch", fetchMock);

    await request("/api/agents");

    const [, init] = fetchMock.mock.calls[0];
    expect(new Headers(init?.headers).get("X-Podiom-Token")).toBe("secret");
    expect(auth.invalidate).toHaveBeenCalledOnce();
  });

  it("does not invalidate the token on a non-API 401", async () => {
    auth.token = "secret";
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 401 })));

    await request("healthz");

    expect(auth.invalidate).not.toHaveBeenCalled();
  });
});

describe("verifyToken", () => {
  it.each([
    { ok: true, expected: true },
    { ok: false, expected: false },
  ])("returns $expected for a response with ok=$ok", async ({ ok, expected }) => {
    const fetchMock = vi.fn().mockResolvedValue({ ok });
    vi.stubGlobal("fetch", fetchMock);

    await expect(verifyToken("candidate")).resolves.toBe(expected);
    expect(fetchMock).toHaveBeenCalledWith(
      new URL("api/auth/check", "http://podiom.test/base/"),
      { headers: { "X-Podiom-Token": "candidate" } },
    );
  });

  it("returns false when fetch rejects", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));

    await expect(verifyToken("candidate")).resolves.toBe(false);
  });
});
