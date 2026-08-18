import { defineConfig } from "vitest/config";

// Vitest covers the pure logic the notification and routing layers rest on:
// hash parsing and formatting, notification-to-route mapping, and action-identifier
// handling. Those are the pieces where a mistake is silent — a malformed hash simply
// lands on the wrong page — so they are worth asserting directly.
//
// Deliberately no component or DOM testing. The Svelte layer is covered by
// svelte-check and by driving the real app; adding a DOM environment here would buy
// slow tests of framework behaviour rather than of Podiom's own.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
