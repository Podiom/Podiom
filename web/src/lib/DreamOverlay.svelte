<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { dreamAgent } from "./api";
  import { live } from "./live.svelte";
  import { agentGradient, avatarStyle, initial } from "./theme";
  import type { Dream, DreamPhase, ServerMessage } from "./types";

  let {
    agentName,
    onClose = () => {},
    onDone = () => {},
  }: {
    agentName: string;
    onClose?: () => void;
    onDone?: (dream: Dream | null) => void;
  } = $props();

  // Visual phase: the fall→gather→distill intro is timer-driven; distill holds
  // until the daemon reports a terminal state, then we resolve to the outcome.
  type Visual = "fall" | "gather" | "distill" | "reveal" | "noop" | "error";
  let visual = $state<Visual>("fall");
  let dream = $state<Dream | null>(null);
  let errorText = $state<string>("");
  let requestId = "";
  let settled = false; // terminal WS message arrived
  const timers: number[] = [];
  let unsubscribe: (() => void) | null = null;

  // Motes are the un-dreamed sessions being consolidated, labelled by name.
  const motes = $derived(
    live.sessions
      .filter((s) => s.AgentName === agentName && !s.DreamedAt)
      .slice(0, 6)
      .map((s) => s.Name || "a conversation"),
  );

  const stars = Array.from({ length: 16 }, (_, i) => ({
    top: (i * 37) % 90,
    left: (i * 53) % 96,
    delay: (i % 5) * 0.4,
    size: 2 + (i % 3),
  }));

  const caption = $derived(
    visual === "reveal"
      ? "What stayed with me."
      : visual === "noop"
        ? "Nothing left waiting."
        : visual === "error"
          ? "The dream slipped away."
          : `${agentName} is dreaming…`,
  );
  const subhead = $derived(
    visual === "reveal"
      ? dream?.Note || "The day is settled into memory."
      : visual === "noop"
        ? "The day is already dreamed."
        : visual === "error"
          ? errorText || "Nothing was changed."
          : "Going back over the hours you spent together.",
  );
  const newItems = $derived(dream?.NewItems ?? []);

  function onMessage(msg: ServerMessage) {
    if (msg.type !== "dream_state" || msg.agent_name !== agentName) return;
    if (requestId && msg.request_id && msg.request_id !== requestId) return;
    resolveTerminal(msg.dream_phase, msg.dream ?? null, msg.error ?? "");
  }

  function resolveTerminal(phase: DreamPhase | undefined, d: Dream | null, err: string) {
    if (phase === "done") {
      settled = true;
      dream = d;
      // Hold the animation at least through the intro before revealing.
      if (visual === "distill" || visual === "reveal") visual = "reveal";
      else queueReveal();
      onDone(d);
    } else if (phase === "noop") {
      settled = true;
      visual = "noop";
      onDone(null);
    } else if (phase === "error") {
      settled = true;
      errorText = err;
      visual = "error";
      onDone(null);
    }
  }

  function queueReveal() {
    // Ensure the intro plays even if the daemon answered instantly.
    timers.push(window.setTimeout(() => (visual = settled && dream ? "reveal" : visual), 100));
  }

  onMount(async () => {
    unsubscribe = live.subscribe(onMessage);
    // Advance the intro animation on a fixed cadence; distill holds for the WS.
    timers.push(window.setTimeout(() => visual === "fall" && (visual = "gather"), 2200));
    timers.push(window.setTimeout(() => (visual === "gather" || visual === "fall") && (visual = "distill"), 4000));

    if (live.dreamConnected()) {
      requestId = live.dream(agentName);
      return;
    }
    // WebSocket offline — fall back to the REST endpoint (no streamed phases).
    try {
      const res = await dreamAgent(agentName);
      resolveTerminal(res.noop ? "noop" : "done", res.dream, "");
    } catch (e) {
      resolveTerminal("error", null, e instanceof Error ? e.message : String(e));
    }
  });

  onDestroy(() => {
    timers.forEach((t) => clearTimeout(t));
    unsubscribe?.();
  });

  function close() {
    onClose();
  }
</script>

