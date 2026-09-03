import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { relativeTime, shortDate } from "./memory";

const NOW = new Date("2026-09-03T12:00:00.000Z");

function minutesAgo(minutes: number): string {
  return new Date(NOW.getTime() - minutes * 60_000).toISOString();
}

function hoursAgo(hours: number): string {
  return new Date(NOW.getTime() - hours * 60 * 60_000).toISOString();
}

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
