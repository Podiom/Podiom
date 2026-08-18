// native.ts — which platform the SPA is running on, and the handful of native
// affordances shared code needs. Everything here works in a browser too (as a
// no-op or the plain web behaviour), so pages call it unconditionally rather
// than forking into web and mobile variants (R2).

import { App as CapApp } from "@capacitor/app";
import { Browser } from "@capacitor/browser";
import { Capacitor } from "@capacitor/core";
import { Keyboard, KeyboardResize } from "@capacitor/keyboard";
import { Style, StatusBar } from "@capacitor/status-bar";
import { SecureStorage } from "@aparajita/capacitor-secure-storage";

// isNative is true inside the iOS and Android apps, false in any browser.
// It is the one switch that decides whether the app talks to a configured
// remote instance (see base.ts) or to the origin that served it.
export const isNative = Capacitor.isNativePlatform();

export const platform = Capacitor.getPlatform();

// openExternal sends a URL out of the app, returning the popup handle in a
// browser and null on native. window.open opens nothing in a WebView, so
// without this every sign-in and docs link would be a dead button; on native
// the URL goes to the system browser sheet instead.
//
// features is the window.open feature string used in the browser; it has no
// native equivalent and is ignored there.
export function openExternal(url: string, target = "_blank", features = "noopener"): Window | null {
  if (isNative) {
    void Browser.open({ url });
    return null;
  }
  return window.open(url, target, features);
}

// closeExternal dismisses whatever openExternal opened. On native the sheet is
// ours to close; in a browser the caller's own window handle is authoritative,
// so this only covers the native half.
export function closeExternal(): void {
  if (!isNative) return;
  void Browser.close().catch(() => {
    // Already dismissed by the user.
  });
}

// secureGet / secureSet / secureForget wrap the platform keystore — the iOS
// Keychain and Android's EncryptedSharedPreferences. The gateway token is a
// bearer credential for a machine that can run shell commands, so on native it
// is never written to localStorage (R7). Callers must guard on isNative: the
// plugin's web implementation silently falls back to localStorage, which would
// defeat the point.
//
// All three swallow failures. A keystore read that fails means the user is sent
// back to the connection screen, which is recoverable; a write that fails costs
// them re-entering the token next launch. Neither is worth a crash on boot.
export async function secureGet(key: string): Promise<string> {
  try {
    return (await SecureStorage.getItem(key)) ?? "";
  } catch {
    return "";
  }
}

export async function secureSet(key: string, value: string): Promise<void> {
  try {
    await SecureStorage.setItem(key, value);
  } catch {
    // Best-effort: the value stays in memory for this session.
  }
}

export async function secureForget(key: string): Promise<void> {
  try {
    await SecureStorage.removeItem(key);
  } catch {
    // Best-effort: a stale entry is re-validated on next launch anyway.
  }
}

// initChrome settles the parts of the native shell that surround the WebView.
// No-op in a browser.
export function initChrome(): void {
  if (!isNative) return;

  // Podiom's background is warm parchment, so the status bar needs dark
  // content. Style.Light means "dark text for a light background".
  void StatusBar.setStyle({ style: Style.Light }).catch(() => {});
  if (platform === "android") {
    void StatusBar.setBackgroundColor({ color: "#f4ece2" }).catch(() => {});
  }

  // Resize the WebView rather than pushing the whole document up, so the app
  // shell — in particular the fixed bottom nav — stays laid out correctly while
  // the keyboard is open.
  void Keyboard.setResizeMode({ mode: KeyboardResize.Native }).catch(() => {});
}

// onBackButton registers Android's hardware/gesture back. Returns an
// unsubscribe. The handler decides what "back" means and returns true when it
// consumed the press; returning false exits the app, which is the correct
// behaviour only at the top of the navigation stack.
export function onBackButton(handler: () => boolean): () => void {
  if (!isNative || platform !== "android") return () => {};
  const registration = CapApp.addListener("backButton", () => {
    if (handler()) return;
    void CapApp.exitApp();
  });
  return () => {
    void registration.then((r) => r.remove());
  };
}

// onKeyboardToggle reports the on-screen keyboard opening and closing. Returns
// an unsubscribe. It never fires in a browser — keyboard.svelte.ts reads
// visualViewport there instead — because the plugin has no web implementation
// worth trusting for this.
//
// The "will" events rather than "did": the UI that folds away on a phone should
// collapse while the keyboard animates in, not after it has landed.
export function onKeyboardToggle(handler: (open: boolean) => void): () => void {
  if (!isNative) return () => {};
  const shown = Keyboard.addListener("keyboardWillShow", () => handler(true));
  const hidden = Keyboard.addListener("keyboardWillHide", () => handler(false));
  return () => {
    void shown.then((r) => r.remove());
    void hidden.then((r) => r.remove());
  };
}
