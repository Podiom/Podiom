import { describe, expect, it } from "vitest";

import { androidModelFromUserAgent, deviceLabel } from "./devicelabel";

// The device list is the only place a user can tell two registered phones apart, so a
// label that silently degrades to a placeholder is worse than one that admits it does not
// know. These pin both halves of that.
describe("androidModelFromUserAgent", () => {
  it("reads the model out of a WebView user agent", () => {
    expect(
      androidModelFromUserAgent(
        "Mozilla/5.0 (Linux; Android 14; Pixel 8 Build/AP31.240617.009; wv) " +
          "AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/126.0.6478.71 Mobile Safari/537.36",
      ),
    ).toBe("Pixel 8");
  });

  it("reads a multi-word model with no Build suffix", () => {
    expect(
      androidModelFromUserAgent("Mozilla/5.0 (Linux; Android 13; SM-S918B) AppleWebKit/537.36"),
    ).toBe("SM-S918B");
  });

  // Chromium's user-agent reduction replaces every model with "K". Labelling a phone "K"
  // would look like a name while naming nothing, so the caller must get the chance to say
  // "Android device" instead.
  it("refuses the frozen placeholder a reduced user agent carries", () => {
    expect(
      androidModelFromUserAgent(
        "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) " +
          "Chrome/126.0.0.0 Mobile Safari/537.36",
      ),
    ).toBe("");
  });

  it("finds nothing in an iOS user agent", () => {
    expect(
      androidModelFromUserAgent(
        "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15",
      ),
    ).toBe("");
  });

  it("finds nothing in an empty user agent", () => {
    expect(androidModelFromUserAgent("")).toBe("");
  });

  // The value is rendered straight into the device list, so a user agent that claims an
  // absurd model must not be able to stretch the row.
  it("caps an implausibly long model", () => {
    const model = androidModelFromUserAgent(`Mozilla/5.0 (Linux; Android 14; ${"M".repeat(200)})`);
    expect(model).toHaveLength(40);
  });
});

const IOS_UA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15";
const IPAD_UA = "Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15";
const REDUCED_ANDROID_UA = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36";

describe("deviceLabel", () => {
  it("tells an iPhone from an iPad", async () => {
    expect(await deviceLabel("ios", { userAgent: IOS_UA })).toBe("iPhone");
    expect(await deviceLabel("ios", { userAgent: IPAD_UA })).toBe("iPad");
  });

  // The point of asking Client Hints first: this user agent has been reduced to "K", so
  // the string alone would give up and the phone would go back to being anonymous.
  it("prefers the model Client Hints reports over a reduced user agent", async () => {
    const label = await deviceLabel("android", {
      userAgent: REDUCED_ANDROID_UA,
      userAgentData: { getHighEntropyValues: async () => ({ model: "Pixel 8" }) },
    });
    expect(label).toBe("Pixel 8");
  });

  it("falls back to the user agent when Client Hints are refused", async () => {
    const label = await deviceLabel("android", {
      userAgent: "Mozilla/5.0 (Linux; Android 14; Pixel 8 Build/AP31.240617.009; wv)",
      userAgentData: {
        getHighEntropyValues: async () => {
          throw new Error("refused");
        },
      },
    });
    expect(label).toBe("Pixel 8");
  });

  // Naming it "K" would look like an answer. Saying "Android device" admits it is not one.
  it("says Android device when nothing knows the model", async () => {
    expect(await deviceLabel("android", { userAgent: REDUCED_ANDROID_UA })).toBe("Android device");
  });

  it("leaves the browser alone", async () => {
    expect(await deviceLabel("web", { userAgent: IOS_UA })).toBe("Mobile device");
  });
});
