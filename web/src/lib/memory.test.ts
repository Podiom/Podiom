import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { parseMemory, relativeTime, shortDate } from "./memory";
import type { Dream, DreamNewItem, DreamStatus } from "./types";

const NOW = new Date("2026-09-03T12:00:00.000Z");

function minutesAgo(minutes: number): string {
  return new Date(NOW.getTime() - minutes * 60_000).toISOString();
}

function hoursAgo(hours: number): string {
  return new Date(NOW.getTime() - hours * 60 * 60_000).toISOString();
}

// parseMemory only reads RanAt, Status and NewItems off a dream journal row;
// the remaining fields exist to satisfy the Dream shape.
function dream(
  ranAt: string,
  newItems: DreamNewItem[] | null = [],
  status: DreamStatus = "success",
): Dream {
  return {
    ID: `dream-${ranAt}`,
    AgentName: "vera",
    RanAt: ranAt,
    FinishedAt: ranAt,
    Trigger: "nightly",
    Status: status,
    Error: "",
    SessionCount: 0,
    Kept: 0,
    Merged: 0,
    Pruned: 0,
    Note: "",
    NewItems: newItems,
  };
}

describe("parseMemory", () => {
  it("splits ## lines into sections with the marker stripped", () => {
    const parsed = parseMemory("## Likes\n- rain\n## Dislikes\n- wind");

    expect(parsed.sections.map((s) => s.title)).toEqual(["Likes", "Dislikes"]);
  });

  it("collects both - and * bullets as section items", () => {
    const parsed = parseMemory("## Likes\n- rain\n* coffee");

    expect(parsed.sections[0].items.map((i) => i.text)).toEqual(["rain", "coffee"]);
  });

  it("ignores the top-level # heading instead of making it a section", () => {
    const parsed = parseMemory("# Memory\n## Likes\n- rain");

    expect(parsed.sections.map((s) => s.title)).toEqual(["Likes"]);
  });

  it("skips comment lines and strips inline comments from item text", () => {
    const parsed = parseMemory(
      "<!-- last dreamed 2026-09-03 -->\n## Likes\n- rain <!-- the wet kind -->",
    );

    expect(parsed.sections[0].items).toEqual([{ text: "rain" }]);
  });

  it("collapses whitespace inside an item to single spaces", () => {
    const parsed = parseMemory("## Likes\n-   rain   on  rooftops  ");

    expect(parsed.sections[0].items[0].text).toBe("rain on rooftops");
  });

  it("drops a bullet that is empty once comments are stripped", () => {
    const parsed = parseMemory("## Likes\n- <!-- nothing kept -->\n- rain");

    expect(parsed.sections[0].items).toEqual([{ text: "rain" }]);
  });

  it("collects prose before the first section into intro", () => {
    const parsed = parseMemory("Written by hand.\nKept between dreams.\n## Likes\n- rain");

    expect(parsed.intro).toBe("Written by hand. Kept between dreams.");
  });

  it("does not attach a bullet appearing before any section", () => {
    const parsed = parseMemory("- stray\n## Likes\n- rain");

    expect(parsed.sections).toHaveLength(1);
    expect(parsed.sections[0].items).toEqual([{ text: "rain" }]);
  });

  it("returns no sections for empty input or a bare title", () => {
    expect(parseMemory("")).toEqual({ intro: "", sections: [] });
    expect(parseMemory("# Memory").sections).toEqual([]);
  });

  // The dream journal is handed in newest-first and re-sorted inside
  // parseMemory, so the fixtures below keep that order on purpose.
  it("sets since to the oldest successful dream that introduced the item", () => {
    const dreams = [
      dream("2026-09-03T00:00:00.000Z"),
      dream("2026-09-02T00:00:00.000Z", [{ section: "Likes", text: "rain" }]),
      dream("2026-09-01T00:00:00.000Z", [{ section: "Likes", text: "rain" }]),
    ];

    const item = parseMemory("## Likes\n- rain", dreams).sections[0].items[0];

    expect(item.since).toBe("2026-09-01T00:00:00.000Z");
    expect(item.isNew).toBeUndefined();
  });

  it("flags isNew only when the most recent successful dream introduced the item", () => {
    const dreams = [
      dream("2026-09-03T00:00:00.000Z", [{ section: "Likes", text: "rain" }]),
      dream("2026-09-01T00:00:00.000Z", [{ section: "Likes", text: "rain" }]),
    ];

    const item = parseMemory("## Likes\n- rain", dreams).sections[0].items[0];

    expect(item.isNew).toBe(true);
    expect(item.since).toBe("2026-09-01T00:00:00.000Z");
  });

  // A failed or still-running dream is filtered out before the oldest/newest
  // scans run, so it can neither supply a since date nor steal the NEW badge.
  it.each(["running", "error"] as const)(
    "ignores dreams with status %s",
    (status) => {
      const dreams = [
        dream("2026-09-03T00:00:00.000Z", [{ section: "Likes", text: "rain" }], status),
      ];

      const item = parseMemory("## Likes\n- rain", dreams).sections[0].items[0];

      expect(item.since).toBeUndefined();
      expect(item.isNew).toBeUndefined();
    },
  );

  it("still flags isNew when the newest dream failed", () => {
    const dreams = [
      dream("2026-09-03T00:00:00.000Z", [], "error"),
      dream("2026-09-02T00:00:00.000Z", [{ section: "Likes", text: "rain" }]),
    ];

    const item = parseMemory("## Likes\n- rain", dreams).sections[0].items[0];

    expect(item.isNew).toBe(true);
    expect(item.since).toBe("2026-09-02T00:00:00.000Z");
  });

  it("matches dream items by normalized text and section", () => {
    const dreams = [
      dream("2026-09-01T00:00:00.000Z", [
        { section: "  Likes ", text: "rain   on  rooftops" },
      ]),
    ];

    const item = parseMemory("## Likes\n- rain on rooftops", dreams).sections[0]
      .items[0];

    expect(item.since).toBe("2026-09-01T00:00:00.000Z");
  });

  it("does not match a dream item recorded under a different section", () => {
    const dreams = [
      dream("2026-09-01T00:00:00.000Z", [{ section: "Dislikes", text: "rain" }]),
    ];

    const item = parseMemory("## Likes\n- rain", dreams).sections[0].items[0];

    expect(item.since).toBeUndefined();
    expect(item.isNew).toBeUndefined();
  });

  it("treats a dream with null NewItems as having introduced nothing", () => {
    const dreams = [dream("2026-09-01T00:00:00.000Z", null)];

    const item = parseMemory("## Likes\n- rain", dreams).sections[0].items[0];

    expect(item.since).toBeUndefined();
    expect(item.isNew).toBeUndefined();
  });
});

