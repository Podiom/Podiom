import { describe, expect, it } from "vitest";

import { appendTranscript } from "./voice";

describe("appendTranscript", () => {
  it("returns the transcript when the current value is empty", () => {
    expect(appendTranscript("", "hello")).toBe("hello");
  });

  it("returns the transcript when the current value is whitespace", () => {
    expect(appendTranscript(" \t\n", "hello")).toBe("hello");
  });

  it("joins an existing value with exactly one space", () => {
    expect(appendTranscript("existing   ", "hello")).toBe("existing hello");
  });
});
