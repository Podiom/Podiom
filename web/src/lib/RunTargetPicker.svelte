<script lang="ts">
  import {
    capabilityKey,
    effortMeta,
    effortOptions,
    loadProviderCapabilities,
    modelMeta,
    modelOptions,
  } from "./capabilities";
  import ProviderLogo from "./ProviderLogo.svelte";
  import { DEFAULT_PROVIDER, PROVIDERS, providerMeta } from "./providers";
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
  const effectiveProvider = $derived((value.provider || agent?.Provider || DEFAULT_PROVIDER) as Provider);
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

  // Rich labels/rows used by the stacked (creation) layout.
  const accountDisplay = $derived(
    !explicit
      ? "Agent default"
      : effectiveProfile
        ? `${cap(effectiveProvider)} / ${effectiveProfile}`
        : `${cap(effectiveProvider)} · default`,
  );
  const modelDisplay = $derived(modelMeta(caps, effectiveModel)?.display_name || effectiveModel || "model");
  const modelRows = $derived(
    modelList.map((model) => {
      const meta = modelMeta(caps, model);
      return {
        model,
        name: meta?.display_name || model,
        desc: meta?.description || "",
        def: !!meta?.is_default,
        selected: model === effectiveModel,
      };
    }),
  );
  const effortRows = $derived(
    effortList.map((effort) => ({
      effort,
      label: cap(effort),
      desc: effortMeta(caps, effectiveModel, effort)?.description || "",
      dot: effortDot(effort),
      selected: effort === effectiveEffort,
    })),
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

  function cap(x: string) {
    return x ? x.charAt(0).toUpperCase() + x.slice(1) : x;
  }

  function effortDot(effort: string) {
    switch (effort) {
      case "low":
      case "minimal":
        return "#c99a5b";
      case "high":
      case "xhigh":
      case "max":
        return "#b06a3c";
      default:
        return "#3f8f7e";
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
    const out: {
      key: string;
      label: string;
      name: string;
      sub: string;
      provider: Provider | "";
      profile: string;
      default?: boolean;
    }[] = [];
    const add = (provider: Provider | "", profile = "", label = "", def = false, name = "", sub = "") => {
      const key = `${provider}:${profile}:${def ? "default" : ""}`;
      if (seen.has(key)) return;
      seen.add(key);
      out.push({
        key,
        label: label || (profile ? `${provider} / ${profile}` : provider),
        name: name || (profile ? cap(provider) : cap(provider)),
        sub,
        provider,
        profile,
        default: def,
      });
    };
    add("", "", "Agent default", true, "Agent default", "Inherit colleague settings");
    if (agent) add(agent.Provider, "", `${agent.Provider} default`, false, cap(agent.Provider), "Default account");
    for (const p of PROVIDERS) add(p.id, "", `${p.label} default`, false, p.label, "Default account");
    for (const p of profiles) add(p.Provider, p.Name, "", false, cap(p.Provider), p.Name);
    return out;
  }

  // Compact provider dot: color and shape come from the registry (claude:
  // circle, codex: rotated square rendered via border-radius+transform).
  function dotStyle(p: string): string {
    const m = providerMeta(p);
    return (
      `background:${m.accent.ink}` +
      (m.dotShape === "diamond" ? ";border-radius:2px;transform:rotate(45deg)" : "")
    );
  }
</script>

{#if variant === "stacked"}
  <div class="rt-grid">
    <!-- Provider / account -->
    <div class="rt-cell">
      <div class="rt-label">Provider</div>
      {#if readonlyAccount || !showAccount}
        <div class="rt-field readonly">
          <ProviderLogo provider={effectiveProvider} size={17} />
          <span class="rt-field-text">{accountDisplay}</span>
        </div>
      {:else}
        <button
          class="rt-field"
          class:open={open === "account"}
          type="button"
          onclick={() => toggle("account")}>
          <ProviderLogo provider={effectiveProvider} size={17} />
          <span class="rt-field-text">{accountDisplay}</span>
          <span class="rt-chev">▾</span>
        </button>
        {#if open === "account"}
          <div class="rt-pop">
            {#each accountChoices as choice}
              <button
                class="rt-row"
                class:sel={choice.default ? !explicit : choice.provider === effectiveProvider && choice.profile === effectiveProfile && explicit}
                type="button"
                onclick={() => chooseAccount(choice.provider, choice.profile)}>
                <span class="rt-row-ic">
                  <ProviderLogo provider={choice.provider || effectiveProvider} size={15} />
                </span>
                <span class="rt-row-main">
                  <span class="rt-row-name">{choice.name}</span>
                  {#if choice.sub}<span class="rt-row-sub">{choice.sub}</span>{/if}
                </span>
                {#if choice.default ? !explicit : choice.provider === effectiveProvider && choice.profile === effectiveProfile && explicit}
                  <span class="rt-check">✓</span>
                {/if}
              </button>
            {/each}
          </div>
        {/if}
      {/if}
    </div>

    <!-- Model -->
    <div class="rt-cell">
      <div class="rt-label">Model</div>
      <button class="rt-field" class:open={open === "model"} type="button" onclick={() => toggle("model")}>
        <span class="rt-field-text">{modelDisplay}</span>
        <span class="rt-chev">▾</span>
      </button>
      {#if open === "model"}
        <div class="rt-pop wide">
          {#each modelRows as m}
            <button class="rt-row col" class:sel={m.selected} type="button" onclick={() => chooseModel(m.model)}>
              <span class="rt-row-head">
                <span class="rt-row-name strong">{m.name}</span>
                {#if m.def}<span class="rt-badge">default</span>{/if}
                {#if m.selected}<span class="rt-check push">✓</span>{/if}
              </span>
              {#if m.desc}<span class="rt-row-desc">{m.desc}</span>{/if}
            </button>
          {/each}
          <div class="rt-custom">
            <input
              class="rt-input"
              bind:value={customModel}
              placeholder="custom model"
              onkeydown={(e) => {
                if (e.key === "Enter") applyCustomModel();
              }} />
            <button class="rt-set" type="button" disabled={!customModel.trim()} onclick={applyCustomModel}>Set</button>
          </div>
        </div>
      {/if}
    </div>

    <!-- Effort -->
    <div class="rt-cell">
      <div class="rt-label">Effort</div>
      <button class="rt-field" class:open={open === "effort"} type="button" onclick={() => toggle("effort")}>
        <span class="rt-dot" style="background:{effortDot(effectiveEffort)}"></span>
        <span class="rt-field-text">{cap(effectiveEffort)}</span>
        <span class="rt-chev">▾</span>
      </button>
      {#if open === "effort"}
        <div class="rt-pop right">
          {#each effortRows as e}
            <button class="rt-row col" class:sel={e.selected} type="button" onclick={() => chooseEffort(e.effort)}>
              <span class="rt-row-head">
                <span class="rt-dot" style="background:{e.dot}"></span>
                <span class="rt-row-name strong">{e.label}</span>
                {#if e.selected}<span class="rt-check push">✓</span>{/if}
              </span>
              {#if e.desc}<span class="rt-row-desc">{e.desc}</span>{/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
{:else}
  <div class="run-target">
    {#if !readonlyAccount && showAccount}
      <div class="rt-dd">
        <button class="rt-chip account" type="button" onclick={() => toggle("account")}>
          <span class="rt-dot" style={dotStyle(effectiveProvider)}></span>
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
        <span class="rt-dot" style={dotStyle(effectiveProvider)}></span>
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
{/if}

<style>
  /* ── Stacked (creation) layout: labelled three-column grid ── */
  .rt-grid {
    display: grid;
    grid-template-columns: 1.15fr 0.95fr 0.95fr;
    gap: 11px;
  }

  .rt-cell {
    position: relative;
    min-width: 0;
  }

  .rt-label {
    font: 700 10.5px "JetBrains Mono", monospace;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: #a89c8e;
    margin-bottom: 7px;
  }

  .rt-field {
    width: 100%;
    min-height: 44px;
    display: flex;
    align-items: center;
    gap: 9px;
    cursor: pointer;
    padding: 8px 11px;
    border-radius: 12px;
    border: 1.5px solid #e9ded0;
    background: #fffaf4;
    transition: border-color 0.14s ease;
  }

  .rt-field.open {
    border-color: #2f6e60;
  }

  .rt-field.readonly {
    cursor: default;
    background: #fbf6ef;
  }

  .rt-field-text {
    flex: 1;
    min-width: 0;
    text-align: left;
    font: 700 13px "Hanken Grotesk";
    color: #4f473f;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .rt-chev {
    font-size: 10px;
    color: #b6a996;
    flex: none;
  }

  .rt-pop {
    position: absolute;
    z-index: 40;
    top: calc(100% + 6px);
    left: 0;
    min-width: 240px;
    max-width: 320px;
    max-height: 300px;
    overflow: auto;
    padding: 6px;
    border: 1px solid #eadfce;
    border-radius: 14px;
    background: #fffdf9;
    box-shadow: 0 20px 46px rgba(43, 37, 32, 0.18);
    animation: rtIn 0.12s ease;
  }

  .rt-pop.wide {
    min-width: 250px;
  }

  .rt-pop.right {
    left: auto;
    right: 0;
    min-width: 220px;
  }

  @keyframes rtIn {
    0% {
      opacity: 0;
      transform: translateY(6px);
    }
    100% {
      opacity: 1;
      transform: none;
    }
  }

  .rt-row {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 9px;
    text-align: left;
    border: none;
    cursor: pointer;
    border-radius: 9px;
    padding: 8px 9px;
    background: transparent;
  }

  .rt-row.col {
    flex-direction: column;
    align-items: stretch;
    gap: 2px;
    padding: 9px 10px;
  }

  .rt-row:hover,
  .rt-row.sel {
    background: #edf5f1;
  }

  .rt-row-ic {
    width: 26px;
    height: 26px;
    flex: none;
    border-radius: 8px;
    background: #f6efe6;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .rt-row-main {
    flex: 1;
    min-width: 0;
  }

  .rt-row-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .rt-row-name {
    display: block;
    font: 700 13px "Hanken Grotesk";
    color: #4f473f;
  }

  .rt-row-name.strong {
    font-weight: 800;
    font-size: 13.5px;
    color: #2b2520;
  }

  .rt-row-sub {
    display: block;
    font: 500 11px "JetBrains Mono", monospace;
    color: #a89c8e;
  }

  .rt-row-desc {
    display: block;
    font: 500 11.5px "Hanken Grotesk";
    color: #8a7f73;
  }

  .rt-badge {
    font: 700 9px "JetBrains Mono", monospace;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: #2a6452;
    background: #e4f0eb;
    border-radius: 5px;
    padding: 2px 5px;
  }

  .rt-check {
    color: #2f6e60;
    font-weight: 800;
    flex: none;
  }

  .rt-check.push {
    margin-left: auto;
  }

  .rt-custom {
    display: flex;
    gap: 6px;
    padding: 8px 2px 2px;
    margin-top: 4px;
    border-top: 1px solid #f0e5d8;
  }

  /* ── Inline (composer chip row) layout ── */
  .run-target {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
    align-items: center;
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

  .rt-field .rt-dot,
  .rt-row-head .rt-dot {
    width: 7px;
    height: 7px;
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

  @media (max-width: 480px) {
    .rt-grid {
      grid-template-columns: 1fr;
    }
    .rt-pop,
    .rt-pop.wide,
    .rt-pop.right {
      right: auto;
      left: 0;
      width: 100%;
      min-width: 0;
      max-width: 100%;
      max-height: min(300px, 50vh);
    }
    .run-target {
      width: 100%;
      align-items: stretch;
    }
    .run-target > .rt-chip,
    .rt-dd {
      width: 100%;
      min-width: 0;
    }
    .rt-chip {
      width: 100%;
      max-width: 100%;
      justify-content: flex-start;
      text-align: left;
      white-space: normal;
      overflow-wrap: anywhere;
    }
    .rt-chip .chev {
      margin-left: auto;
    }
    .rt-menu {
      right: auto;
      left: 0;
      width: 100%;
      min-width: 0;
      max-width: 100%;
    }
    .rt-custom,
    .custom {
      min-width: 0;
    }
    .rt-row-head {
      min-width: 0;
      flex-wrap: wrap;
    }
    .rt-row-name,
    .rt-row-sub,
    .rt-row-desc {
      min-width: 0;
      overflow-wrap: anywhere;
    }
  }
</style>
