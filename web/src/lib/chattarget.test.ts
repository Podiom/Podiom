import { describe, expect, it } from "vitest";

import { carryChatTarget, type ChatTarget } from "./chattarget";

describe("carryChatTarget", () => {
  it("keeps a parked seed when the hash lands on the same session", () => {
    // The roadmap start case: openChat parks the task prompt, navigate() assigns the
    // hash, and the hashchange comes back knowing only the session id.
    const parked: ChatTarget = { sessionId: "s1", seed: "Add dark mode\n\nUse the tokens." };
    expect(carryChatTarget(parked, "s1")).toEqual(parked);
  });

  it("drops a seed parked for a different session", () => {
    const parked: ChatTarget = { sessionId: "s1", seed: "Add dark mode" };
    expect(carryChatTarget(parked, "s2")).toEqual({ sessionId: "s2" });
  });

  it("returns a bare target when nothing was parked", () => {
    expect(carryChatTarget(null, "s1")).toEqual({ sessionId: "s1" });
  });

  it("does not attach an agent-only target to a session", () => {
    // openChat({ agentName }) navigates to the chat route rather than a session, so a
    // target still sitting here belongs to a different navigation.
    const parked: ChatTarget = { agentName: "jared" };
    expect(carryChatTarget(parked, "s1")).toEqual({ sessionId: "s1" });
  });

  it("takes permission from the hash, with or without a parked target", () => {
    const parked: ChatTarget = { sessionId: "s1", seed: "run it" };
    expect(carryChatTarget(parked, "s1", true)).toEqual({ ...parked, permission: true });
    expect(carryChatTarget(null, "s1", true)).toEqual({ sessionId: "s1", permission: true });
  });

  it("leaves permission unset when the hash does not ask for it", () => {
    // Only #/chat/<id>/permission sets the flag; an ordinary visit to the same session
    // must not re-open the approval dock.
    expect(carryChatTarget(null, "s1", false)).toEqual({ sessionId: "s1" });
    expect(carryChatTarget({ sessionId: "s1", seed: "run it" }, "s1")).toEqual({
      sessionId: "s1",
      seed: "run it",
    });
  });
});
