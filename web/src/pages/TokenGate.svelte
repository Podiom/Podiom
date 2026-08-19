<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { auth } from "../lib/auth.svelte";
  import { apiUrl } from "../lib/base";
  import * as connection from "../lib/connection";
  import * as discovery from "../lib/discovery";
  import type { DiscoveredInstance } from "../lib/discovery";
  import { verifyToken } from "../lib/http";
  import { isNative } from "../lib/native";
  import { WAVE_ACCENTS, WAVE_HEIGHTS } from "../lib/waveform";

  type Status = "idle" | "validating" | "error" | "success";
  type VisualStatus = Status | "offline";

  let value = $state("");
  let status = $state<Status>("idle");
  let errorMsg = $state("");
  let daemonUp = $state<boolean | null>(null);
  let unlockTimer: number | undefined;
  let probeTimer: number | undefined;

  // Native only. A browser is already talking to the daemon that served it, so
  // there is nothing to ask for beyond the token and this whole block stays
  // inert — the web screen renders exactly as it did before (R7).
  let address = $state("");
  let searching = $state(false);
  let searched = $state(false);
  let found = $state<DiscoveredInstance[]>([]);

  // The address as a URL, or null while it is unparseable. Drives both the
  // reachability probe and the submit guard.
  const addressURL = $derived.by(() => {
    if (!isNative) return null;
    try {
      return connection.normalizeAddress(address);
    } catch {
      return null;
    }
  });

  const visualStatus = $derived<VisualStatus>(daemonUp === false ? "offline" : status);
  // On the web an unreachable daemon means there is nothing to log into, so the
  // form locks. On native "unreachable" is the normal state while the user is
  // still typing an address, so Connect has to stay live to report why.
  const disabled = $derived(
    visualStatus === "validating" || visualStatus === "success" || (!isNative && visualStatus === "offline"),
  );
  const isOnline = $derived(visualStatus !== "offline");

  // The instance this screen is pointed at. On the web that is whatever origin
  // served the page (under HA Ingress, the Home Assistant host) — never the
  // hardcoded loopback address this line used to claim.
  const endpointLabel = $derived.by(() => {
    if (!isNative) return apiUrl("").host;
    return addressURL ? addressURL.host : "";
  });
  const inputBorder = $derived(
    visualStatus === "error" ? "#C97B5D" : visualStatus === "success" ? "#3F8F7E" : "#E0D3C3",
  );

  onMount(() => {
    // After a token rotation the instance is still the right one — only its
    // token went stale — so the address comes back pre-filled and the user
    // re-enters just the token (R7).
    if (isNative) void connection.storedAddress().then((a) => (address = a));
    void probe();
    probeTimer = window.setInterval(() => void probe(), 10_000);
  });

  onDestroy(() => {
    if (probeTimer) window.clearInterval(probeTimer);
    if (unlockTimer) window.clearTimeout(unlockTimer);
  });

  async function probe() {
    // On native there is no daemon until the user names one, so the liveness
    // dot reflects the address currently in the field rather than the origin
    // (which is the phone itself).
    const target = isNative ? addressURL : apiUrl("healthz");
    if (isNative && !target) {
      daemonUp = null;
      return;
    }
    try {
      const url = isNative ? new URL("healthz", target as URL) : (target as URL);
      const res = await fetch(url, { signal: AbortSignal.timeout(5000) });
      daemonUp = res.ok;
    } catch {
      daemonUp = false;
    }
  }

  function onInput() {
    if (status === "validating" || status === "success") return;
    status = "idle";
    errorMsg = "";
  }

  function onAddressInput() {
    onInput();
    // The old result set belongs to a different address now.
    searched = false;
    found = [];
    daemonUp = null;
    void probe();
  }

  async function search() {
    if (searching) return;
    searching = true;
    errorMsg = "";
    try {
      found = await discovery.discover();
    } finally {
      searching = false;
      searched = true;
    }
  }

  function choose(instance: DiscoveredInstance) {
    address = discovery.addressFor(instance);
    found = [];
    searched = false;
    status = "idle";
    errorMsg = "";
    void probe();
  }

  async function submit() {
    const candidate = value.trim();
    if (disabled && visualStatus !== "error") return;
    if (isNative && !address.trim()) {
      status = "error";
      errorMsg = "enter the address of your Podiom instance";
      return;
    }
    if (isNative && !addressURL) {
      status = "error";
      errorMsg = "that address is not a valid URL";
      return;
    }
    if (!candidate) {
      status = "error";
      errorMsg = "paste a gateway token first";
      return;
    }
    status = "validating";
    errorMsg = "";

    if (isNative) {
      // Both halves are checked before either is stored, so the screen can say
      // which one is wrong instead of a single "could not connect" (R7).
      const result = await connection.probe(addressURL as URL, candidate);
      if (!result.ok) {
        status = "error";
        errorMsg =
          result.reason === "token-rejected"
            ? "token not recognized - check with podiom token show"
            : result.reason === "not-podiom"
              ? "not a Podiom API - for Home Assistant, enable the LAN port and use http://<HA-IP>:<port>, not the sidebar URL"
              : "cannot reach that address - check it and that podiomd is running";
        return;
      }
      status = "success";
      unlockTimer = window.setTimeout(() => void connection.save(addressURL as URL, candidate), 650);
      return;
    }

    const ok = await verifyToken(candidate);
    if (!ok) {
      status = "error";
      errorMsg = daemonUp === false
        ? "podiomd unreachable - is the daemon running?"
        : "token not recognized by podiomd - check with podiom token show";
      return;
    }
    status = "success";
    unlockTimer = window.setTimeout(() => auth.setToken(candidate), 650);
  }

  function handleKey(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      void submit();
    }
  }

  function waveConfig(s: VisualStatus) {
    switch (s) {
      case "validating":
        return { hMul: 1.1, dMul: 0.42, sand: "#9CC8BB", accent: "#2F6E60" };
      case "error":
        return { hMul: 0.32, dMul: 1.8, sand: "#E3CDBE", accent: "#C97B5D" };
      case "success":
        return { hMul: 1.5, dMul: 0.6, sand: "#7FBCAC", accent: "#2F6E60" };
      case "offline":
        return { hMul: 0.5, dMul: 2.6, sand: "#E6DCCB", accent: "#CBBFA9" };
      default:
        return { hMul: 1, dMul: 1, sand: "#DCCFBD", accent: "#46A08C" };
    }
  }

  function barStyle(h: number, i: number, s: VisualStatus): string {
    const cfg = waveConfig(s);
    const isAccent = WAVE_ACCENTS.has(i);
    const dur = (2.5 + ((i * 7) % 12) / 10) * cfg.dMul;
    return [
      `width:${isAccent ? 3 : 2.5}px`,
      `height:${Math.min(64, Math.round(h * cfg.hMul))}px`,
      "border-radius:99px",
      `background:${isAccent ? cfg.accent : cfg.sand}`,
      `animation:equalize ${dur.toFixed(2)}s ease-in-out ${(-(i * 0.3)).toFixed(1)}s infinite`,
      "transition:height .6s ease, background .6s ease",
    ].join(";");
  }
