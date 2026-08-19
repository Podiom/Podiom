import { describe, expect, it } from "vitest";

import { classify, reachable } from "./reachability";

// The offline screen's whole value is that it names which failure happened, so
// each of these pins one reason against the signal that is supposed to produce
// it. Getting the mapping wrong doesn't break the app — it just sends the user
// to check the wrong thing, which is why it is worth holding still.
describe("classify", () => {
  it("reports no network when the device has no interface", () => {
    expect(classify(false, { error: new TypeError("Load failed") })).toEqual({
      reason: "no-network",
      code: "ENETDOWN",
    });
  });

  it("prefers no network over whatever the probe saw", () => {
    // The request never left the phone, so its error says nothing about the
    // gateway and must not be allowed to name the reason.
    expect(classify(false, { status: 502 }).reason).toBe("no-network");
    expect(classify(false, { error: abortError() }).reason).toBe("no-network");
  });

  it("reports a timeout when our own abort signal fired", () => {
    expect(classify(true, { error: abortError() })).toEqual({
      reason: "timeout",
      code: "ETIMEDOUT",
    });
  });

  it("reports a timeout for a plain error object named AbortError", () => {
    // Not every runtime rejects with a DOMException; the name is the contract.
    const err = Object.assign(new Error("aborted"), { name: "AbortError" });
    expect(classify(true, { error: err }).reason).toBe("timeout");
  });

  it("reports the gateway unreachable when the transport failed outright", () => {
    expect(classify(true, { error: new TypeError("Load failed") })).toEqual({
      reason: "unreachable",
      code: "ECONNREFUSED",
    });
  });

  it("surfaces the status when something answered but was not healthy", () => {
    // Reachable, so not a network problem — show what it said rather than
    // claiming a connection was refused.
    expect(classify(true, { status: 503 })).toEqual({
      reason: "unreachable",
      code: "HTTP 503",
    });
  });

  it("does not mistake a thrown non-error for a timeout", () => {
    expect(classify(true, { error: "boom" }).code).toBe("ECONNREFUSED");
    expect(classify(true, { error: undefined }).code).toBe("ECONNREFUSED");
  });
});

describe("reachable", () => {
  it("accepts a 2xx", () => {
    expect(reachable({ status: 200 })).toBe(true);
    expect(reachable({ status: 204 })).toBe(true);
  });

  it("rejects an HTTP error, which is a failure here even though it answered", () => {
    expect(reachable({ status: 401 })).toBe(false);
    expect(reachable({ status: 503 })).toBe(false);
    expect(reachable({ status: 302 })).toBe(false);
  });

  it("rejects a rejected fetch", () => {
    expect(reachable({ error: new TypeError("Load failed") })).toBe(false);
  });
});

function abortError(): DOMException {
  return new DOMException("The operation was aborted.", "AbortError");
}
