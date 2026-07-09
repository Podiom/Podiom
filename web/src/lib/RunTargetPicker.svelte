<script lang="ts">
  import { capabilityKey, effortOptions, loadProviderCapabilities, modelOptions } from "./capabilities";
  import type { Agent, ProfileInfo, Provider, ProviderCapabilities } from "./types";

  export interface RunTargetValue {
    provider?: Provider | "";
    profile?: string;
    model?: string;
    effort?: string;
  }

  let {
    agent = null,
    profiles = [],
    value = {},
    onChange = (_value: RunTargetValue) => {},
    readonlyAccount = false,
    variant = "inline",
  }: {
    agent?: Agent | null;
    profiles?: ProfileInfo[];
    value?: RunTargetValue;
    onChange?: (value: RunTargetValue) => void;
    readonlyAccount?: boolean;
    variant?: "inline" | "stacked";
  } = $props();

  let open = $state<string | null>(null);
  let customModel = $state("");
  let capabilitiesByKey = $state<Record<string, ProviderCapabilities>>({});
  let loading = new Set<string>();

  const explicit = $derived(!!(value.provider || value.profile || value.model || value.effort));
  const effectiveProvider = $derived((value.provider || agent?.Provider || "claude") as Provider);
  const effectiveProfile = $derived(value.profile ?? (value.provider ? "" : agent?.Profile ?? ""));
  const effectiveModel = $derived(value.model || agent?.Model || "");
  const effectiveEffort = $derived(value.effort || agent?.Effort || "medium");
  const accountChoices = $derived(buildAccountChoices(agent, profiles));
  const showAccount = $derived(accountChoices.length > 1);
  const capKey = $derived(capabilityKey(effectiveProvider, effectiveProfile));
  const caps = $derived(capabilitiesByKey[capKey] ?? null);
  const modelList = $derived(modelOptions(caps, effectiveModel));
  const effortList = $derived(effortOptions(caps, effectiveModel, effectiveEffort));
  const accountLabel = $derived(
    !explicit ? "Agent default" : effectiveProfile ? `${effectiveProvider} / ${effectiveProfile}` : effectiveProvider,
  );

  $effect(() => {
    void ensureCapabilities(effectiveProvider, effectiveProfile);
  });

  async function ensureCapabilities(provider: Provider, profile = "") {
    const key = capabilityKey(provider, profile);
    if (capabilitiesByKey[key] || loading.has(key)) return;
    loading.add(key);
    try {
      const next = await loadProviderCapabilities(provider, profile);
      capabilitiesByKey = { ...capabilitiesByKey, [key]: next };
    } catch {
      // Keep the free-form model input usable if capability loading fails.
    } finally {
      loading.delete(key);
    }
  }

  function emit(patch: RunTargetValue) {
    onChange({ ...value, ...patch });
  }

  function reset() {
    open = null;
    onChange({ provider: "", profile: "", model: "", effort: "" });
  }

  function chooseAccount(provider: Provider | "", profile = "") {
    if (!provider) {
      reset();
      return;
    }
    emit({
      provider,
      profile,
      model: value.model || effectiveModel,
      effort: value.effort || effectiveEffort,
    });
    open = null;
  }

  function chooseModel(model: string) {
    emit({ model, effort: value.effort || effectiveEffort });
    open = null;
  }

  function chooseEffort(effort: string) {
    emit({ model: value.model || effectiveModel, effort });
    open = null;
  }

  function applyCustomModel() {
    const model = customModel.trim();
    if (model) chooseModel(model);
  }

  function toggle(which: string) {
    open = open === which ? null : which;
    if (open === "model") customModel = effectiveModel;
  }

  function buildAccountChoices(agent: Agent | null, profiles: ProfileInfo[]) {
    const seen = new Set<string>();
    const out: { key: string; label: string; provider: Provider | ""; profile: string; default?: boolean }[] = [];
    const add = (provider: Provider | "", profile = "", label = "", def = false) => {
      const key = `${provider}:${profile}:${def ? "default" : ""}`;
      if (seen.has(key)) return;
      seen.add(key);
      out.push({ key, label: label || (profile ? `${provider} / ${profile}` : provider), provider, profile, default: def });
    };
    add("", "", "Agent default", true);
    if (agent) add(agent.Provider, "", `${agent.Provider} default`);
    add("claude", "", "Claude default");
    add("codex", "", "Codex default");
    for (const p of profiles) add(p.Provider, p.Name);
    return out;
  }
</script>

