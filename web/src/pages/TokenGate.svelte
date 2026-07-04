<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { auth } from "../lib/auth.svelte";
  import { apiUrl } from "../lib/base";
  import { verifyToken } from "../lib/http";

  type Status = "idle" | "validating" | "error" | "success";
  type VisualStatus = Status | "offline";

  const heights = [16, 24, 20, 32, 26, 40, 30, 22, 36, 28, 46, 34, 24, 42, 52, 38, 58, 44, 60, 44, 58, 38, 52, 42, 24, 34, 46, 28, 36, 22, 30, 40, 26, 32, 20, 24, 16];
  const accents = new Set([6, 14, 18, 22, 30]);

  let value = $state("");
  let status = $state<Status>("idle");
  let errorMsg = $state("");
  let daemonUp = $state<boolean | null>(null);
  let unlockTimer: number | undefined;
  let probeTimer: number | undefined;

  const visualStatus = $derived<VisualStatus>(daemonUp === false ? "offline" : status);
  const disabled = $derived(visualStatus === "validating" || visualStatus === "success" || visualStatus === "offline");
  const isOnline = $derived(visualStatus !== "offline");
  const inputBorder = $derived(
    visualStatus === "error" ? "#C97B5D" : visualStatus === "success" ? "#3F8F7E" : "#E0D3C3",
  );

  onMount(() => {
    void probe();
    probeTimer = window.setInterval(() => void probe(), 10_000);
  });

  onDestroy(() => {
    if (probeTimer) window.clearInterval(probeTimer);
    if (unlockTimer) window.clearTimeout(unlockTimer);
  });

  async function probe() {
    try {
      const res = await fetch(apiUrl("healthz"));
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

  async function submit() {
    const candidate = value.trim();
    if (disabled && visualStatus !== "error") return;
    if (!candidate) {
      status = "error";
      errorMsg = "paste a gateway token first";
      return;
    }
    status = "validating";
    errorMsg = "";
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
    const isAccent = accents.has(i);
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
      {#each heights as h, i}
        <span style={barStyle(h, i, visualStatus)}></span>
      {/each}
    </div>
    <div class="model-line">claude-opus &nbsp;·&nbsp; gpt-4o &nbsp;·&nbsp; gemini-pro &nbsp;·&nbsp; llama-70b &nbsp;·&nbsp; mistral &nbsp;·&nbsp; deepseek</div>
  </div>

  <form class:error-shake={visualStatus === "error"} onsubmit={(e) => { e.preventDefault(); void submit(); }}>
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
    {#if isOnline}
      <span class="node online"></span>
      <span>podiomd live · 127.0.0.1:8787 · token stays on-device</span>
    {:else}
      <span class="node offline"></span>
      <span>podiomd offline · retrying 127.0.0.1:8787 · token stays on-device</span>
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
    min-height: 100%;
    min-height: 100dvh;
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
    bottom: 26px;
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

  @media (max-width: 560px) {
    .auth-root {
      justify-content: flex-start;
      padding-top: 82px;
    }

    h1 {
      font-size: 28px;
    }

    .wave-block,
    form {
      margin-top: 44px;
    }

    .waveform {
      gap: 4px;
      transform: scaleX(0.86);
    }
  }
</style>
