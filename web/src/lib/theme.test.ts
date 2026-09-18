import { describe, expect, it, vi } from "vitest";

vi.mock("./providers", () => ({
  providerMeta: () => ({ chip: { bg: "", bd: "", ink: "" } }),
}));

import {
  agentGradient,
  GRADIENTS,
  initial,
  isAgentOrigin,
  modeChip,
  PROJECT_COLORS,
  projectColor,
} from "./theme";
import type { PermissionMode, SessionOrigin } from "./types";

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

describe("modeChip", () => {
  const base =
    "padding:3px 9px;border-radius:999px;font:600 10px 'JetBrains Mono',monospace;";

  it("always starts with the base chip style prefix", () => {
    const samples: (PermissionMode | string)[] = [
      "approve",
      "auto",
      "yolo",
      "",
      "unknown",
      "YOLO",
    ];
    for (const m of samples) {
      expect(modeChip(m).startsWith(base)).toBe(true);
    }
  });

  it("renders distinct colour suffixes for the three valid modes", () => {
    const approveSuffix = modeChip("approve").slice(base.length);
    const autoSuffix = modeChip("auto").slice(base.length);
    const yoloSuffix = modeChip("yolo").slice(base.length);

    expect(approveSuffix).not.toBe(autoSuffix);
    expect(autoSuffix).not.toBe(yoloSuffix);
    expect(approveSuffix).not.toBe(yoloSuffix);
  });

  const cases: [PermissionMode | string, string][] = [
    ["approve", "background:#EAF1ED;border:1px solid #CFE3D8;color:#3F7A5F"],
    ["auto", "background:#FBF0DA;border:1px solid #EDDCAE;color:#8A6516"],
    ["yolo", "background:#F8E0D6;border:1px solid #EFC3AF;color:#B14E2A"],
    // Unrecognized or case-mismatched strings fall back to the safe/calm approve posture
    ["", "background:#EAF1ED;border:1px solid #CFE3D8;color:#3F7A5F"],
    ["unknown", "background:#EAF1ED;border:1px solid #CFE3D8;color:#3F7A5F"],
    ["YOLO", "background:#EAF1ED;border:1px solid #CFE3D8;color:#3F7A5F"],
  ];

  it.each(cases)("modeChip(%j) matches expected style", (mode, suffix) => {
    expect(modeChip(mode)).toBe(base + suffix);
  });
});

describe("isAgentOrigin", () => {
  // AGENT_ORIGINS mirrors server.unattendedOrigins on the Go side.
  // Drift between the two is invisible, so this test serves as a drift alarm.
  const unionCases: [SessionOrigin, boolean][] = [
    ["schedule", true],
    ["roadmap", true],
    ["goal", true],
    ["web", false],
    ["cli", false],
    ["onboarding", false],
    ["interview", false],
  ];

  it.each(unionCases)("isAgentOrigin(%j) -> %j", (origin, want) => {
    expect(isAgentOrigin(origin)).toBe(want);
  });

  const nonMatchingCases: [string, boolean][] = [
    ["", false],
    ["Goal", false],
    ["goals", false],
    ["Schedule", false],
    ["unattended", false],
  ];

  it.each(nonMatchingCases)("isAgentOrigin non-matching string %j -> %j", (input, want) => {
    expect(isAgentOrigin(input)).toBe(want);
  });
});