<div class:stacked={variant === "stacked"} class="run-target">
  {#if !readonlyAccount && showAccount}
    <div class="rt-dd">
      <button class="rt-chip account" type="button" onclick={() => toggle("account")}>
        <span class="rt-dot" class:codex={effectiveProvider === "codex"}></span>
        {accountLabel}<span class="chev">▾</span>
      </button>
      {#if open === "account"}
        <div class="rt-menu">
          {#each accountChoices as choice}
            <button class="rt-opt" class:sel={choice.default ? !explicit : choice.provider === effectiveProvider && choice.profile === effectiveProfile && explicit} type="button" onclick={() => chooseAccount(choice.provider, choice.profile)}>
              {choice.label}
            </button>
          {/each}
        </div>
      {/if}
    </div>
  {:else}
    <span class="rt-chip account readonly">
      <span class="rt-dot" class:codex={effectiveProvider === "codex"}></span>
      {accountLabel}
    </span>
  {/if}

  <div class="rt-dd">
    <button class="rt-chip mono" type="button" onclick={() => toggle("model")}>{effectiveModel || "model"}<span class="chev">▾</span></button>
    {#if open === "model"}
      <div class="rt-menu">
        {#each modelList as model}
          <button class="rt-opt mono" class:sel={model === effectiveModel} type="button" onclick={() => chooseModel(model)}>{model}</button>
        {/each}
        <div class="custom">
          <input class="rt-input mono" bind:value={customModel} placeholder="custom model" onkeydown={(e) => { if (e.key === "Enter") applyCustomModel(); }} />
          <button class="rt-set" type="button" disabled={!customModel.trim()} onclick={applyCustomModel}>Set</button>
        </div>
      </div>
    {/if}
  </div>

  <div class="rt-dd">
    <button class="rt-chip mono" type="button" onclick={() => toggle("effort")}>{effectiveEffort}<span class="chev">▾</span></button>
    {#if open === "effort"}
      <div class="rt-menu">
        {#each effortList as effort}
          <button class="rt-opt mono" class:sel={effort === effectiveEffort} type="button" onclick={() => chooseEffort(effort)}>{effort}</button>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
  .run-target {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
    align-items: center;
  }

  .run-target.stacked {
    align-items: stretch;
  }

  .run-target.stacked .rt-dd,
  .run-target.stacked .rt-chip {
    flex: 1 1 150px;
  }

  .rt-dd {
    position: relative;
  }

  .rt-chip {
    min-height: 34px;
    border: 1px solid #e9ded0;
    background: #fffaf4;
    color: #4f473f;
    border-radius: 10px;
    padding: 7px 11px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    font: 650 12.5px "Hanken Grotesk";
    cursor: pointer;
    white-space: nowrap;
  }

  .rt-chip.readonly {
    cursor: default;
    color: #7a6e63;
    background: #fbf6ef;
  }

  .mono {
    font-family: "JetBrains Mono", monospace;
    font-size: 12px;
  }

  .rt-dot {
    width: 9px;
    height: 9px;
    border-radius: 999px;
    background: #b0572f;
    flex: none;
  }

  .rt-dot.codex {
    border-radius: 2px;
    background: #4b5560;
    transform: rotate(45deg);
  }

  .chev {
    opacity: 0.52;
    font-size: 11px;
  }

  .rt-menu {
    position: absolute;
    z-index: 40;
    left: 0;
    bottom: calc(100% + 7px);
    min-width: 190px;
    max-height: 280px;
    overflow: auto;
    padding: 6px;
    border: 1px solid #eadfce;
    border-radius: 12px;
    background: #fffdf9;
    box-shadow: 0 18px 42px rgba(43, 37, 32, 0.16);
  }

  .rt-opt {
    width: 100%;
    border: 0;
    background: transparent;
    color: #4f473f;
    text-align: left;
    border-radius: 8px;
    padding: 8px 10px;
    cursor: pointer;
    font: 600 12.5px "Hanken Grotesk";
  }

  .rt-opt:hover,
  .rt-opt.sel {
    background: #edf5f1;
    color: #2f6e60;
  }

  .custom {
    display: flex;
    gap: 6px;
    padding: 8px 2px 2px;
    margin-top: 4px;
    border-top: 1px solid #f0e5d8;
  }

  .rt-input {
    min-width: 0;
    flex: 1;
    border: 1px solid #e8dccd;
    border-radius: 8px;
    padding: 7px 8px;
    background: #fff;
    color: #3f3933;
  }

  .rt-set {
    border: 1px solid #cbe1d8;
    border-radius: 8px;
    background: #e8f4ef;
    color: #2f6e60;
    font: 700 12px "Hanken Grotesk";
    padding: 0 10px;
    cursor: pointer;
  }

  .rt-set:disabled {
    opacity: 0.45;
    cursor: default;
  }
</style>
