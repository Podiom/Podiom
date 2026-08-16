// discovery.ts — finding Podiom instances on the local network (R8).
//
// The browse itself is native: mDNS/DNS-SD needs multicast sockets that a
// WebView has no access to, so it runs behind a small first-party Capacitor
// plugin (plugins/podiom-discovery) over Bonjour on iOS and NsdManager on
// Android. This module is the only thing the UI talks to, so the whole feature
// degrades to "no instances found" in a browser rather than needing the
// connection screen to know which platform it is on.

import { registerPlugin } from "@capacitor/core";
import { isNative } from "./native";

export interface DiscoveredInstance {
  // name is the human label from the service instance, e.g. "Podiom on MacBook".
  name: string;
  host: string;
  port: number;
  // version comes from the service's TXT record when the responder published
  // one. Advisory only — the address is still probed before it is stored.
  version?: string;
}

interface PodiomDiscoveryPlugin {
  discover(options: { timeoutMs: number }): Promise<{ instances: DiscoveredInstance[] }>;
}

const plugin = registerPlugin<PodiomDiscoveryPlugin>("PodiomDiscovery", {
  // Browsers cannot browse mDNS. Returning nothing keeps the "Search again"
  // and manual-entry paths on the connection screen working unchanged.
  web: () => ({ discover: async () => ({ instances: [] }) }),
});

// available reports whether searching can find anything at all. The connection
// screen hides the search action when it cannot.
export const available = isNative;

// DISCOVER_TIMEOUT_MS bounds the browse. mDNS has no "done" signal — responders
// simply stop answering — so the search runs for a fixed window and reports
// whatever answered.
const DISCOVER_TIMEOUT_MS = 4000;

export async function discover(timeoutMs = DISCOVER_TIMEOUT_MS): Promise<DiscoveredInstance[]> {
  if (!isNative) return [];
  try {
    const { instances } = await plugin.discover({ timeoutMs });
    return instances ?? [];
  } catch {
    // A denied local-network permission surfaces here. The user still has
    // manual entry, which R8 requires stay available when discovery fails.
    return [];
  }
}

// addressFor renders a discovered instance as the address the user would have
// typed, so selecting one fills the same field by the same rules.
export function addressFor(instance: DiscoveredInstance): string {
  return `http://${instance.host}:${instance.port}`;
}
