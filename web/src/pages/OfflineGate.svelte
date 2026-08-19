<script lang="ts">
  // OfflineGate — the takeover shown when a native app cannot reach its gateway.
  //
  // Ported from the Claude design comp "Podium Mobile Offline.dc.html". Its copy
  // is per-reason on purpose: a phone with no signal, a sleeping machine and a
  // restarting daemon look identical to a client but need different things from
  // the user, and this is the only place the app gets to say which it found.
  //
  // Rendered over the app rather than in place of it (see App.svelte), so
  // dismissing it restores the page the user was on untouched.

  import type { OfflineReason } from "../lib/reachability";
  import { reachability } from "../lib/reachability.svelte";
  import { WAVE_ACCENTS, WAVE_HEIGHTS } from "../lib/waveform";

  const COPY: Record<OfflineReason, { title: string; body: string }> = {
    unreachable: {
      title: "Can't reach your gateway.",
      body: "podiomd isn't answering on the saved address. The machine may be asleep, or the daemon has stopped.",
    },
    "no-network": {
      title: "This device is offline.",
      body: "No Wi-Fi or mobile data. Podiom needs a connection before it can reach the gateway.",
    },
    timeout: {
      title: "Gateway isn't responding.",
      body: "The connection opened but podiomd stopped answering mid-request. It may be restarting.",
    },
  };

  const retrying = $derived(reachability.phase === "retrying");
  // Falling back to the unreachable wording rather than rendering nothing: the
  // screen is only up because something failed, and a blank heading would be a
  // worse answer than the commonest one.
  const copy = $derived(COPY[reachability.reason ?? "unreachable"]);

  // barVars carries only the per-bar numbers. Everything else — colour, and
  // crucially the animation — lives in the scoped CSS below, because Svelte
  // rewrites @keyframes names to hashed ones: an inline `animation: equalize`
  // would name a keyframe that does not exist in the built stylesheet and the
  // figure would sit dead. The stagger is negative on purpose, so the bars start
  // mid-cycle rather than all rising together.
  function barVars(h: number, i: number): string {
    return [
      `--bar-h:${Math.max(6, Math.round(h * 0.34))}px`,
      `--bar-dur:${(0.9 + ((i * 7) % 9) / 10).toFixed(2)}s`,
      `--bar-delay:${(-(i * 0.12)).toFixed(2)}s`,
      `--bar-pulse-delay:${(-(i * 0.09)).toFixed(2)}s`,
    ].join(";");
  }
</script>

