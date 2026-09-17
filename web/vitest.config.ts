import { defineConfig } from "vitest/config";

// Vitest covers the pure logic the notification and routing layers rest on:
// hash parsing and formatting, notification-to-route mapping, and action-identifier
// handling. Those are the pieces where a mistake is silent — a malformed hash simply
// lands on the wrong page — so they are worth asserting directly.
//
// Deliberately no component testing. The Svelte layer is covered by svelte-check
// and by driving the real app; testing framework behaviour here would buy slow tests
// of Svelte rather than of Podiom's own logic.
//
// "node" stays the default for that reason. A module whose own dependency needs a
// real DOM opts in per file instead, with jsdom:
//
//     // @vitest-environment jsdom
//
// as the first line of the test file. That keeps the exception visible at the file
// that needs it rather than giving every test a DOM it has no use for.
//
// markdown.ts is the case this exists for: DOMPurify binds to `window`, and without
// one `DOMPurify.sanitize` is not even a function. jsdom rather than happy-dom is
// deliberate — under happy-dom, sanitize() runs but strips `<p>` while leaving an
// `onerror` handler intact, so a sanitization test would pass against a sanitizer
// that is not actually sanitizing.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
