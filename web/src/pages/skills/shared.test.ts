import { describe, expect, it } from "vitest";
import type { SkillRegistry } from "../../lib/types";
import { popularity, registryLabel } from "./shared";

describe("popularity", () => {
  it("formats counts under 1000 verbatim", () => {
    expect(popularity(999, 42)).toBe("★ 999 · 42 installs");
  });

  it("compacts 1000..9999 with one decimal place", () => {
    expect(popularity(1000, 1500)).toBe("★ 1.0k · 1.5k installs");
  });

  it("handles the 9999 boundary where toFixed(1) rounds to 10.0k", () => {
    expect(popularity(9999, 9999)).toBe("★ 10.0k · 10.0k installs");
  });

  it("compacts >= 10000 without decimal places", () => {
    expect(popularity(10000, 25400)).toBe("★ 10k · 25k installs");
  });

  it("treats 0 as falsy and returns empty string for popularity(0, 0)", () => {
    expect(popularity(0, 0)).toBe("");
  });

  it("omits stars when stars is 0 or undefined", () => {
    expect(popularity(0, 5)).toBe("5 installs");
    expect(popularity(undefined, 5)).toBe("5 installs");
  });

  it("omits installs when installs is 0 or undefined", () => {
    expect(popularity(5, 0)).toBe("★ 5");
    expect(popularity(5, undefined)).toBe("★ 5");
    expect(popularity(5)).toBe("★ 5");
  });

  it("omits separator when only one metric is present", () => {
    const starsOnly = popularity(5);
    expect(starsOnly).toBe("★ 5");
    expect(starsOnly).not.toContain("·");

    const installsOnly = popularity(undefined, 10);
    expect(installsOnly).toBe("10 installs");
    expect(installsOnly).not.toContain("·");
  });

  it("returns empty string when both arguments are undefined or omitted", () => {
    expect(popularity(undefined, undefined)).toBe("");
    expect(popularity()).toBe("");
  });
});

describe("registryLabel", () => {
  it("maps anthropics to Verified rather than Anthropics", () => {
    expect(registryLabel("anthropics")).toBe("Verified");
  });

  it("maps known registries to their respective labels", () => {
    expect(registryLabel("skillsmp")).toBe("SkillsMP");
    expect(registryLabel("github")).toBe("GitHub");
  });

  it("falls back to GitHub for unrecognized registry values", () => {
    expect(registryLabel("bogus" as SkillRegistry)).toBe("GitHub");
    expect(registryLabel("" as SkillRegistry)).toBe("GitHub");
  });
});
