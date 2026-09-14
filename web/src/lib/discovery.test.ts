import { afterEach, describe, expect, it, vi } from "vitest";

import { addressFor, available, discover } from "./discovery";
import type { DiscoveredInstance } from "./discovery";

// The web implementation the module registers with registerPlugin for the
// browser build.
interface WebOption {
  web(): {
    discover(options: { timeoutMs: number }): Promise<{ instances: DiscoveredInstance[] }>;
  };
}

// vi.doMock rather than the hoisted vi.mock: this file exercises discovery.ts
// both as it loads in a real browser run — the static import above — and with
// isNative forced on, so the plugin-path tests re-import the module with the
// stubs they need in place.
async function importDiscovery(): Promise<typeof import("./discovery")> {
  vi.resetModules();
  return import("./discovery");
}

afterEach(() => {
  vi.doUnmock("./native");
  vi.doUnmock("@capacitor/core");
  vi.resetModules();
});

describe("addressFor", () => {
  it("renders an IPv4 host as http://host:port", () => {
    expect(addressFor({ name: "Podiom on MacBook", host: "192.168.1.42", port: 8787 })).toBe(
      "http://192.168.1.42:8787",
    );
  });

  it("renders a .local hostname the same way", () => {
    expect(addressFor({ name: "Podiom", host: "podiom-server.local", port: 8787 })).toBe(
      "http://podiom-server.local:8787",
    );
  });
});

describe("discovery in a browser", () => {
  it("reports discovery unavailable", () => {
    expect(available).toBe(false);
  });

  it("resolves to an empty list rather than rejecting", async () => {
    await expect(discover()).resolves.toEqual([]);
  });

  // The early return is the degradation contract: without it the call still
  // resolves to [] (the catch swallows plugin errors), so only the spy sees
  // the difference.
  it("never consults the plugin", async () => {
    const pluginDiscover = vi.fn(async () => ({
      instances: [{ name: "Podiom", host: "192.168.1.42", port: 8787 }],
    }));
    vi.doMock("./native", () => ({ isNative: false }));
    vi.doMock("@capacitor/core", () => ({
      registerPlugin: () => ({ discover: pluginDiscover }),
    }));
    const { discover } = await importDiscovery();

    await expect(discover()).resolves.toEqual([]);
    expect(pluginDiscover).not.toHaveBeenCalled();
  });
});

describe("discovery through the plugin", () => {
  // isNative is forced on so discover() actually calls through — the branch
  // the browser tests above short-circuit before.
  it("the registered web fallback returns no instances", async () => {
    const calls: { timeoutMs: number }[] = [];
    vi.doMock("./native", () => ({ isNative: true }));
    vi.doMock("@capacitor/core", () => ({
      // Resolves the registered web implementation, the way the real
      // registerPlugin does on a non-native platform.
      registerPlugin: (_name: string, implementations: WebOption) => ({
        discover: (options: { timeoutMs: number }) => {
          calls.push(options);
          return implementations.web().discover(options);
        },
      }),
    }));
    const { discover } = await importDiscovery();

    await expect(discover()).resolves.toEqual([]);
    await expect(discover(1500)).resolves.toEqual([]);
    expect(calls).toEqual([{ timeoutMs: 4000 }, { timeoutMs: 1500 }]);
  });

  // A plugin that answers without an instance list still degrades to "nothing
  // found" — that is the `instances ?? []` fallback.
  it("a plugin resolving without an instance list resolves to an empty list", async () => {
    vi.doMock("./native", () => ({ isNative: true }));
    vi.doMock("@capacitor/core", () => ({
      registerPlugin: () => ({ discover: () => Promise.resolve({}) }),
    }));
    const { discover } = await importDiscovery();

    await expect(discover()).resolves.toEqual([]);
  });

  // A denied local-network permission rejects the plugin call; the catch is
  // what keeps manual entry on the connection screen working.
  it("a rejecting plugin still resolves to an empty list", async () => {
    vi.doMock("./native", () => ({ isNative: true }));
    vi.doMock("@capacitor/core", () => ({
      registerPlugin: () => ({
        discover: () => Promise.reject(new Error("local network access denied")),
      }),
    }));
    const { discover } = await importDiscovery();

    await expect(discover()).resolves.toEqual([]);
  });
});
