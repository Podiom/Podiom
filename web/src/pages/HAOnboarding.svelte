<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { completeOnboarding, getOnboardingState, getOnboardingToken } from "../lib/api";
  import { auth } from "../lib/auth.svelte";
  import HATerminalPanel from "../lib/HATerminalPanel.svelte";
  import type { OnboardingState } from "../lib/types";

  interface Props {
    onUnlocked: () => void;
  }

  let { onUnlocked }: Props = $props();

  let onboarding = $state<OnboardingState | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let finishing = $state(false);
  let poll: number | undefined;

  const phase = $derived(onboarding?.completed ? "complete" : onboarding ? "running" : "waiting");
  const badgeText = $derived(
    phase === "complete" ? "SETUP COMPLETE" : phase === "running" ? "WIZARD RUNNING" : "WAITING FOR WIZARD",
  );
  const markSrc = $derived(`${import.meta.env.BASE_URL}podium-mark-teal.svg`);

  onMount(() => {
    void refresh();
    poll = window.setInterval(() => void refresh(true), 3_000);
  });

  onDestroy(() => {
    if (poll) window.clearInterval(poll);
  });

  async function refresh(silent = false) {
    if (!silent) {
      loading = true;
      error = null;
    }
    try {
      onboarding = await getOnboardingState();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function copyGatewayToken(): Promise<boolean> {
    error = null;
    try {
      const result = await getOnboardingToken();
      try {
        await navigator.clipboard.writeText(result.token);
      } catch {
        // Best effort only: storing it in auth is what unlocks this browser.
      }
      auth.setToken(result.token);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async function finish() {
    finishing = true;
    error = null;
    try {
      if (!onboarding?.completed) {
        onboarding = await completeOnboarding();
      }
      if (!auth.token && !(await copyGatewayToken())) return;
      onUnlocked();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      finishing = false;
    }
  }
</script>

<main class="ha-onboarding">
  <div class="shell">
    <header class="top-row">
      <div class="brand">
        <img src={markSrc} width="30" height="30" alt="Podiom" />
        <span>Podiom</span>
      </div>
      <div class="phase-pill" class:running={phase === "running"} class:complete={phase === "complete"}>
        <span></span>
        {badgeText}
      </div>
    </header>

    <section class="hero-copy" aria-labelledby="ha-onboarding-title">
      <div class="eyebrow">HOME ASSISTANT SETUP · FIRST RUN</div>
      <h1 id="ha-onboarding-title">Let's set the stage.</h1>
      <p>
        Podiom is settling into your Home Assistant install. The onboarding wizard runs itself once, right here.
        When the last cue lands, you're ready to conduct.
      </p>
    </section>

    {#if error}
      <div class="error">{error}</div>
    {/if}

    <section class="terminal-card" aria-label="Onboarding wizard">
      <div class="titlebar">
        <div class="terminal-label">
          <span class="live-dot"></span>
          ONBOARDING WIZARD
          <span class="wave" aria-hidden="true">
            {#each [9, 15, 11, 19, 13, 17, 10] as height, i}
              <span style={`height:${height}px; animation-delay:${-(i * 0.22).toFixed(2)}s`}></span>
            {/each}
          </span>
        </div>
      </div>
      <div class="terminal-body">
        <HATerminalPanel flow="onboard" showToolbar={false} />
      </div>
    </section>

    <footer class="footer-row">
      <div class="live-line">
        <span></span>
        podiomd live · first-run setup · runs once per install
      </div>
      {#if onboarding?.completed}
        <button class="stage-cta" disabled={finishing || loading} onclick={finish}>
          {finishing ? "Opening..." : "Take the stage"}
          <span>→</span>
        </button>
      {/if}
    </footer>
  </div>
</main>

<style>
  @keyframes equalize {
    0%,
    100% {
      transform: scaleY(0.35);
    }
    50% {
      transform: scaleY(1);
    }
  }

  @keyframes ringPulse {
    0% {
      box-shadow: 0 0 0 0 rgba(79, 158, 120, 0.32);
    }
    70% {
      box-shadow: 0 0 0 7px rgba(79, 158, 120, 0);
    }
    100% {
      box-shadow: 0 0 0 0 rgba(79, 158, 120, 0);
    }
  }

  @keyframes softIn {
    from {
      opacity: 0;
      transform: translateY(10px);
    }
    to {
      opacity: 1;
      transform: none;
    }
  }

  .ha-onboarding {
    min-height: 100vh;
    background: linear-gradient(180deg, #f8f3ea 0%, #f4ece2 62%, #efe5d6 100%);
    color: #2b2520;
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 56px 32px 44px;
  }

  .shell {
    width: 100%;
    max-width: 1000px;
    display: flex;
    flex-direction: column;
  }

  .top-row,
  .footer-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 12px;
    color: #2b2520;
    font: 700 21px "Hanken Grotesk", system-ui, sans-serif;
  }

  .brand img {
    width: 30px;
    height: 30px;
  }

  .phase-pill {
    display: inline-flex;
    align-items: center;
    gap: 9px;
    padding: 9px 15px;
    border-radius: 999px;
    border: 1px solid rgba(162, 149, 127, 0.18);
    background: #f0e7da;
    color: #a2957f;
    font: 600 11px "JetBrains Mono", monospace;
    letter-spacing: 0.13em;
  }

  .phase-pill.running {
    border-color: rgba(47, 110, 96, 0.18);
    background: #e5f1eb;
    color: #2f6e60;
  }

  .phase-pill.complete {
    border-color: rgba(47, 110, 96, 0.18);
    background: #dceee3;
    color: #2f6e60;
  }

  .phase-pill span {
    width: 6px;
    height: 6px;
    border-radius: 99px;
    background: currentColor;
  }

  .hero-copy {
    margin-top: 44px;
  }

  .eyebrow {
    font: 600 11.5px "JetBrains Mono", monospace;
    letter-spacing: 0.2em;
    color: #b0a491;
  }

  h1 {
    margin: 14px 0 0;
    font: 700 40px/1.05 "Hanken Grotesk", system-ui, sans-serif;
    color: #2b2520;
  }

  p {
    margin: 14px 0 0;
    max-width: 640px;
    color: #6e6558;
    font: 400 16.5px/1.55 "Hanken Grotesk", system-ui, sans-serif;
  }

  .error {
    margin-top: 22px;
    border: 1px solid #efc7bd;
    background: #fff3ef;
    color: #7c382b;
    border-radius: 8px;
    padding: 11px 12px;
    font: 650 14px "Hanken Grotesk", system-ui, sans-serif;
  }

  .terminal-card {
    margin-top: 34px;
    border-radius: 16px;
    background: linear-gradient(180deg, #24211b 0%, #201d18 100%);
    border: 1px solid rgba(140, 128, 108, 0.22);
    box-shadow:
      0 28px 64px -28px rgba(60, 44, 28, 0.38),
      inset 0 1px 0 rgba(255, 255, 255, 0.04);
    overflow: hidden;
  }

  .titlebar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 14px 18px;
    border-bottom: 1px solid rgba(140, 128, 108, 0.16);
    background: rgba(255, 255, 255, 0.015);
  }

  .terminal-label {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    color: #b6ab98;
    font: 600 11px "JetBrains Mono", monospace;
    letter-spacing: 0.13em;
  }

  .live-dot {
    width: 7px;
    height: 7px;
    border-radius: 99px;
    background: #5fb3a0;
    animation: ringPulse 2.2s ease-out infinite;
  }

  .wave {
    height: 22px;
    margin-left: 6px;
    display: flex;
    align-items: flex-end;
    gap: 3px;
  }

  .wave span {
    width: 2.5px;
    border-radius: 99px;
    background: #4a4235;
    transform-origin: bottom;
    animation: equalize 1.6s ease-in-out infinite;
  }

  .wave span:nth-child(3n + 1) {
    background: #5fb3a0;
  }

  .terminal-body {
    min-height: 400px;
    height: min(52vh, 520px);
  }

  .terminal-body :global(.terminal-frame) {
    min-height: 400px;
    background: #0d0f0b;
  }

  .footer-row {
    margin-top: 22px;
    flex-wrap: wrap;
  }

  .live-line {
    display: flex;
    align-items: center;
    gap: 8px;
    color: #a99c89;
    font: 500 11px "JetBrains Mono", monospace;
    letter-spacing: 0.02em;
  }

  .live-line span {
    width: 6px;
    height: 6px;
    border-radius: 99px;
    background: #4f9e78;
    box-shadow: 0 0 0 3px rgba(79, 158, 120, 0.16);
  }

  .stage-cta {
    animation: softIn 0.6s cubic-bezier(0.2, 0.7, 0.2, 1) both;
    display: inline-flex;
    align-items: center;
    gap: 10px;
    padding: 14px 26px;
    border: 0;
    border-radius: 11px;
    background: #3f8f7e;
    color: #f8f3ea;
    font: 700 15px "Hanken Grotesk", system-ui, sans-serif;
    cursor: pointer;
    box-shadow: 0 12px 24px -10px rgba(63, 143, 126, 0.55);
  }

  .stage-cta:hover {
    background: #357b6c;
  }

  .stage-cta:disabled {
    cursor: not-allowed;
    opacity: 0.64;
  }

  .stage-cta span {
    font: 600 16px "JetBrains Mono", monospace;
  }

  @media (max-width: 720px) {
    .ha-onboarding {
      padding: 28px 18px 28px;
    }

    .top-row {
      align-items: flex-start;
      flex-direction: column;
    }

    .phase-pill {
      max-width: 100%;
      letter-spacing: 0.08em;
    }

    .hero-copy {
      margin-top: 32px;
    }

    h1 {
      font-size: 34px;
    }

    .terminal-body {
      height: 55vh;
    }

    .footer-row {
      align-items: stretch;
      flex-direction: column;
    }

    .stage-cta {
      justify-content: center;
      width: 100%;
    }
  }
</style>
