import { describe, expect, it } from "vitest";

import {
  REGISTRY,
  formatDate,
  installPath,
  kebab,
  registryChip,
  scriptsChip,
  shortSHA,
} from "./shared";

describe("skill marketplace helpers", () => {
  it("uses registry colors and falls back to GitHub", () => {
    for (const registry of ["skillsmp", "anthropics", "github"] as const) {
      const style = registryChip(registry);
      expect(style).toContain(`color:${REGISTRY[registry].fg}`);
      expect(style).toContain(`background:${REGISTRY[registry].bg}`);
      expect(style).toContain(`border:1px solid ${REGISTRY[registry].bd}`);
    }
    expect(registryChip("unknown" as never)).toBe(registryChip("github"));
    expect(scriptsChip()).toContain("color:#9A6B1A");
  });

  it.each([[undefined], [""]])("handles an absent SHA (%s)", (sha) =>
    expect(shortSHA(sha)).toBe(""),
  );
  it("shortens a SHA", () => expect(shortSHA("123456789")).toBe("1234567"));

  it("formats valid dates and rejects invalid ones", () => {
    expect(formatDate()).toBe("");
    expect(formatDate("")).toBe("");
    expect(formatDate("not-a-date")).toBe("");
    expect(formatDate("2026-09-19T00:00:00Z")).not.toBe("");
  });

  it.each([
    ["Hello World!", "hello-world"],
    [" Already--Kebab ", "already-kebab"],
    ["Symbols_And Spaces", "symbols-and-spaces"],
  ])("kebab-cases %s", (input, output) => expect(kebab(input)).toBe(output));

  it("uses a kebab-cased install path", () => {
    expect(installPath("My Cool Skill")).toBe(
      "~/.agents/skills/my-cool-skill/",
    );
  });
});
