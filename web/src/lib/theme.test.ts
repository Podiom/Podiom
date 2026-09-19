import { describe, expect, it, vi } from "vitest";

import {
  agentChipStyle,
  avatarStyle,
  originLabel,
  originStyle,
  providerChip,
} from "./theme";

const { providerChipData } = vi.hoisted(() => ({
  providerChipData: { ink: "test-ink", bg: "test-bg", bd: "test-border" },
}));

vi.mock("./providers", () => ({
  providerMeta: () => ({ chip: providerChipData }),
}));

describe("theme style helpers", () => {
  it("interpolates every avatar argument", () => {
    const style = avatarStyle("linear-gradient(red, blue)", 31, 7, 13);
    expect(style).toContain("width:31px");
    expect(style).toContain("height:31px");
    expect(style).toContain("border-radius:7px");
    expect(style).toContain("background:linear-gradient(red, blue)");
    expect(style).toContain("font:800 13px");
  });

  it("uses provider chip metadata", () => {
    const style = providerChip("codex");
    expect(style).toContain("padding:3px 9px");
    expect(style).toContain(`background:${providerChipData.bg}`);
    expect(style).toContain(`border:1px solid ${providerChipData.bd}`);
    expect(style).toContain(`color:${providerChipData.ink}`);
  });

  it("uses web colors for an unknown origin", () => {
    expect(originStyle("unknown")).toBe(originStyle("web"));
    expect(originStyle("onboarding")).toBe(originStyle("interview"));
    expect(originStyle("roadmap")).not.toBe(originStyle("goal"));
  });

  it("keeps agent and origin chip geometry aligned", () => {
    for (const declaration of [
      "padding:4px 10px",
      "border-radius:999px",
      "font:600 10.5px",
    ]) {
      expect(agentChipStyle()).toContain(declaration);
      expect(originStyle("goal")).toContain(declaration);
    }
  });
});

describe("originLabel", () => {
  it.each([
    ["onboarding", "✦ onboarding"],
    ["interview", "✦ interview"],
    ["schedule", "⟳ schedule"],
    ["roadmap", "▤ roadmap"],
    ["goal", "◎ goal"],
    ["custom", "custom"],
  ])("labels %s", (origin, label) => {
    expect(originLabel(origin)).toBe(label);
  });
});
