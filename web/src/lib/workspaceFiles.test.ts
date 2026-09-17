import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { extractWorkspaceFileLinks, workspaceFileIDFromHref } from "./workspaceFiles.svelte";

const ID = "123E4567-E89B-12D3-A456-426614174000";
const LOWER_ID = ID.toLowerCase();
const originalDocument = globalThis.document;
beforeAll(() => {
  Object.defineProperty(globalThis, "document", { configurable: true, value: { baseURI: "https://podiom.test/app/" } });
});
afterAll(() => {
  Object.defineProperty(globalThis, "document", { configurable: true, value: originalDocument });
});


describe("extractWorkspaceFileLinks", () => {
  it("extracts, normalizes, unescapes, and deduplicates workspace links", () => {
    const markdown = String.raw`[file \[one\] \\ copy](api/workspace-files/${ID}) and [duplicate](./api/workspace-files/${LOWER_ID})`;
    expect(extractWorkspaceFileLinks(markdown)).toEqual([{ id: LOWER_ID, label: "file [one] \\ copy" }]);
  });

  it("ignores unrelated text and links", () => {
    expect(extractWorkspaceFileLinks("plain text [site](https://example.com)")).toEqual([]);
  });
});

describe("workspaceFileIDFromHref", () => {
  it("accepts a same-origin workspace-file href", () => {
    expect(workspaceFileIDFromHref(`api/workspace-files/${ID}`)).toBe(ID);
  });

  it.each([`api/workspace-files/${ID}?download=1`, `api/workspace-files/${ID}#preview`, `https://example.com/app/api/workspace-files/${ID}`])("rejects %s", (href) => {
    expect(workspaceFileIDFromHref(href)).toBeNull();
  });
});
