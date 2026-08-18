// keyboard.svelte.ts — is the on-screen keyboard up? On a phone it covers about
// half the screen, so the shell folds away everything that isn't the message
// list and the input while it is (the bottom nav, the chat header, the chip
// bar). Nothing here does anything on a desktop browser, where the state stays
// false forever.
//
// The two platforms need different sources. In the native apps the keyboard is
// KeyboardResize.Native (see initChrome in native.ts): the whole WebView shrinks,
// so visualViewport.height simply tracks window.innerHeight and never reveals
// the keyboard at all — only the plugin's events do. In a browser there is no
// plugin, but the visual viewport is exactly the thing the keyboard shrinks.

import { isNative, onKeyboardToggle } from "./native";

// A keyboard is at least this tall. Below it the gap is a browser UI bar
// collapsing or an address bar sliding away, neither of which should strip the
// chrome off the page.
const MIN_KEYBOARD_HEIGHT = 150;

class KeyboardStore {
  open = $state(false);
}

export const keyboard = new KeyboardStore();

// watchKeyboard starts tracking and returns a teardown. Call it once, from the
// app shell.
export function watchKeyboard(): () => void {
  if (isNative) return onKeyboardToggle((open) => (keyboard.open = open));

  const viewport = window.visualViewport;
  if (!viewport) return () => {};
  const sync = () => {
    keyboard.open = window.innerHeight - viewport.height > MIN_KEYBOARD_HEIGHT;
  };
  sync();
  viewport.addEventListener("resize", sync);
  return () => viewport.removeEventListener("resize", sync);
}
