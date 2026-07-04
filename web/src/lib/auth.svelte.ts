// auth.svelte.ts — the browser's copy of the gateway token (HA9). Entered once
// in the token screen, remembered per browser in localStorage, and mirrored
// into IndexedDB because the service worker needs it for its "Approve" push
// action and service workers cannot read localStorage.

const STORAGE_KEY = "podiom:gateway-token";

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
  token = $state<string>(localStorage.getItem(STORAGE_KEY) ?? "");

  // setToken stores a verified token and re-mirrors it for the service worker.
  setToken(token: string) {
    this.token = token;
    localStorage.setItem(STORAGE_KEY, token);
    void mirrorToIDB(token);
  }

  // invalidate drops the stored token — called on any 401 or a 4401 WebSocket
  // close (token rotated, HA12). App.svelte reacts by showing the token screen.
  invalidate() {
    if (!this.token) return;
    this.token = "";
    localStorage.removeItem(STORAGE_KEY);
    void mirrorToIDB("");
  }
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
// via browser sync) reaches the service worker too.
if (auth.token) void mirrorToIDB(auth.token);
