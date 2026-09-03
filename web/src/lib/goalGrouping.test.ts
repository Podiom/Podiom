import { describe, expect, it } from "vitest";

import { goalGroupedEntries, goalGroupOpen, goalLabel } from "./goalGrouping";
import type { Goal } from "./types";

function goal(overrides: Partial<Goal>): Goal {
  return {
    ID: "goal-alpha",
    Title: "Launch alpha",
    Description: "",
    SuccessCriteria: "",
    Metrics: [],
    ReviewEvery: "",
    LeadAgent: "",
    ProjectID: "",
    Provider: "",
    Profile: "",
    Model: "",
    Effort: "",
    LeadSessionID: "",
    Status: "active",
    NextReviewAt: "",
    ClosingReport: "",
    NextStep: "",
    NextStepWhy: "",
    NextStepAt: "",
    CreatedAt: "",
    UpdatedAt: "",
    ...overrides,
  };
}

describe("goalLabel", () => {
  it("uses the goal title when one is present", () => {
    expect(goalLabel("goal-alpha", goal({ Title: "Ship checkout" }))).toBe("Ship checkout");
  });

  it("falls back to a shortened goal id", () => {
    expect(goalLabel("goal-alpha-123", null)).toBe("Goal goal-alp");
  });

  it("does not shorten ids of eight characters or fewer", () => {
    expect(goalLabel("goal1234", null)).toBe("Goal goal1234");
  });

  it("uses a bare label when no id is available", () => {
    expect(goalLabel("", null)).toBe("Goal");
  });
});

describe("goalGroupedEntries", () => {
  it("keeps items without a goal as standalone entries", () => {
    type Item = { id: string; goalId?: string | null };
    const items: Item[] = [
      { id: "unset" },
      { id: "null", goalId: null },
      { id: "blank", goalId: "   " },
    ];

    expect(goalGroupedEntries(items, (item) => item.goalId, [])).toEqual([
      { kind: "item", item: items[0] },
      { kind: "item", item: items[1] },
      { kind: "item", item: items[2] },
    ]);
  });

  it("groups shared goal ids at their first item position", () => {
    type Item = { id: string; goalId?: string | null };
    const items: Item[] = [
      { id: "first", goalId: "goal-alpha" },
      { id: "solo" },
      { id: "second", goalId: "goal-alpha" },
      { id: "other", goalId: "goal-beta" },
    ];
    const alpha = goal({ ID: "goal-alpha", Title: "Launch alpha" });
    const beta = goal({ ID: "goal-beta", Title: "Launch beta" });

    expect(goalGroupedEntries(items, (item) => item.goalId, [alpha, beta])).toEqual([
      {
        kind: "group",
        goalId: "goal-alpha",
        goal: alpha,
        label: "Launch alpha",
        items: [items[0], items[2]],
      },
      { kind: "item", item: items[1] },
      {
        kind: "group",
        goalId: "goal-beta",
        goal: beta,
        label: "Launch beta",
        items: [items[3]],
      },
    ]);
  });

  it("groups items even when the goal is missing from the lookup", () => {
    const item = { id: "orphan", goalId: "goal-missing-123" };

    expect(goalGroupedEntries([item], (entry) => entry.goalId, [])).toEqual([
      {
        kind: "group",
        goalId: "goal-missing-123",
        goal: null,
        label: "Goal goal-mis",
        items: [item],
      },
    ]);
  });
});

describe("goalGroupOpen", () => {
  it("defaults missing group keys to open", () => {
    expect(goalGroupOpen({}, "goal-alpha")).toBe(true);
  });

  it("respects an explicit closed state", () => {
    expect(goalGroupOpen({ "goal-alpha": false }, "goal-alpha")).toBe(false);
  });
});
