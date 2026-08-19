// reachability.ts — naming the reason a health probe failed.
//
// Kept apart from the store in reachability.svelte.ts so it can be tested as a
// plain function: the classification is the part with cases worth pinning down,
// and it needs neither runes nor a fetch.
//
// The distinction the reasons draw is the whole point of the offline screen. A
// phone in airplane mode, a sleeping Mac, and a daemon that accepted the
// connection then stopped answering are three different problems with three
// different things for the user to do about them, and "could not connect" tells
// them none of that.

export type OfflineReason = "no-network" | "timeout" | "unreachable";

// Probe is what came back from one attempt: a response arrived, or the fetch
// rejected. A rejection carries the error because which rejection it was is the
// only signal separating a hung daemon from an absent one.
export type Probe = { status: number } | { error: unknown };

export interface Verdict {
  reason: OfflineReason;
  // code is the errno-style label the diagnostics row shows. Self-hosters read
  // it: ECONNREFUSED sends them to look at the daemon, ENETDOWN at the phone.
  code: string;
}

// reachable reports whether a probe succeeded outright, so callers do not have
// to remember that an HTTP error is still a failure here.
export function reachable(probe: Probe): boolean {
  return "status" in probe && probe.status >= 200 && probe.status < 300;
}

// classify decides which failure the screen describes.
//
// online comes from navigator.onLine, which in both WebViews tracks whether the
// device has a network interface at all. That is a weaker claim than "the
// internet works" — but it is exactly the claim being made, and it outranks
// anything the probe found: with no interface, the fetch never left the phone,
// so its error says nothing about the gateway.
export function classify(online: boolean, probe: Probe): Verdict {
  if (!online) return { reason: "no-network", code: "ENETDOWN" };

  if ("status" in probe) {
    // Something answered on the address and refused to call itself healthy.
    // The gateway is reachable, so this is not a network problem — report what
    // it said and let the user judge.
    return { reason: "unreachable", code: `HTTP ${probe.status}` };
  }

  // Our own AbortSignal.timeout fired: the request was still outstanding when
  // the budget ran out. A daemon that is simply gone refuses immediately
  // instead, so this is the hung case.
  if (isAbort(probe.error)) return { reason: "timeout", code: "ETIMEDOUT" };

  // Anything else is a transport failure before any HTTP happened — refused,
  // no route, DNS, TLS. ECONNREFUSED is the honest common case and the one the
  // copy is written for.
  return { reason: "unreachable", code: "ECONNREFUSED" };
}

function isAbort(err: unknown): boolean {
  return err instanceof DOMException ? err.name === "AbortError" : (err as Error)?.name === "AbortError";
}
