// auth.svelte.ts — this client's copy of the gateway token (HA9). Entered once
// in the token screen, remembered per browser in localStorage, and mirrored
// into IndexedDB because the service worker needs it for its "Approve" push
// action and service workers cannot read localStorage.
//
// The native apps persist it in the platform keystore instead (R7). That store
// is async, so there the token starts empty and connection.hydrate() fills it
// before the app mounts; localStorage and the IndexedDB mirror are both unused
// on native, the latter because there is no service worker.

import { isNative, secureForget, secureGet, secureSet } from "./native";

const STORAGE_KEY = "podiom:gateway-token";

// SECURE_KEY is the keystore entry backing STORAGE_KEY on native.
export const SECURE_TOKEN_KEY = "gateway-token";

// Shared with the service worker (sw.js reads the same DB/store/key).
const IDB_NAME = "podiom";
const IDB_STORE = "auth";
const IDB_KEY = "gateway-token";

// TOKEN_HEADER matches gateway.Header on the daemon.
export const TOKEN_HEADER = "X-Podiom-Token";

// WS_PROTOCOL / wsTokenProtocol mirror the daemon's Sec-WebSocket-Protocol
// contract: the browser WebSocket API cannot set headers, so the token rides
// the subprotocol list on the handshake.
export const WS_PROTOCOL = "podiom.v1";
export function wsTokenProtocol(token: string): string {
  return `podiom-token.${token}`;
}

class AuthStore {
  token = $state<string>(isNative ? "" : (localStorage.getItem(STORAGE_KEY) ?? ""));

  // hydrate seeds the token read from the keystore during native boot. It does
  // not persist: the value came from storage, so writing it back is pointless.
  hydrate(token: string) {
    this.token = token;
  }

  // setToken stores a verified token and re-mirrors it for the service worker.
  setToken(token: string) {
    this.token = token;
    if (isNative) {
      void secureSet(SECURE_TOKEN_KEY, token);
      return;
    }
    localStorage.setItem(STORAGE_KEY, token);
    void mirrorToIDB(token);
  }

  // invalidate drops the stored token — called on any 401 or a 4401 WebSocket
  // close (token rotated, HA12). App.svelte reacts by showing the token screen.
  // The configured address deliberately survives on native: the instance is
  // still the right one, only its token has gone stale (R7).
  invalidate() {
    if (!this.token) return;
    this.token = "";
    if (isNative) {
      void secureForget(SECURE_TOKEN_KEY);
      return;
    }
    localStorage.removeItem(STORAGE_KEY);
    void mirrorToIDB("");
  }
}

// readStoredToken returns the persisted native token. Used only by
// connection.hydrate() during boot.
export function readStoredToken(): Promise<string> {
  return secureGet(SECURE_TOKEN_KEY);
}

async function mirrorToIDB(token: string): Promise<void> {
  try {
    const db = await new Promise<IDBDatabase>((resolve, reject) => {
      const req = indexedDB.open(IDB_NAME, 1);
      req.onupgradeneeded = () => {
        if (!req.result.objectStoreNames.contains(IDB_STORE)) {
          req.result.createObjectStore(IDB_STORE);
        }
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction(IDB_STORE, "readwrite");
      tx.objectStore(IDB_STORE).put(token, IDB_KEY);
      tx.oncomplete = () => resolve();
      tx.onerror = () => reject(tx.error);
    });
    db.close();
  } catch {
    // Best-effort: without the mirror the SW's approve action falls back to
    // focusing the app, which then shows the token screen if needed.
  }
}

export const auth = new AuthStore();

// Sync the mirror on boot so a token entered before this build (or restored
// via browser sync) reaches the service worker too. Browser-only: native has no
// service worker to feed.
if (!isNative && auth.token) void mirrorToIDB(auth.token);
