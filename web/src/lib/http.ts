// http.ts — the single fetch wrapper. Every REST call goes through request()
// so two cross-cutting concerns live in one place: URLs resolve against the
// app's base (sub-path safety under HA Ingress, HA14) and the gateway token
// rides every request (HA7). A 401 on the API surface drops the stored token,
// which sends the user back to the token screen (HA12).

import { auth, TOKEN_HEADER } from "./auth.svelte";
import { apiUrl } from "./base";

export async function request(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  if (auth.token) headers.set(TOKEN_HEADER, auth.token);
  const res = await fetch(apiUrl(path), { ...init, headers });
  if (res.status === 401 && path.replace(/^\//, "").startsWith("api/")) {
    auth.invalidate();
  }
  return res;
}

// verifyToken checks a candidate against the daemon before storing it — the
// token screen's submit action. Uses a raw fetch so a bad candidate does not
// disturb any currently stored token.
export async function verifyToken(candidate: string): Promise<boolean> {
  try {
    const res = await fetch(apiUrl("api/auth/check"), {
      headers: { [TOKEN_HEADER]: candidate },
    });
    return res.ok;
  } catch {
    return false;
  }
}
