// base.ts — the SPA's own base URL. The app cannot assume it lives at the
// origin root: behind HA Ingress it is served under a rewritten sub-path
// (/api/hassio_ingress/<token>/...), where the daemon injects a <base href>
// derived from X-Ingress-Path into index.html. document.baseURI is therefore
// authoritative in every browser deployment, and all API / WebSocket /
// service-worker URLs must derive from it (HA14).
//
// The native apps are the one exception: they load the SPA from the app bundle,
// so document.baseURI is the WebView's own origin and the daemon lives
// elsewhere on the network. There the connection screen supplies the base
// instead — see setEndpoint below.

// configured is the Podiom instance chosen on the connection screen. It is set
// only in the native apps: there the SPA is served from the app bundle
// (capacitor://localhost), so document.baseURI points at the phone itself
// rather than at a daemon. In a browser it stays null and every URL keeps
// deriving from document.baseURI exactly as before.
let configured: URL | null = null;

// setEndpoint installs (or clears) the configured instance. Called once during
// boot from the persisted connection, and again whenever the user connects to
// a different instance.
export function setEndpoint(url: URL | null): void {
  configured = url;
}

// endpoint reports the configured instance, or null in the browser.
export function endpoint(): URL | null {
  return configured;
}

export function appBase(): URL {
  return configured ?? new URL(".", document.baseURI);
}

// apiUrl resolves a path like "/api/agents" (or "api/agents") against the
// app's base rather than the origin root.
export function apiUrl(path: string): URL {
  return new URL(path.replace(/^\//, ""), appBase());
}

// wsUrl is the WebSocket endpoint derived from the page's own location/ingress
// path — never hard-coded against the origin (HA14).
export function wsUrl(): string {
  const url = apiUrl("api/ws");
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  return url.toString();
}

export type Deployment = "ha" | "standalone";

// deployment reads the hint the daemon injects into index.html. It only
// selects which token-retrieval instructions the token screen shows (HA10) —
// nothing security-relevant hangs off it.
export function deployment(): Deployment {
  const meta = document.querySelector('meta[name="podiom-deployment"]');
  return meta?.getAttribute("content") === "ha" ? "ha" : "standalone";
}
