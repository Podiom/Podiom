import { describe, expect, it } from "vitest";
import {
  formatDate,
  installPath,
  kebab,
  popularity,
  registryChip,
  registryLabel,
  scriptsChip,
  shortSHA,
} from "./shared";
import type { SkillRegistry } from "../../lib/types";

describe("popularity", () => {
  it("renders empty string when no arguments provided or both zero", () => {
    expect(popularity()).toBe("");
    expect(popularity(0, 0)).toBe("");
    expect(popularity(undefined, undefined)).toBe("");
    expect(popularity(0, undefined)).toBe("");
    expect(popularity(undefined, 0)).toBe("");
  });

  it("handles stars only without trailing separator", () => {
    expect(popularity(5, undefined)).toBe("★ 5");
    expect(popularity(5, 0)).toBe("★ 5");
    expect(popularity(5)).toBe("★ 5");
  });

  it("handles installs only without star prefix or leading separator", () => {
    expect(popularity(0, 5)).toBe("5 installs");
    expect(popularity(undefined, 5)).toBe("5 installs");
  });

  it("combines stars and installs with separator when both present", () => {
    expect(popularity(10, 20)).toBe("★ 10 · 20 installs");
  });

  it("compacts numbers around thresholds and preserves 9999 rounding behavior", () => {
    expect(popularity(999, 999)).toBe("★ 999 · 999 installs");
    expect(popularity(1000, 1000)).toBe("★ 1.0k · 1.0k installs");
    expect(popularity(1500, 2500)).toBe("★ 1.5k · 2.5k installs");
    expect(popularity(9999, 9999)).toBe("★ 10.0k · 10.0k installs");
    expect(popularity(10000, 10000)).toBe("★ 10k · 10k installs");
    expect(popularity(125000, 125000)).toBe("★ 125k · 125k installs");
  });
});

describe("registryLabel", () => {
  it("maps anthropics to Verified instead of the key name", () => {
    expect(registryLabel("anthropics")).toBe("Verified");
  });

  it("maps known registries to their respective labels", () => {
    expect(registryLabel("skillsmp")).toBe("SkillsMP");
    expect(registryLabel("github")).toBe("GitHub");
  });

  it("falls back to GitHub for unrecognized registry value", () => {
    expect(registryLabel("bogus" as SkillRegistry)).toBe("GitHub");
  });
});

describe("registryChip and scriptsChip", () => {
  it("renders registry chips with corresponding colors", () => {
    expect(registryChip("anthropics")).toContain("color:#2F6E60");
    expect(registryChip("skillsmp")).toContain("color:#4B5560");
    expect(registryChip("github")).toContain("color:#8A7560");
  });

  it("falls back to GitHub colors for unrecognized registry value", () => {
    expect(registryChip("bogus" as SkillRegistry)).toContain("color:#8A7560");
  });

  it("renders scriptsChip with warning colors", () => {
    expect(scriptsChip()).toContain("color:#9A6B1A");
  });
});

describe("shortSHA", () => {
  it("truncates SHA to 7 characters", () => {
    expect(shortSHA("e194df515e9984a584544c923ea6706798e4309a")).toBe("e194df5");
  });

  it("returns empty string when SHA is missing or empty", () => {
    expect(shortSHA()).toBe("");
    expect(shortSHA("")).toBe("");
    expect(shortSHA(undefined)).toBe("");
  });
});

describe("formatDate", () => {
  it("formats valid ISO timestamp into locale date string", () => {
    const formatted = formatDate("2026-01-15T12:00:00Z");
    expect(formatted).toBeTruthy();
    expect(formatted).toContain("2026");
  });

  it("returns empty string for missing, empty, or invalid dates", () => {
    expect(formatDate()).toBe("");
    expect(formatDate("")).toBe("");
    expect(formatDate(undefined)).toBe("");
    expect(formatDate("not-a-valid-date")).toBe("");
  });
});

describe("kebab and installPath", () => {
  it("converts strings to kebab-case and trims leading/trailing hyphens", () => {
    expect(kebab("Hello World")).toBe("hello-world");
    expect(kebab("---Leading and Trailing---")).toBe("leading-and-trailing");
    expect(kebab("Special!@#$%^&*Characters")).toBe("special-characters");
  });

  it("formats install path using kebab-cased skill name", () => {
    expect(installPath("My Skill")).toBe("~/.agents/skills/my-skill/");
  });
});