<div class="offline-root" role="alertdialog" aria-modal="true" aria-labelledby="offline-heading">
  <div class="glow" aria-hidden="true"></div>
  <div class="hairline" aria-hidden="true"></div>

  <header>
    <svg class="mark" width="24" height="24" viewBox="0 0 48 48" aria-label="Podiom">
      <path d="M8 31 Q18 6 30 16" fill="none" stroke="#2B2520" stroke-width="3.4" stroke-linecap="round" />
      <circle cx="30" cy="16" r="4.6" fill="#2B2520" />
      <circle cx="36" cy="23" r="2.9" fill="#2B2520" opacity=".72" />
      <circle cx="41" cy="29" r="1.7" fill="#2B2520" opacity=".45" />
    </svg>
    <div class="pill">
      <span class="pill-dot"></span>
      <span class="pill-label">OFFLINE</span>
    </div>
  </header>

  <div class="body">
    <div class="wave" aria-hidden="true">
      {#each WAVE_HEIGHTS as h, i}
        <span
          class="bar"
          class:accent={WAVE_ACCENTS.has(i)}
          class:animating={retrying}
          style={barVars(h, i)}></span>
      {/each}
    </div>
    <div class="caption" aria-hidden="true">no signal from the pit</div>

    {#key reachability.reason}
      <div class="reason">
        <h1 id="offline-heading">{copy.title}</h1>
        <p>{copy.body}</p>
      </div>
    {/key}

    <dl class="diagnostics">
      <div class="diag-row">
        <dt>gateway</dt>
        <dd>{reachability.endpoint}</dd>
      </div>
      <div class="diag-row">
        <dt>error</dt>
        <dd class="diag-error">{reachability.code}</dd>
      </div>
    </dl>
  </div>

  <footer>
    <button class="retry" type="button" onclick={() => reachability.retry()} disabled={retrying}>
      {#if retrying}
        <span class="retry-dot"></span>Reconnecting&hellip;
      {:else}
        Try again
      {/if}
    </button>

    <div class="status" aria-live="polite">
      {#if retrying}
        <span class="status-live">reaching {reachability.endpoint}&hellip;</span>
      {:else}
        <span>auto-retry in {reachability.countdown}s · attempt {reachability.attempt}</span>
      {/if}
    </div>

    <button class="settings" type="button" onclick={() => reachability.openSettings()}>Gateway settings</button>
  </footer>
</div>

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

  @keyframes nodePulse {
    0%,
    100% {
      opacity: 0.35;
    }
    50% {
      opacity: 1;
    }
  }

  @keyframes softBlink {
    0%,
    49% {
      opacity: 1;
    }
    50%,
    100% {
      opacity: 0.25;
    }
  }

  @keyframes driftIn {
    from {
      opacity: 0;
      transform: translateY(6px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  /* Fixed and above every other layer, including DreamOverlay's 600: while this
     is up nothing behind it can be acted on, so nothing behind it should show
     through or take a tap. */
  .offline-root {
    position: fixed;
    inset: 0;
    z-index: 700;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    background: linear-gradient(180deg, #f1eae0 0%, #ece3d7 58%, #e4d9c9 100%);
    color: #2b2520;
  }

  .glow {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 300px;
    background: radial-gradient(120% 100% at 50% -10%, rgba(193, 97, 63, 0.16), rgba(193, 97, 63, 0) 68%);
    pointer-events: none;
  }

  .hairline {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 2px;
    background: linear-gradient(90deg, rgba(193, 97, 63, 0), #c1613f, rgba(193, 97, 63, 0));
    pointer-events: none;
  }

  header {
    position: relative;
    /* The comp's flat 58px assumes a notch. Deriving it from the inset instead
       keeps the mark clear of the status bar on every device. */
    padding: calc(24px + env(safe-area-inset-top)) 24px 0;
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .mark {
    opacity: 0.32;
  }

  .pill {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 6px 11px 6px 9px;
    border-radius: 99px;
    background: rgba(193, 97, 63, 0.1);
    border: 1px solid rgba(193, 97, 63, 0.22);
  }

  .pill-dot {
    width: 6px;
    height: 6px;
    border-radius: 99px;
    background: #c1613f;
    box-shadow: 0 0 0 3px rgba(193, 97, 63, 0.16);
    animation: nodePulse 1.6s infinite;
  }

  .pill-label {
    font: 600 10px "JetBrains Mono", monospace;
    letter-spacing: 0.14em;
    color: #b0532f;
  }

  .body {
    position: relative;
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 0 28px;
    text-align: center;
  }

  .wave {
    display: flex;
    align-items: center;
    gap: 6px;
    height: 64px;
  }

  .bar {
    width: 2.5px;
    height: 2.5px;
    border-radius: 99px;
    background: #dcd0bf;
    animation: nodePulse 2.8s ease-in-out var(--bar-pulse-delay) infinite;
    transition:
      height 0.5s ease,
      background 0.5s ease;
  }

  .bar.accent {
    width: 3px;
    background: rgba(193, 97, 63, 0.55);
  }

  .bar.animating {
    height: var(--bar-h);
    background: #c7d9d1;
    animation: equalize var(--bar-dur) ease-in-out var(--bar-delay) infinite;
  }

  .bar.animating.accent {
    background: #3f8f7e;
  }

  .caption {
    font: 500 10px "JetBrains Mono", monospace;
    color: #c0b2a0;
    letter-spacing: 0.14em;
    margin-top: 14px;
  }

  .reason {
    margin-top: 38px;
    animation: driftIn 0.4s ease;
  }

  .reason h1 {
    font: 700 27px "Hanken Grotesk", system-ui, sans-serif;
    letter-spacing: -0.02em;
    line-height: 1.15;
    margin: 0;
  }

  .reason p {
    font: 400 15px "Hanken Grotesk", system-ui, sans-serif;
    color: #8b7f71;
    line-height: 1.55;
    margin: 12px auto 0;
    max-width: 300px;
    text-wrap: pretty;
  }

  .diagnostics {
    margin: 30px 0 0;
    width: 100%;
    max-width: 320px;
    border: 1px solid rgba(43, 37, 32, 0.09);
    border-radius: 12px;
    background: rgba(255, 252, 247, 0.62);
    padding: 2px 14px;
  }

  .diag-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 0;
  }

  .diag-row + .diag-row {
    border-top: 1px solid rgba(43, 37, 32, 0.07);
  }

  .diag-row dt {
    font: 500 10.5px "JetBrains Mono", monospace;
    color: #a89c8e;
    letter-spacing: 0.06em;
  }

  .diag-row dd {
    margin: 0;
    font: 500 10.5px "JetBrains Mono", monospace;
    color: #2b2520;
    /* A long address must not push the label out of the card. */
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .diag-row dd.diag-error {
    font-weight: 600;
    color: #b0532f;
  }

  footer {
    position: relative;
    padding: 0 24px calc(24px + env(safe-area-inset-bottom));
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 14px;
  }

  .retry {
    width: 100%;
    height: 54px;
    border: none;
    border-radius: 14px;
    background: #2b2520;
    color: #f6f0e6;
    font: 600 15.5px "Hanken Grotesk", system-ui, sans-serif;
    letter-spacing: 0.01em;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 9px;
    box-shadow: 0 6px 18px rgba(43, 37, 32, 0.16);
  }

  .retry:hover:not(:disabled) {
    background: #3a322b;
  }

  .retry:active:not(:disabled) {
    transform: translateY(1px);
  }

  .retry:disabled {
    cursor: default;
  }

  .retry-dot {
    width: 7px;
    height: 7px;
    border-radius: 99px;
    background: #7fbcac;
    animation: softBlink 0.8s infinite;
  }

  .status {
    height: 16px;
    display: flex;
    align-items: center;
    max-width: 100%;
    font: 400 11px "JetBrains Mono", monospace;
    color: #a89c8e;
  }

  .status span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .status-live {
    color: #8b7f71;
  }

  .settings {
    height: 44px;
    padding: 0 16px;
    border: none;
    background: transparent;
    font: 600 13.5px "Hanken Grotesk", system-ui, sans-serif;
    color: #3f8f7e;
  }

  .settings:hover {
    color: #2f6e60;
  }
</style>
