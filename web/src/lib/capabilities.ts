import { getProviderCapabilities } from "./api";
import type { EffortOption, ModelOption, Provider, ProviderCapabilities } from "./types";

const cache = new Map<string, Promise<ProviderCapabilities>>();

export function capabilityKey(provider: Provider | string, profile = ""): string {
  return `${provider || "claude"}|${profile}`;
}

export function loadProviderCapabilities(
  provider: Provider,
  profile = "",
  refresh = false,
): Promise<ProviderCapabilities> {
  const key = capabilityKey(provider, profile);
  if (!refresh && cache.has(key)) return cache.get(key)!;
  const promise = getProviderCapabilities(provider, profile, refresh).catch((err) => {
    cache.delete(key);
    throw err;
  });
  cache.set(key, promise);
  return promise;
}

export function modelOptions(caps: ProviderCapabilities | null | undefined, current = ""): string[] {
  const options = (caps?.models ?? []).map((m) => m.model).filter(Boolean);
  return includeCurrent(options, current);
}

export function effortOptions(
  caps: ProviderCapabilities | null | undefined,
  model = "",
  current = "",
): string[] {
  const selected = findModel(caps, model);
  const efforts = selected?.supported_efforts?.length ? selected.supported_efforts : caps?.efforts ?? [];
  return includeCurrent(efforts.map((e) => e.effort).filter(Boolean), current);
}

export function defaultEffort(caps: ProviderCapabilities | null | undefined, model = ""): string {
  const selected = findModel(caps, model);
  return selected?.default_reasoning_effort || caps?.efforts?.[0]?.effort || "medium";
}

export function modelMeta(caps: ProviderCapabilities | null | undefined, model: string): ModelOption | undefined {
  return findModel(caps, model);
}

export function effortMeta(
  caps: ProviderCapabilities | null | undefined,
  model: string,
  effort: string,
): EffortOption | undefined {
  const selected = findModel(caps, model);
  return (selected?.supported_efforts?.length ? selected.supported_efforts : caps?.efforts ?? []).find(
    (e) => e.effort === effort,
  );
}

function findModel(caps: ProviderCapabilities | null | undefined, model: string): ModelOption | undefined {
  if (!model) return undefined;
  return caps?.models?.find((m) => m.model === model);
}

function includeCurrent(options: string[], current: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of current ? [current, ...options] : options) {
    if (!value || seen.has(value)) continue;
    seen.add(value);
    out.push(value);
  }
  return out;
}
