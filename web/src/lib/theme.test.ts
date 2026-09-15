import { describe, expect, it } from "vitest";

import { agentGradient, GRADIENTS, initial, PROJECT_COLORS, projectColor } from "./theme";

describe("initial", () => {
  it("uses the empty-name fallback", () => expect(initial("")).toBe("?"));
  it("trims before uppercasing the first character", () => expect(initial("  ada")).toBe("A"));
});

describe("agentGradient", () => {
  it("maps names deterministically to an exported gradient", () => {
    expect(agentGradient("ada")).toBe(agentGradient("ada"));
    expect(GRADIENTS).toContain(agentGradient("ada"));
  });
  it("maps empty names to an exported gradient", () => expect(GRADIENTS).toContain(agentGradient("")));
});

describe("projectColor", () => {
  it("maps ids deterministically to an exported color", () => {
    expect(projectColor("project-1")).toBe(projectColor("project-1"));
    expect(PROJECT_COLORS).toContain(projectColor("project-1"));
  });
  it("maps empty ids to an exported color", () => expect(PROJECT_COLORS).toContain(projectColor("")));
});
