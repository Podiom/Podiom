// connection.ts — the Podiom instance the native apps talk to (R7, R9).
//
// A browser gets its instance for free: the daemon served the page, so the
// origin is the answer and none of this runs. The native apps are loaded from
// the app bundle, so the address is something the user supplies once — typed or
// picked from local discovery — and is remembered between launches. The token
// itself is owned by auth.svelte.ts; this module owns the address and the
// boot-time hydration that applies both.

import { auth, readStoredToken, TOKEN_HEADER } from "./auth.svelte";
import { setEndpoint } from "./base";
import { isNative, secureForget, secureGet, secureSet } from "./native";

const ADDRESS_KEY = "instance-address";

// PROBE_TIMEOUT_MS bounds each setup request. A wrong address on a LAN
// typically hangs rather than refusing, so without this the connection screen
// would sit on "validating" until the platform's own much longer timeout.
const PROBE_TIMEOUT_MS = 6000;

export type ProbeFailure = "unreachable" | "not-podiom" | "token-rejected";

export interface ProbeResult {
  ok: boolean;
  reason?: ProbeFailure;
  version?: string;
}

// normalizeAddress turns what the user typed into a base URL suitable for
// resolving relative API paths against. Throws if it is not a usable URL.
export function normalizeAddress(input: string): URL {
  const trimmed = input.trim();
  if (!trimmed) throw new Error("empty address");
  // A bare host or host:port has no scheme. Default to http: podiomd serves
  // plain HTTP, and an instance behind TLS is entered with an explicit https://.
  const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed) ? trimmed : `http://${trimmed}`;
  const url = new URL(withScheme);
  // A trailing slash makes the path a directory, so new URL("api/x", base)
  // keeps any sub-path the user gave (e.g. a reverse proxy mounting Podiom at
  // /podiom/) instead of replacing it.
  if (!url.pathname.endsWith("/")) url.pathname += "/";
  url.hash = "";
  url.search = "";
  return url;
}

// probe is the connection screen's validator: it proves the address is a
// reachable Podiom instance and that the token is one it accepts, before either
// is stored. The two checks are separate so the screen can tell the user which
// half is wrong.
export async function probe(address: URL, token: string): Promise<ProbeResult> {
  let version: string;
  let health: Response;
  try {
    // /healthz is unauthenticated, so this isolates reachability from auth.
    health = await fetch(new URL("healthz", address), {
      signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
    });
  } catch {
    return { ok: false, reason: "unreachable" };
  }
  // Receiving any HTTP response proves reachability. A missing endpoint, HTML
  // shell, or malformed payload instead proves this is not Podiom's API.
  if (!health.ok) return { ok: false, reason: "not-podiom" };

  let body: { status?: string; version?: string };
  try {
    body = (await health.json()) as { status?: string; version?: string };
  } catch {
    // A Home Assistant sidebar URL answers with the HA HTML shell. It is
    // reachable, but it is not a direct Podiom API endpoint.
    return { ok: false, reason: "not-podiom" };
  }
  // Something answered, but anything can answer an HTTP GET. Only the shape
  // of Podiom's own health response proves what this is.
  if (body?.status !== "ok" || typeof body.version !== "string") {
    return { ok: false, reason: "not-podiom" };
  }
  version = body.version;

  try {
    const res = await fetch(new URL("api/auth/check", address), {
      headers: { [TOKEN_HEADER]: token },
      signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
    });
    if (res.status === 401) return { ok: false, reason: "token-rejected" };
    if (!res.ok) return { ok: false, reason: "unreachable" };
  } catch {
    return { ok: false, reason: "unreachable" };
  }

  return { ok: true, version };
}

// hydrate restores the stored instance before the app mounts. Nothing may issue
// a request until this resolves: until setEndpoint runs, apiUrl() would resolve
// against the WebView's own origin rather than the daemon.
export async function hydrate(): Promise<void> {
  if (!isNative) return;
  const stored = await secureGet(ADDRESS_KEY);
  if (!stored) return;
  try {
    setEndpoint(normalizeAddress(stored));
  } catch {
    // A stored address we can no longer parse is not recoverable; drop it and
    // let the connection screen ask again.
    void secureForget(ADDRESS_KEY);
    return;
  }
  auth.hydrate(await readStoredToken());
}

// save persists a validated address/token pair and makes it live.
export async function save(address: URL, token: string): Promise<void> {
  setEndpoint(address);
  await secureSet(ADDRESS_KEY, address.toString());
  auth.setToken(token);
}

// clear forgets the instance entirely, returning the app to the connection
// screen with nothing pre-filled — the "connect to a different Podiom" path.
// Distinct from auth.invalidate(), which keeps the address and asks only for a
// fresh token.
export async function clear(): Promise<void> {
  auth.invalidate();
  setEndpoint(null);
  await secureForget(ADDRESS_KEY);
}

// storedAddress reports the configured address as the user would type it, for
// pre-filling the connection screen after a token rotation.
export async function storedAddress(): Promise<string> {
  if (!isNative) return "";
  return secureGet(ADDRESS_KEY);
}
