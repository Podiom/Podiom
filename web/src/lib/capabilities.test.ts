import { describe, expect, it, vi } from "vitest";

import { capabilityKey, defaultEffort, effortMeta, effortOptions, modelMeta, modelOptions } from "./capabilities";
import type { ProviderCapabilities } from "./types";

const DEFAULT_PROVIDER = "codex";

vi.mock("./api", () => ({
  getProviderCapabilities: vi.fn(),
}));

vi.mock("./providers", () => ({
  DEFAULT_PROVIDER: "codex",
}));

describe("capabilityKey", () => {
  it("falls back to the default provider for an empty provider", () => {
    expect(capabilityKey("", "")).toBe(`${DEFAULT_PROVIDER}|`);
  });

  it("includes the selected provider and profile", () => {
    expect(capabilityKey("codex", "work")).toBe("codex|work");
  });
});

describe("modelOptions", () => {
  it("returns an empty list while capabilities are loading", () => {
    expect(modelOptions(null)).toEqual([]);
  });

  it("keeps a current model that is no longer advertised", () => {
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5" }, { model: "gpt-5-mini" }],
      efforts: [],
    };

    expect(modelOptions(caps, "legacy-model")).toEqual(["legacy-model", "gpt-5", "gpt-5-mini"]);
  });

  it("does not duplicate a current model already in the list", () => {
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5" }, { model: "gpt-5-mini" }],
      efforts: [],
    };

    expect(modelOptions(caps, "gpt-5-mini")).toEqual(["gpt-5-mini", "gpt-5"]);
  });
});

describe("effortOptions", () => {
  it("returns an empty list while capabilities are loading", () => {
    expect(effortOptions(null)).toEqual([]);
  });

  it("uses the selected model's supported efforts", () => {
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5", supported_efforts: [{ effort: "low" }, { effort: "high" }] }],
      efforts: [{ effort: "medium" }],
    };

    expect(effortOptions(caps, "gpt-5")).toEqual(["low", "high"]);
  });

  it("falls back to provider efforts when the selected model has no efforts", () => {
    // An empty supported_efforts array means the model has no model-specific opinion.
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5", supported_efforts: [] }],
      efforts: [{ effort: "medium" }, { effort: "high" }],
    };

    expect(effortOptions(caps, "gpt-5")).toEqual(["medium", "high"]);
  });

  it("keeps a current effort that is no longer advertised", () => {
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5" }],
      efforts: [{ effort: "medium" }, { effort: "high" }],
    };

    expect(effortOptions(caps, "gpt-5", "minimal")).toEqual(["minimal", "medium", "high"]);
  });
});

describe("defaultEffort", () => {
  it("uses the selected model's default effort", () => {
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5", default_reasoning_effort: "high" }],
      efforts: [{ effort: "medium" }],
    };

    expect(defaultEffort(caps, "gpt-5")).toBe("high");
  });

  it("falls back to the first provider effort", () => {
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5" }],
      efforts: [{ effort: "low" }, { effort: "medium" }],
    };

    expect(defaultEffort(caps, "gpt-5")).toBe("low");
  });

  it("falls back to medium when capabilities are missing", () => {
    expect(defaultEffort(null)).toBe("medium");
  });
});

describe("modelMeta and effortMeta", () => {
  it("returns model metadata when present", () => {
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5", display_name: "GPT-5" }],
      efforts: [],
    };

    expect(modelMeta(caps, "gpt-5")).toEqual({ model: "gpt-5", display_name: "GPT-5" });
  });

  it("returns undefined for a missing model", () => {
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5" }],
      efforts: [],
    };

    expect(modelMeta(caps, "missing")).toBeUndefined();
  });

  it("returns effort metadata from model-specific efforts", () => {
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5", supported_efforts: [{ effort: "high", description: "More thinking" }] }],
      efforts: [{ effort: "medium" }],
    };

    expect(effortMeta(caps, "gpt-5", "high")).toEqual({ effort: "high", description: "More thinking" });
  });

  it("returns undefined for a missing effort", () => {
    const caps: ProviderCapabilities = {
      provider: "codex",
      source: "live",
      fetched_at: "",
      stale: false,
      models: [{ model: "gpt-5" }],
      efforts: [{ effort: "medium" }],
    };

    expect(effortMeta(caps, "gpt-5", "missing")).toBeUndefined();
  });
});
