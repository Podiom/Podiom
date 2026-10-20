import { describe, expect, it } from "vitest";

import { urlBase64ToUint8Array } from "./vapid";

describe("urlBase64ToUint8Array", () => {
  it("does not append padding to an already padded-length input", () => {
    expect([...urlBase64ToUint8Array("AAAA")]).toEqual([0, 0, 0]);
  });

  it("decodes the URL-safe alphabet", () => {
    expect([...urlBase64ToUint8Array("-_8")]).toEqual([0xfb, 0xff]);
  });

  it("preserves bytes above 0x7f", () => {
    expect([...urlBase64ToUint8Array("//8=")]).toEqual([0xff, 0xff]);
  });

  it("decodes a realistic 65-byte P-256 VAPID key", () => {
    const bytes = Uint8Array.from({ length: 65 }, (_, i) => i);
    const base64url = Buffer.from(bytes).toString("base64url");

    const decoded = urlBase64ToUint8Array(base64url);

    expect(decoded).toHaveLength(65);
    expect([...decoded]).toEqual([...bytes]);
  });
});
