import { afterEach, describe, expect, it } from "vitest";

import { setEndpoint } from "./base";
import { terminalUrl } from "./terminal";

afterEach(() => setEndpoint(null));

describe("terminalUrl", () => {
  it.each(["onboard", "shell"] as const)(
    "builds the %s terminal URL with a trailing slash",
    (flow) => {
      setEndpoint(new URL("http://h:8080/"));

      expect(terminalUrl(flow)).toBe(`http://h:8080/terminal/${flow}/`);
    },
  );

  it("keeps the configured endpoint sub-path", () => {
    setEndpoint(new URL("https://h/ingress/token/"));

    expect(terminalUrl("shell")).toBe(
      "https://h/ingress/token/terminal/shell/",
    );
  });
});
