// Native push registration and handling for the iOS and Android apps.
//
// Podiom's side of native push is deliberately thin. The daemon decides what is worth
// notifying about and what a payload may contain; the relay is a transport; this module
// only registers the device, keeps its token current, and turns a tapped notification
// into a destination.
//
// The device id is Podiom's own, not the push token. The token rotates while the phone
// stays the same, so registering by token would create a new device on every refresh and
// deliver the same notification twice.

import { FirebaseMessaging } from "@capacitor-firebase/messaging";

import { deleteNotificationDevice, registerNotificationDevice } from "./api";
import { targetFromNotification, type Target } from "./deeplink";
import { randomID } from "./id";
import { isNative, platform, secureForget, secureGet, secureSet } from "./native";
import { pushPayloadAsNotification } from "./pushpayload";

// DEVICE_ID_KEY holds the opaque id in the platform keystore. It has to survive app
// restarts and reinstalls-over-the-top, otherwise every launch would look like a new
// device to the daemon.
const DEVICE_ID_KEY = "notification-device-id";

export type NativePushState =
  | "unsupported"
  | "idle"
  | "enabling"
  | "enabled"
  | "denied";

// available reports whether native push can work here at all. The browser uses Web
// Push instead, which needs a service worker the WebView does not provide.
export const nativePushAvailable = isNative;

// Handlers the app supplies. Registration is a transport concern; what to do with a
// notification is not, so both are injected.
export interface PushHandlers {
  // onNavigate opens the destination a tapped notification names.
  onNavigate: (target: Target) => void;
  // onForeground is called for a notification that arrived while the app was open, so
  // the Notification Center can refresh instead of the OS interrupting over a screen
  // the user is already reading.
  onForeground: () => void;
}

let handlers: PushHandlers | null = null;
let listenersAttached = false;

// currentDeviceID returns this app's device id if it has one, without creating it.
//
// Used to tell "this device" apart from the others in the device list, so forgetting it
// also clears the local token rather than leaving the app re-registering on next launch.
export async function currentDeviceID(): Promise<string | null> {
  return (await secureGet(DEVICE_ID_KEY)) || null;
}

// deviceID returns this installation's device id, creating one on first use.
async function deviceID(): Promise<string> {
  const existing = await secureGet(DEVICE_ID_KEY);
  if (existing) return existing;
  const created = randomID();
  await secureSet(DEVICE_ID_KEY, created);
  return created;
}

// permissionState reports the OS-level permission without prompting.
//
// It is reported separately from Podiom's own preferences: the OS answers "may this app
// notify at all", Podiom answers "which events are worth notifying about", and showing
// them as one setting would leave a user unable to tell which one is stopping them.
export async function nativePermissionState(): Promise<NativePushState> {
  if (!nativePushAvailable) return "unsupported";
  try {
    const { receive } = await FirebaseMessaging.checkPermissions();
    if (receive === "granted") return "enabled";
    if (receive === "denied") return "denied";
    return "idle";
  } catch {
    return "unsupported";
  }
}

// enableNativePush asks for permission, then registers this device with the daemon.
//
// Must be called from a user action: both platforms gate the permission prompt on one,
// and asking before the user knows why is how an app gets permanently denied.
export async function enableNativePush(): Promise<NativePushState> {
  if (!nativePushAvailable) return "unsupported";
  const { receive } = await FirebaseMessaging.requestPermissions();
  if (receive !== "granted") return receive === "denied" ? "denied" : "idle";
  await registerWithDaemon();
  return "enabled";
}

// registerWithDaemon sends the current token to podiomd. Idempotent, so it is safe on
// every launch, every token refresh and every reconnect.
export async function registerWithDaemon(): Promise<void> {
  if (!nativePushAvailable) return;
  const { token } = await FirebaseMessaging.getToken();
  if (!token) return;
  await registerNotificationDevice({
    device_id: await deviceID(),
    platform: platform === "ios" ? "ios" : "android",
    label: deviceLabel(),
    push_token: token,
  });
}

// unregisterFromDaemon stops delivery to this device and forgets its token.
export async function unregisterFromDaemon(): Promise<void> {
  if (!nativePushAvailable) return;
  const id = await secureGet(DEVICE_ID_KEY);
  if (id) {
    // Best effort: an unreachable daemon must not leave the app unable to sign out of
    // notifications locally.
    try {
      await deleteNotificationDevice(id);
    } catch {
      /* the daemon prunes a token the relay reports as dead anyway */
    }
  }
  try {
    await FirebaseMessaging.deleteToken();
  } catch {
    /* already gone */
  }
  await secureForget(DEVICE_ID_KEY);
}

// startNativePush attaches the notification listeners. Idempotent.
export function startNativePush(next: PushHandlers): void {
  handlers = next;
  if (!nativePushAvailable || listenersAttached) return;
  listenersAttached = true;

  // A token can be reissued at any time — a restore, a reinstall, or the push service's
  // own rotation — and a stale token is a notification that silently goes nowhere.
  void FirebaseMessaging.addListener("tokenReceived", () => {
    void registerWithDaemon().catch(() => {
      /* retried on the next launch; nothing the user can act on */
    });
  });

  // Arrived while the app is open. The daemon has already recorded it and broadcast it
  // over the socket, so the Center updates itself — interrupting over a screen the user
  // is actively reading would be showing them the same thing twice.
  void FirebaseMessaging.addListener("notificationReceived", () => {
    handlers?.onForeground();
  });

  // Tapped, from any app state: foreground, background, or launched by the tap itself.
  // The plugin delivers this after the web layer is ready in all three cases, which is
  // what makes a cold-start deep link work.
  void FirebaseMessaging.addListener("notificationActionPerformed", (event) => {
    const data = (event.notification?.data ?? {}) as Record<string, unknown>;
    handlers?.onNavigate(targetFromNotification(pushPayloadAsNotification(data)));
  });
}

// deviceLabel names the device in Podiom's own device list, so a user with several can
// tell which is which.
function deviceLabel(): string {
  if (platform === "ios") return "iPhone or iPad";
  if (platform === "android") return "Android device";
  return "Mobile device";
}