</script>

<main class="auth-root" data-status={visualStatus}>
  <svg class="mark" width="40" height="40" viewBox="0 0 48 48" aria-label="Podiom">
    <path d="M8 31 Q18 6 30 16" fill="none" stroke="#2F6E60" stroke-width="3.4" stroke-linecap="round" />
    <circle cx="30" cy="16" r="4.6" fill="#2F6E60" />
    <circle cx="36" cy="23" r="2.9" fill="#2F6E60" opacity=".72" />
    <circle cx="41" cy="29" r="1.7" fill="#2F6E60" opacity=".45" />
  </svg>

  <h1>The orchestra awaits.</h1>

  <div class="wave-block" aria-hidden="true">
    <div class="waveform">
      {#each WAVE_HEIGHTS as h, i}
        <span style={barStyle(h, i, visualStatus)}></span>
      {/each}
    </div>
    <div class="model-line">claude-opus &nbsp;·&nbsp; gpt-4o &nbsp;·&nbsp; gemini-pro &nbsp;·&nbsp; llama-70b &nbsp;·&nbsp; mistral &nbsp;·&nbsp; deepseek</div>
  </div>

  <form class:error-shake={visualStatus === "error"} onsubmit={(e) => { e.preventDefault(); void submit(); }}>
    {#if isNative}
      <input
        class="address"
        type="url"
        bind:value={address}
        oninput={onAddressInput}
        onkeydown={handleKey}
        disabled={disabled}
        placeholder="https://podiom.example.com"
        autocomplete="url"
        autocapitalize="none"
        autocorrect="off"
        inputmode="url"
        spellcheck="false"
        aria-label="Podiom address"
        style={`border-bottom-color:${inputBorder}`} />
    {/if}
    <input
      type="password"
      bind:value
      oninput={onInput}
      onkeydown={handleKey}
      disabled={disabled}
      placeholder="pdm_gateway_token"
      autocomplete="off"
      spellcheck="false"
      aria-label="Gateway token"
      style={`border-bottom-color:${inputBorder}`} />
  </form>

  {#if isNative && discovery.available}
    <div class="discover">
      <div class="or"><span>or</span></div>
      <button class="find" type="button" onclick={search} disabled={searching || disabled}>
        {searching ? "searching the network..." : searched ? "Search again" : "Find Podiom on network"}
      </button>
      {#if found.length}
        <ul class="instances">
          {#each found as instance (instance.host + instance.port)}
            <li>
              <button type="button" onclick={() => choose(instance)}>
                <span class="node online"></span>
                <span class="inst-text">
                  <span class="inst-name">{instance.name}</span>
                  <span class="inst-addr">{instance.host}:{instance.port}</span>
                </span>
              </button>
            </li>
          {/each}
        </ul>
      {:else if searched}
        <div class="no-instances">
          nothing answered on this network - enter the address above instead
        </div>
      {/if}
    </div>
  {/if}

  <div class="status-line" aria-live="polite">
    {#if visualStatus === "idle"}
      <span class="idle">press <strong>↵</strong> to take the stage</span>
    {:else if visualStatus === "validating"}
      <span class="checking">tuning<span class="caret">...</span></span>
    {:else if visualStatus === "error"}
      <span class="error">x {errorMsg}</span>
    {:else if visualStatus === "success"}
      <span class="ok">connected - taking the stage</span>
    {:else}
      <span class="error">podiomd unreachable - is the daemon running?</span>
    {/if}
  </div>

  <div class="daemon">
    {#if isNative && daemonUp === null}
      <span class="node unknown"></span>
      <span>no instance yet · token stays on-device</span>
    {:else if isOnline}
      <span class="node online"></span>
      <span>podiomd live · {endpointLabel} · token stays on-device</span>
    {:else}
      <span class="node offline"></span>
      <span>podiomd offline · retrying {endpointLabel} · token stays on-device</span>
    {/if}
  </div>
</main>

<style>
  @keyframes equalize {
    0%,
    100% {
      transform: scaleY(0.22);
    }
    50% {
      transform: scaleY(1);
    }
  }

  @keyframes caretBlink {
    0%,
    49% {
      opacity: 1;
    }
    50%,
    100% {
      opacity: 0;
    }
  }

  @keyframes shakeX {
    0%,
    100% {
      transform: translateX(0);
    }
    20% {
      transform: translateX(-7px);
    }
    40% {
      transform: translateX(6px);
    }
    60% {
      transform: translateX(-4px);
    }
    80% {
      transform: translateX(3px);
    }
  }

  @keyframes nodePulse {
    0%,
    100% {
      opacity: 0.45;
    }
    50% {
      opacity: 1;
    }
  }

  .auth-root {
    width: 100%;
    max-width: 100vw;
    height: 100%;
    height: 100dvh;
    min-height: 0;
    position: relative;
    overflow: hidden;
    background: linear-gradient(180deg, #f8f3ea 0%, #f4ece2 70%, #efe5d6 100%);
    color: #2b2520;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 28px 22px 70px;
  }

  .mark {
    flex: none;
    margin-bottom: 28px;
  }

  h1 {
    margin: 0;
    color: #2b2520;
    font: 700 32px "Hanken Grotesk";
    letter-spacing: -0.015em;
    text-align: center;
  }

  .wave-block {
    margin-top: 52px;
    display: flex;
    flex-direction: column;
    align-items: center;
    max-width: 100%;
  }

  .waveform {
    height: 64px;
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .waveform span {
    flex: none;
    transform-origin: center;
  }

  .model-line {
    margin-top: 18px;
    max-width: min(680px, calc(100vw - 32px));
    color: #c4b6a2;
    font: 500 10.5px "JetBrains Mono", monospace;
    letter-spacing: 0.12em;
    text-align: center;
    white-space: normal;
  }

  form {
    margin-top: 52px;
    width: min(380px, calc(100vw - 44px));
  }

  form.error-shake {
    animation: shakeX 0.45s ease;
  }

  input {
    width: 100%;
    text-align: center;
    padding: 12px 4px;
    border: none;
    border-bottom: 1.5px solid #e0d3c3;
    background: transparent;
    color: #2b2520;
    caret-color: #3f8f7e;
    border-radius: 0;
    font: 500 16px "JetBrains Mono", monospace;
  }

  input:focus {
    outline: none;
    border-bottom-color: #3f8f7e !important;
  }

  input:disabled {
    opacity: 0.55;
  }

  /* Native only: the address field sits above the token field in the same form,
     sharing its borderless treatment so the screen still reads as one input
     column rather than a form with two boxes (R7). */
  input.address {
    margin-bottom: 18px;
    /* iOS zooms the WebView when focusing inputs below 16px, which makes the
       otherwise fixed viewport pannable in both directions. */
    font-size: 16px;
  }

  .discover {
    display: flex;
    width: min(380px, calc(100vw - 44px));
    flex-direction: column;
    align-items: stretch;
    margin-top: 26px;
  }

  .or {
    display: flex;
    align-items: center;
    justify-content: center;
    margin-bottom: 16px;
    color: #c4b6a2;
    font: 500 10px "JetBrains Mono", monospace;
    letter-spacing: 0.14em;
  }

  .or::before,
  .or::after {
    flex: 1;
    height: 1px;
    background: #e6dbcc;
    content: "";
  }

  .or span {
    padding: 0 12px;
  }

  .find {
    padding: 11px 16px;
    border: 1px solid #e0d3c3;
    border-radius: 12px;
    background: transparent;
    color: #2f6e60;
    font: 600 13px "Hanken Grotesk";
    cursor: pointer;
  }

  .find:disabled {
    opacity: 0.55;
    cursor: default;
  }

  .instances {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin: 12px 0 0;
    padding: 0;
    list-style: none;
  }

  .instances button {
    display: flex;
    width: 100%;
    align-items: center;
    gap: 10px;
    padding: 11px 14px;
    border: 1px solid #e6dbcc;
    border-radius: 12px;
    background: rgba(255, 253, 251, 0.72);
    cursor: pointer;
    text-align: left;
  }

  .inst-text {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 2px;
  }

  .inst-name {
    overflow: hidden;
    color: #2b2520;
    font: 600 13px "Hanken Grotesk";
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .inst-addr {
    color: #a89c8e;
    font: 400 10.5px "JetBrains Mono", monospace;
  }

  .no-instances {
    margin-top: 12px;
    color: #a89c8e;
    font: 400 11px "JetBrains Mono", monospace;
    line-height: 1.5;
    text-align: center;
  }

  .status-line {
    min-height: 24px;
    margin-top: 16px;
    display: flex;
    align-items: center;
    justify-content: center;
    max-width: min(520px, calc(100vw - 44px));
    text-align: center;
  }

  .status-line span {
    font: 400 11.5px "JetBrains Mono", monospace;
    line-height: 1.45;
  }

  .status-line strong,
  .checking {
    color: #3f8f7e;
    font-weight: 600;
  }

  .idle {
    color: #a89c8e;
  }

  .error {
    color: #c1613f;
    font-weight: 500;
  }

  .ok {
    color: #2f6e60;
    font-weight: 500;
  }

  .caret {
    animation: caretBlink 1s infinite;
  }

  .daemon {
    position: absolute;
    bottom: calc(26px + env(safe-area-inset-bottom));
    left: 22px;
    right: 22px;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    color: #b8ab99;
    font: 500 10px "JetBrains Mono", monospace;
    text-align: center;
  }

  .node {
    width: 6px;
    height: 6px;
    flex: none;
    border-radius: 99px;
  }

  .node.online {
    background: #4f9e78;
    box-shadow: 0 0 0 3px rgba(79, 158, 120, 0.16);
  }

  .node.offline {
    background: #c1613f;
    box-shadow: 0 0 0 3px rgba(193, 97, 63, 0.16);
    animation: nodePulse 1.6s infinite;
  }

  /* Native, before an address has been entered: neither live nor down. */
  .node.unknown {
    background: #cbbfa9;
  }

  @media (max-width: 560px) {
    .auth-root {
      justify-content: flex-start;
      /* This screen renders outside the app shell, so it carries its own inset
         rather than inheriting .main's (App.svelte). */
      padding-top: calc(clamp(24px, 7dvh, 82px) + env(safe-area-inset-top));
      padding-bottom: calc(54px + env(safe-area-inset-bottom));
    }

    h1 {
      font-size: 28px;
    }

    .wave-block,
    form {
      margin-top: clamp(20px, 5dvh, 44px);
    }

    .waveform {
      gap: 4px;
      transform: scaleX(0.86);
    }

    .discover {
      margin-top: clamp(12px, 3dvh, 26px);
    }

    .status-line {
      margin-top: clamp(8px, 2dvh, 16px);
    }
  }

  /* When the iOS keyboard is visible, or the device is in landscape, keep the
     fields in the resized viewport and drop only the decorative elements. */
  @media (max-height: 520px) {
    .auth-root {
      justify-content: center;
      padding: calc(12px + env(safe-area-inset-top)) 22px calc(12px + env(safe-area-inset-bottom));
    }

    .mark,
    .wave-block,
    .daemon {
      display: none;
    }

    h1 {
      font-size: 26px;
    }

    form {
      margin-top: 16px;
    }

    input {
      padding-block: 8px;
    }

    input.address {
      margin-bottom: 8px;
    }

    .discover {
      margin-top: 10px;
    }

    .or {
      margin-bottom: 8px;
    }

    .find {
      padding-block: 8px;
    }

    .status-line {
      min-height: 20px;
      margin-top: 8px;
    }
  }
</style>
