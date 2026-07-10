<script lang="ts">
  // Microphone button for voice input: tap to record, tap again to stop.
  // The recording is uploaded to POST /api/transcribe, where the daemon calls
  // the OpenAI Whisper API (see docs/voice-input.md), and the recognized text
  // is handed to the caller via onText. Used beside the chat composer's send
  // button and next to the task/goal prompt fields.
  import { onDestroy } from "svelte";
  import { transcribeAudio } from "./api";

  let {
    onText,
    size = "md",
    disabled = false,
  }: {
    onText: (text: string) => void;
    size?: "sm" | "md";
    disabled?: boolean;
  } = $props();

  // Recording stops itself after two minutes — a guard against a forgotten
  // open mic, and it keeps uploads tiny relative to Whisper's 25 MB cap.
  const MAX_RECORD_MS = 120_000;

  type Phase = "idle" | "recording" | "transcribing";
  let phase = $state<Phase>("idle");
  let error = $state("");

  let recorder: MediaRecorder | null = null;
  let stream: MediaStream | null = null;
  let chunks: Blob[] = [];
  let maxTimer: ReturnType<typeof setTimeout> | undefined;
  let errorTimer: ReturnType<typeof setTimeout> | undefined;
  let destroyed = false;

  // MediaRecorder container support differs per engine: Chrome/Firefox record
  // webm/opus, iOS Safari records mp4 (AAC). An empty string lets old Safari
  // pick its own default.
  function pickMimeType(): string {
    if (typeof MediaRecorder.isTypeSupported !== "function") return "";
    for (const c of ["audio/webm;codecs=opus", "audio/webm", "audio/mp4", "audio/ogg;codecs=opus"]) {
      if (MediaRecorder.isTypeSupported(c)) return c;
    }
    return "";
  }

  function showError(msg: string) {
    error = msg;
    clearTimeout(errorTimer);
    errorTimer = setTimeout(() => (error = ""), 5000);
  }

  function releaseStream() {
    stream?.getTracks().forEach((t) => t.stop());
    stream = null;
    recorder = null;
    clearTimeout(maxTimer);
  }

  async function start() {
    error = "";
    if (!window.isSecureContext) {
      showError("Microphone needs HTTPS (or localhost)");
      return;
    }
    if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") {
      showError("Voice input isn't supported in this browser");
      return;
    }
    try {
      // Must be called directly from the tap gesture (iOS requirement).
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch (e) {
      const name = e instanceof DOMException ? e.name : "";
      showError(
        name === "NotAllowedError" || name === "SecurityError"
          ? "Microphone access denied — allow it in browser settings"
          : "Could not open the microphone",
      );
      return;
    }
    const picked = pickMimeType();
    try {
      recorder = new MediaRecorder(stream, {
        ...(picked ? { mimeType: picked } : {}),
        audioBitsPerSecond: 32_000,
      });
    } catch {
      releaseStream();
      showError("Voice input isn't supported in this browser");
      return;
    }
    chunks = [];
    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) chunks.push(e.data);
    };
    recorder.onstop = () => {
      const type = recorder?.mimeType || picked || "audio/mp4";
      releaseStream();
      void finish(new Blob(chunks, { type }));
    };
    recorder.start();
    phase = "recording";
    maxTimer = setTimeout(() => stop(), MAX_RECORD_MS);
  }

  function stop() {
    if (recorder && recorder.state !== "inactive") {
      phase = "transcribing";
      recorder.stop(); // onstop uploads the assembled blob
    }
  }

  async function finish(blob: Blob) {
    if (destroyed) return;
    if (blob.size === 0) {
      phase = "idle";
      showError("Nothing was recorded");
      return;
    }
    phase = "transcribing";
    try {
      const text = (await transcribeAudio(blob)).trim();
      if (destroyed) return;
      if (text) onText(text);
      else showError("Didn't catch anything — try again");
    } catch (e) {
      if (destroyed) return;
      showError(e instanceof Error ? e.message : "Transcription failed");
    } finally {
      if (!destroyed) phase = "idle";
    }
  }

  function toggle() {
    if (phase === "recording") stop();
    else if (phase === "idle") void start();
  }

  onDestroy(() => {
    destroyed = true;
    clearTimeout(errorTimer);
    if (recorder && recorder.state !== "inactive") recorder.stop();
    releaseStream();
  });

  const title = $derived(
    error ||
      (phase === "recording"
        ? "Stop recording"
        : phase === "transcribing"
          ? "Transcribing…"
          : "Speak instead of typing"),
  );
</script>

<button
  type="button"
  class={`voice-btn ${size}`}
  class:recording={phase === "recording"}
  class:err={!!error}
  disabled={disabled || phase === "transcribing"}
  onclick={toggle}
  {title}
  aria-label={title}
>
  {#if phase === "transcribing"}
    <span class="voice-spinner" aria-hidden="true"></span>
  {:else if phase === "recording"}
    <span class="voice-rec-dot" aria-hidden="true"></span>
  {:else}
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="9" y="3" width="6" height="11" rx="3" />
      <path d="M5 11a7 7 0 0 0 14 0" />
      <line x1="12" y1="18" x2="12" y2="21" />
    </svg>
  {/if}
</button>

<style>
  .voice-btn {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: 1px solid var(--field-line, #d8cdbc);
    border-radius: 50%;
    background: transparent;
    color: var(--teal, #2f6e60);
    cursor: pointer;
    padding: 0;
    transition:
      background 0.15s ease,
      border-color 0.15s ease;
  }
  .voice-btn.md {
    width: 36px;
    height: 36px;
  }
  .voice-btn.sm {
    width: 30px;
    height: 30px;
  }
  .voice-btn:hover:not(:disabled) {
    background: rgba(47, 110, 96, 0.08);
  }
  .voice-btn:disabled {
    opacity: 0.55;
    cursor: default;
  }
  .voice-btn svg {
    width: 58%;
    height: 58%;
  }
  .voice-btn.recording {
    border-color: #c0392b;
    color: #c0392b;
    background: rgba(192, 57, 43, 0.08);
  }
  .voice-btn.err {
    border-color: #c0392b;
    color: #c0392b;
  }
  .voice-rec-dot {
    width: 38%;
    height: 38%;
    border-radius: 50%;
    background: #c0392b;
    animation: voice-pulse 1.1s ease-in-out infinite;
  }
  @keyframes voice-pulse {
    0%,
    100% {
      transform: scale(1);
      opacity: 1;
    }
    50% {
      transform: scale(0.72);
      opacity: 0.6;
    }
  }
  .voice-spinner {
    width: 50%;
    height: 50%;
    border-radius: 50%;
    border: 2px solid rgba(47, 110, 96, 0.25);
    border-top-color: var(--teal, #2f6e60);
    animation: voice-spin 0.8s linear infinite;
  }
  @keyframes voice-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