describe("relativeTime", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders never when the timestamp is missing or invalid", () => {
    expect(relativeTime(undefined)).toBe("never");
    expect(relativeTime(null)).toBe("never");
    expect(relativeTime("not a date")).toBe("never");
  });

  it("renders just now for timestamps under a minute old", () => {
    expect(relativeTime(minutesAgo(0))).toBe("just now");
    expect(relativeTime(new Date(NOW.getTime() - 59_999).toISOString())).toBe("just now");
  });

  it("renders minutes until exactly 60 minutes rolls over to hours", () => {
    expect(relativeTime(minutesAgo(1))).toBe("1m ago");
    expect(relativeTime(minutesAgo(59))).toBe("59m ago");
    expect(relativeTime(minutesAgo(60))).toBe("1h ago");
  });

  it("renders hours until exactly 24 hours rolls over to days", () => {
    expect(relativeTime(hoursAgo(1))).toBe("1h ago");
    expect(relativeTime(hoursAgo(23))).toBe("23h ago");
    expect(relativeTime(hoursAgo(24))).toBe("1d ago");
  });

  it("renders whole days beyond 24 hours", () => {
    expect(relativeTime(hoursAgo(47))).toBe("1d ago");
    expect(relativeTime(hoursAgo(48))).toBe("2d ago");
  });
});

describe("shortDate", () => {
  it("renders an empty string when the timestamp is missing or invalid", () => {
    expect(shortDate(undefined)).toBe("");
    expect(shortDate("not a date")).toBe("");
  });

  it("renders a short month-and-day label for a valid ISO date", () => {
    const rendered = shortDate("2026-09-03T12:00:00.000Z");

    expect(rendered).not.toBe("");
    expect(rendered).toContain("3");
    expect(rendered).toBe(
      new Date("2026-09-03T12:00:00.000Z").toLocaleDateString(undefined, {
        month: "short",
        day: "numeric",
      }),
    );
  });
});
