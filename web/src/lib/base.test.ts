// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";

import { apiUrl, deployment, endpoint, setEndpoint, wsUrl } from "./base";

// The configured endpoint is module-level state: anything a test installs here
// would leak into the next test, so each one clears it again afterwards.
afterEach(() => {
  setEndpoint(null);
  document.querySelectorAll("base, meta[name='podiom-deployment']").forEach((node) => node.remove());
});

describe("deployment", () => {
  it.each([
    [null, "standalone"],
    ["standalone", "standalone"],
    ["HA", "standalone"],
    ["ha-ingress", "standalone"],
    ["ha", "ha"],
  ])("maps %s to %s", (content, want) => {
    if (content !== null) {
      const meta = document.createElement("meta");
      meta.name = "podiom-deployment";
      meta.content = content;
      document.head.append(meta);
    }
    expect(deployment()).toBe(want);
  });
});

describe("setEndpoint", () => {
  it("reports no endpoint before one is configured", () => {
    expect(endpoint()).toBeNull();
  });

  it("round-trips the configured endpoint through endpoint()", () => {
    const url = new URL("http://h:8080/");

    setEndpoint(url);

    expect(endpoint()).toBe(url);
  });

  it("clears the configured endpoint when passed null", () => {
    setEndpoint(new URL("http://h:8080/"));

    setEndpoint(null);

    expect(endpoint()).toBeNull();
  });
});

describe("apiUrl", () => {
  it.each([
    ["http://h/api/hassio_ingress/tok/", "http://h/api/hassio_ingress/tok/api/agents"],
    ["http://h/api/hassio_ingress/tok/index.html", "http://h/api/hassio_ingress/tok/api/agents"],
  ])("uses the document base %s", (href, want) => {
    const base = document.createElement("base");
    base.href = href;
    document.head.append(base);
    expect(apiUrl("/api/agents").href).toBe(want);
  });

  // The leading-slash strip is what keeps the path relative: without it
  // "/api/agents" would resolve against the origin root and drop the base
  // path entirely.
  it("resolves paths with and without a leading slash identically", () => {
    setEndpoint(new URL("http://h/"));

    expect(apiUrl("/api/agents").href).toBe("http://h/api/agents");
    expect(apiUrl("api/agents").href).toBe("http://h/api/agents");
  });

  // Under Home Assistant Ingress the app is served from a rewritten sub-path;
  // a URL resolved against the origin root would lose that prefix and break
  // the whole add-on.
  it("keeps the ingress sub-path prefix in the resolved URL", () => {
    setEndpoint(new URL("http://h/api/hassio_ingress/tok/"));

    expect(apiUrl("api/agents").href).toBe(
      "http://h/api/hassio_ingress/tok/api/agents",
    );
    expect(apiUrl("/api/agents").href).toBe(
      "http://h/api/hassio_ingress/tok/api/agents",
    );
  });
});

describe("wsUrl", () => {
  it("uses the document base when no endpoint is configured", () => {
    const base = document.createElement("base");
    base.href = "https://h/api/hassio_ingress/tok/";
    document.head.append(base);
    expect(wsUrl()).toBe("wss://h/api/hassio_ingress/tok/api/ws");
  });

  it("upgrades an http: base to ws:", () => {
    setEndpoint(new URL("http://h:8080/"));

    expect(wsUrl()).toBe("ws://h:8080/api/ws");
  });

  it("upgrades an https: base to wss:", () => {
    setEndpoint(new URL("https://h/"));

    expect(wsUrl()).toBe("wss://h/api/ws");
  });

  it("keeps the ingress prefix when upgrading the scheme", () => {
    setEndpoint(new URL("https://h/api/hassio_ingress/tok/"));

    expect(wsUrl()).toBe("wss://h/api/hassio_ingress/tok/api/ws");
  });
});
