// @vitest-environment jsdom

import { describe, expect, it } from "vitest";

import { renderMarkdown } from "./markdown";

describe("renderMarkdown", () => {
  it("renders basic markdown", () => {
    expect(renderMarkdown("**bold**")).toContain("<strong>bold</strong>");
    const list = renderMarkdown("- one\n- two");
    expect(list).toContain("<li>one</li>");
    expect(list).toContain("<li>two</li>");
  });

  it("turns a single newline into a line break", () => {
    expect(renderMarkdown("first\nsecond")).toContain("first<br>second");
  });

  it("sanitizes scripts and inline event handlers", () => {
    const html = renderMarkdown(`<script>alert("x")</script><img src="x" onerror="alert(1)">`);
    expect(html).not.toContain("<script");
    expect(html).not.toContain("onerror");
    expect(html).toContain(`<img src="x">`);
  });

  it("keeps plain text readable", () => {
    expect(renderMarkdown("plain text")).toBe("<p>plain text</p>\n");
  });
});