<div class="dream" role="dialog" aria-label="Dreaming">
  <div class="dream-aurora"></div>
  {#each stars as st}
    <div
      class="dream-star"
      style="top:{st.top}%;left:{st.left}%;width:{st.size}px;height:{st.size}px;animation-delay:{st.delay}s"
    ></div>
  {/each}

  <div class="dream-top">
    <div class="dream-moon">
      <div class="dream-moon-glow"></div>
      <div class="dream-moon-shadow"></div>
    </div>
    <div style={avatarStyle(agentGradient(agentName), 44, 14, 18)}>{initial(agentName)}</div>
  </div>

  <div class="dream-core" class:active={visual === "distill" || visual === "gather"}></div>

  {#if visual === "fall" || visual === "gather" || visual === "distill"}
    <div class="dream-motes">
      {#each motes as label, i}
        <div class="dream-mote" style="animation-delay:{i * 0.25}s">{label}</div>
      {/each}
    </div>
  {/if}

  {#if visual === "reveal" && newItems.length > 0}
    <div class="dream-reveal">
      {#each newItems as item}
        <div class="dream-card">
          <div class="dream-card-tag">✦ new memory · {item.section}</div>
          <div class="dream-card-text">{item.text}</div>
        </div>
      {/each}
    </div>
  {/if}

  <div class="dream-caption-wrap">
    <div class="dream-caption">{caption}</div>
    <div class="dream-subhead">{subhead}</div>
  </div>

  <button class="dream-close" onclick={close}>
    {visual === "reveal" || visual === "noop" || visual === "error" ? "Close" : "Dream in the background"}
  </button>
</div>

<style>
  .dream {
    position: fixed;
    inset: 0;
    z-index: 600;
    overflow: hidden;
    animation: dreamIn 0.8s ease both;
    background: radial-gradient(120% 90% at 50% 8%, #2a2358 0%, #1b1740 46%, #120f2b 100%);
  }
  @keyframes dreamIn {
    from {
      opacity: 0;
    }
    to {
      opacity: 1;
    }
  }
  .dream-aurora {
    position: absolute;
    inset: -20% -12%;
    pointer-events: none;
    animation: auroraDrift 12s ease-in-out infinite;
    background:
      radial-gradient(38% 40% at 20% 30%, rgba(80, 175, 165, 0.16), transparent 70%),
      radial-gradient(46% 46% at 82% 22%, rgba(140, 110, 230, 0.24), transparent 72%);
  }
  @keyframes auroraDrift {
    0%,
    100% {
      transform: translate(0, 0);
    }
    50% {
      transform: translate(3%, 2%);
    }
  }
  .dream-star {
    position: absolute;
    border-radius: 99px;
    background: #f4f1ff;
    animation: twinkle 3.5s ease-in-out infinite;
  }
  @keyframes twinkle {
    0%,
    100% {
      opacity: 0.2;
      transform: scale(0.7);
    }
    50% {
      opacity: 1;
      transform: scale(1.25);
    }
  }
  .dream-top {
    position: absolute;
    top: 34px;
    left: 50%;
    transform: translateX(-50%);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 14px;
  }
  .dream-moon {
    position: relative;
    width: 46px;
    height: 46px;
  }
  .dream-moon-glow {
    position: absolute;
    inset: 0;
    border-radius: 50%;
    background: #f4efcf;
    box-shadow: 0 0 42px 7px rgba(244, 239, 207, 0.5);
  }
  .dream-moon-shadow {
    position: absolute;
    top: -3px;
    left: 10px;
    width: 44px;
    height: 44px;
    border-radius: 50%;
    background: #221c4a;
  }
  .dream-core {
    position: absolute;
    top: 44%;
    left: 50%;
    width: 90px;
    height: 90px;
    transform: translate(-50%, -50%);
    border-radius: 50%;
    background: radial-gradient(circle, rgba(160, 146, 240, 0.7), rgba(122, 105, 224, 0.1) 70%);
    opacity: 0.5;
  }
  .dream-core.active {
    animation: corePulse 2s ease-in-out infinite;
  }
  @keyframes corePulse {
    0%,
    100% {
      transform: translate(-50%, -50%) scale(1);
      opacity: 0.45;
    }
    50% {
      transform: translate(-50%, -50%) scale(1.25);
      opacity: 0.8;
    }
  }
  .dream-motes {
    position: absolute;
    top: 44%;
    left: 50%;
    transform: translate(-50%, -50%);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
  }
  .dream-mote {
    padding: 5px 12px;
    border-radius: 999px;
    background: rgba(122, 105, 224, 0.18);
    border: 1px solid rgba(160, 146, 240, 0.4);
    color: #d8d2f5;
    font: 500 12px "Hanken Grotesk";
    animation: floaty 3s ease-in-out infinite;
    white-space: nowrap;
    max-width: 70vw;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  @keyframes floaty {
    0%,
    100% {
      transform: translateY(0);
    }
    50% {
      transform: translateY(-5px);
    }
  }
  .dream-reveal {
    position: absolute;
    left: 50%;
    top: 50%;
    transform: translate(-50%, -50%);
    display: flex;
    flex-direction: column;
    gap: 10px;
    width: min(480px, 82%);
  }
  .dream-card {
    padding: 13px 15px;
    border-radius: 13px;
    background: rgba(122, 105, 224, 0.16);
    border: 1px solid rgba(160, 146, 240, 0.5);
    box-shadow: 0 0 30px -6px rgba(140, 120, 255, 0.55);
    animation: dreamIn 0.6s ease both;
  }
  .dream-card-tag {
    font: 700 10px "JetBrains Mono", monospace;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: #b7a8ff;
    margin-bottom: 5px;
  }
  .dream-card-text {
    font: 500 14px/1.5 "Hanken Grotesk";
    color: #f1eeff;
  }
  .dream-caption-wrap {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 54px;
    text-align: center;
    pointer-events: none;
    padding: 0 24px;
  }
  .dream-caption {
    font: 700 21px "Hanken Grotesk";
    letter-spacing: -0.01em;
    color: #f4f1ff;
  }
  .dream-subhead {
    font: 500 12px "JetBrains Mono", monospace;
    color: #9f94d6;
    margin-top: 8px;
  }
  .dream-close {
    position: absolute;
    top: 26px;
    right: 28px;
    z-index: 5;
    padding: 8px 16px;
    border-radius: 11px;
    border: 1px solid rgba(200, 190, 255, 0.35);
    background: rgba(255, 255, 255, 0.06);
    color: #d8d2f5;
    font: 600 12.5px "Hanken Grotesk";
    cursor: pointer;
  }
</style>
