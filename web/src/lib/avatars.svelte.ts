// avatars.svelte.ts — shared client-side registry of agent profile pictures.
//
// Two problems this solves:
//  1. Most render sites only have an agent NAME (e.g. a schedule row), not the
//     full agent record, so they can't tell whether a picture exists. We keep a
//     reactive name→version map, populated from the agents list App.svelte
//     already loads, that any <AgentAvatar> can consult by name.
//  2. /api is token-gated, so a plain <img src="/api/.../avatar"> is rejected.
//     We fetch the bytes once (via the authed request helper) and hand out an
//     object URL, cached by name+version so all ~15 render sites share one blob
//     and a re-upload (new version) transparently cache-busts.

import { untrack } from "svelte";
import { fetchAgentAvatar } from "./api";
import type { Agent } from "./types";

class AvatarStore {
  // name → AvatarUpdatedAt version stamp ("" / absent = no picture).
  private versions = $state<Record<string, string>>({});
  // "name|version" → resolved object URL (or in-flight promise).
  private urls = new Map<string, string>();
  private loading = new Map<string, Promise<string | null>>();

  // syncFromAgents refreshes the version map from the canonical agents list.
  // Revokes object URLs whose agent's version changed (re-upload / removal) so
  // the next lookup re-fetches and we don't leak blobs.
  syncFromAgents(agents: Agent[]) {
    const next: Record<string, string> = {};
    for (const a of agents) next[a.Name] = a.AvatarUpdatedAt ?? "";
    // Read the current map without subscribing: callers run this inside an
    // $effect keyed on the agents list, so if diffing tracked `versions` too,
    // our own write below would re-trigger that effect forever
    // (effect_update_depth_exceeded).
    const prevVersions = untrack(() => this.versions);
    let changed = false;
    for (const [name, prev] of Object.entries(prevVersions)) {
      if (next[name] !== prev) {
        this.revoke(name, prev);
        changed = true;
      }
    }
    // Also treat newly-added agents as a change so the map actually updates.
    if (!changed) {
      for (const name of Object.keys(next)) {
        if (!(name in prevVersions)) {
          changed = true;
          break;
        }
      }
    }
    if (changed) this.versions = next;
  }

  // setVersion updates one agent's stamp directly, for immediate feedback right
  // after an upload/removal (before the canonical agents list round-trips).
  setVersion(name: string, version: string) {
    const prev = this.versions[name] ?? "";
    if (prev === version) return;
    this.revoke(name, prev);
    this.versions = { ...this.versions, [name]: version };
  }

  // version returns the current picture stamp for an agent ("" = none). Reading
  // it in a component makes that component reactive to uploads/removals.
  version(name: string): string {
    return this.versions[name] ?? "";
  }

  // url resolves (and caches) the object URL for an agent's picture, or null if
  // the agent has no picture. Callers pass the version they observed so a stale
  // in-flight load for an old version can't win.
  async url(name: string, version: string): Promise<string | null> {
    if (!version) return null;
    const key = `${name}|${version}`;
    const cached = this.urls.get(key);
    if (cached) return cached;
    const inflight = this.loading.get(key);
    if (inflight) return inflight;

    const p = (async () => {
      try {
        const blob = await fetchAgentAvatar(name);
        const objectUrl = URL.createObjectURL(blob);
        this.urls.set(key, objectUrl);
        return objectUrl;
      } catch {
        return null;
      } finally {
        this.loading.delete(key);
      }
    })();
    this.loading.set(key, p);
    return p;
  }

  private revoke(name: string, version: string) {
    const key = `${name}|${version}`;
    const url = this.urls.get(key);
    if (url) {
      URL.revokeObjectURL(url);
      this.urls.delete(key);
    }
  }
}

export const avatars = new AvatarStore();
