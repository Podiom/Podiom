// base.ts — the SPA's own base URL. The app cannot assume it lives at the
// origin root: behind HA Ingress it is served under a rewritten sub-path
// (/api/hassio_ingress/<token>/...), where the daemon injects a <base href>
// derived from X-Ingress-Path into index.html. document.baseURI is therefore
// authoritative in every deployment, and all API / WebSocket / service-worker
// URLs must derive from it (HA14).

export const appBase = new URL(".", document.baseURI);

// apiUrl resolves a path like "/api/agents" (or "api/agents") against the
// app's base rather than the origin root.
export function apiUrl(path: string): URL {
  return new URL(path.replace(/^\//, ""), appBase);
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
