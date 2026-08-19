// How a phone names itself in Podiom's own device list, so a user with several can tell
// which is which.
//
// Kept apart from push.ts, and free of every import, because the interesting part is a
// pile of platform quirks that are only worth trusting if they are tested — and push.ts
// cannot be loaded outside a real app.

// MAX_LABEL bounds a name that comes from the platform and is rendered straight into the
// device list. Nothing legitimate comes close to it.
export const MAX_LABEL = 40;

// FROZEN_ANDROID_MODEL is what Chromium's user-agent reduction puts where the model used
// to be. It is a literal "K" on every Android device, so it names nothing.
const FROZEN_ANDROID_MODEL = "K";

// UserAgentData is the slice of the Client Hints API this needs. It is absent from
// TypeScript's DOM library and from WebKit, so it is declared here and every use is guarded.
export interface UserAgentData {
  getHighEntropyValues(hints: string[]): Promise<{ model?: string }>;
}

// Navigatorish is what deviceLabel reads off `navigator`, narrowed so a test can supply it.
export interface Navigatorish {
  userAgent: string;
  userAgentData?: UserAgentData;
}

// deviceLabel names the device as specifically as the platform allows.
//
// How specific that is, is the platform's decision rather than ours. Android still gives up
// the model over Client Hints. iOS gives up the device class and nothing more: the user's
// own name for the phone needs Apple's user-assigned-device-name entitlement, which Podiom
// does not have, and iOS 16 and later return a generic string without it. So "iPhone" is
// the honest ceiling on that side, and it still beats the "iPhone or iPad" that came before
// it — that one could not even tell a phone from a tablet.
export async function deviceLabel(platform: string, nav: Navigatorish): Promise<string> {
  if (platform === "ios") return /iPad/.test(nav.userAgent) ? "iPad" : "iPhone";
  if (platform === "android") return (await androidModel(nav)) || "Android device";
  return "Mobile device";
}

// androidModel asks Client Hints first and falls back to the user-agent string, returning
// "" when neither knows. Both paths earn their place: Client Hints is the only one that
// still answers on a reduced user agent, and the string is the only one that answers on a
// WebView too old to have Client Hints.
async function androidModel(nav: Navigatorish): Promise<string> {
  if (nav.userAgentData) {
    try {
      const { model } = await nav.userAgentData.getHighEntropyValues(["model"]);
      const named = usableModel(model ?? "");
      if (named) return named;
    } catch {
      // Hints can be refused. The user-agent string is the fallback either way.
    }
  }
  return androidModelFromUserAgent(nav.userAgent);
}

// androidModelFromUserAgent digs the device model out of an Android user-agent string,
// returning "" when there is nothing usable in it.
//
// The shape is `Mozilla/5.0 (Linux; Android 14; Pixel 8 Build/AP31.240617.009; wv)`: the
// model is the field after the Android version and may carry a `Build/…` suffix. Chromium
// froze that field in version 110 and Android WebView inherited it, so on a current WebView
// this finds nothing and Client Hints is what actually answers.
export function androidModelFromUserAgent(ua: string): string {
  const match = /Android[^;)]*;\s*([^;)]+)/.exec(ua);
  if (!match) return "";
  return usableModel(match[1].replace(/\s+Build\/.*$/, ""));
}

// usableModel rejects the names that would be worse than admitting we do not know.
function usableModel(raw: string): string {
  const model = raw.trim();
  if (!model || model === FROZEN_ANDROID_MODEL) return "";
  return model.slice(0, MAX_LABEL);
}
