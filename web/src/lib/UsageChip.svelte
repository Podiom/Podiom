<script lang="ts">
  import { onMount } from "svelte";
  import type { Provider, UsageSnapshot, UsageWindow } from "./types";

  let {
    snapshot,
    provider,
    profileLabel,
    open = false,
    onToggle,
  }: {
    snapshot?: UsageSnapshot;
    provider: Provider;
    profileLabel: string;
    open?: boolean;
    onToggle: () => void;
  } = $props();

  // Live 1s ticker so the "resets in …" countdowns advance without a refetch.
  let now = $state(Date.now());
  onMount(() => {
    const id = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(id);
  });

  const PROVIDER_COLORS: Record<string, { ink: string; bg: string; bd: string }> = {
    claude: { ink: "#B0572F", bg: "#F8EBE2", bd: "#ECD3C2" },
    codex: { ink: "#4B5560", bg: "#EAEEF1", bd: "#D6DCE2" },
  };
  const pc = $derived(PROVIDER_COLORS[provider] ?? PROVIDER_COLORS.claude);

  const sessionKeys = $derived(provider === "codex" ? ["primary"] : ["five_hour"]);
  const weeklyKeys = $derived(provider === "codex" ? ["secondary"] : ["seven_day"]);

  function pick(keys: string[]): UsageWindow | undefined {
    return snapshot?.windows?.find((w) => keys.includes(w.key));
  }
  const sessionWin = $derived(pick(sessionKeys));
  const weeklyWin = $derived(pick(weeklyKeys));

  const isOK = $derived(snapshot?.status === "ok");
  const sessionPct = $derived(isOK && sessionWin ? sessionWin.used_percent : 0);

  // Threshold palette: bar fill, track, and percent ink.
  function tone(pct: number): { fill: string; track: string; ink: string } {
    if (pct >= 85) return { fill: "#D2402F", track: "#F2D8D2", ink: "#C0392B" };
    if (pct >= 60) return { fill: "#C6912F", track: "#F1E6CE", ink: "#8A6E22" };
    return { fill: "#4F9E78", track: "#DBEAE0", ink: "#3E7E5F" };
  }
  const ringColor = $derived(isOK ? tone(sessionPct).fill : "#C9BFAF");
  const ringStyle = $derived(
    `background: conic-gradient(${ringColor} ${sessionPct * 3.6}deg, #EFE6D8 0);`,
  );

  function statusMessage(s?: UsageSnapshot): string {
    switch (s?.status) {
      case "no_credentials":
        return "No credentials for this profile.";
      case "stale_credentials":
        return "Stale credentials — run a turn to refresh.";
      case "unauthorized":
        return "Sign-in expired — re-authenticate the provider.";
      case "rate_limited":
        return "Usage endpoint is rate limited; retrying shortly.";
      case "unsupported":
        return "This account has no plan limits to report.";
      case "error":
        return s.error || "Couldn't load usage.";
      default:
        return "No usage data yet.";
    }
  }

  function fmtRelative(iso?: string): string {
    if (!iso) return "";
    const target = new Date(iso).getTime();
    const diff = target - now;
    if (diff <= 0) return "now";
    const mins = Math.floor(diff / 60000);
    const days = Math.floor(mins / 1440);
    const hours = Math.floor((mins % 1440) / 60);
    const m = mins % 60;
    if (days > 0) return `${days}d ${hours}h`;
    if (hours > 0) return `${hours}h ${m}m`;
    return `${m}m`;
  }

  function fmtAbsolute(iso?: string): string {
    if (!iso) return "";
    const d = new Date(iso);
    const date = d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" });
    const time = d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
    return `${date} · ${time}`;
  }

  const footerNote = $derived(
    provider === "codex"
      ? "Counts usage across ChatGPT, Codex & Podium for this profile."
      : "Counts usage across claude.ai, Claude Code & Podium for this profile.",
  );
</script>

<div class="usage-wrap">
  <button class="usage-chip" class:open onclick={onToggle} title="Provider usage limits">
    <span class="usage-ring" style={ringStyle}><span class="usage-ring-center"></span></span>
    <span class="usage-word">usage</span>
    {#if isOK}
      <span class="usage-pct mono" style={`color:${tone(sessionPct).ink}`}>{Math.round(sessionPct)}%</span>
    {:else}
      <span class="usage-pct mono muted">—</span>
    {/if}
    <span class="usage-chev">▾</span>
  </button>

  {#if open}
    <div class="usage-pop">
      <div class="usage-pop-head">
        <span class="usage-pop-title">SESSION LIMITS</span>
        <span class="usage-acct" style={`color:${pc.ink};background:${pc.bg};border:1px solid ${pc.bd}`}>{profileLabel}</span>
      </div>

      {#if isOK}
        {#if sessionWin}
          <div class="usage-row">
            <div class="usage-row-top">
              <span class="usage-row-label">5-hour session</span>
              <span class="usage-row-pct mono" style={`color:${tone(sessionWin.used_percent).ink}`}>
                {Math.round(sessionWin.used_percent)}% used
              </span>
            </div>
            <div class="usage-bar" style={`background:${tone(sessionWin.used_percent).track}`}>
              <div class="usage-bar-fill" style={`width:${Math.min(100, sessionWin.used_percent)}%;background:${tone(sessionWin.used_percent).fill}`}></div>
            </div>
            {#if sessionWin.resets_at}
              <span class="usage-row-reset">resets in {fmtRelative(sessionWin.resets_at)}</span>
            {/if}
          </div>
        {/if}

        {#if weeklyWin}
          <div class="usage-row">
            <div class="usage-row-top">
              <span class="usage-row-label">Weekly · all models</span>
              <span class="usage-row-pct mono" style={`color:${tone(weeklyWin.used_percent).ink}`}>
                {Math.round(weeklyWin.used_percent)}% used
              </span>
            </div>
            <div class="usage-bar" style={`background:${tone(weeklyWin.used_percent).track}`}>
              <div class="usage-bar-fill" style={`width:${Math.min(100, weeklyWin.used_percent)}%;background:${tone(weeklyWin.used_percent).fill}`}></div>
            </div>
            {#if weeklyWin.resets_at}
              <span class="usage-row-reset">Resets {fmtAbsolute(weeklyWin.resets_at)} · in {fmtRelative(weeklyWin.resets_at)}</span>
            {/if}
          </div>
        {/if}

        <div class="usage-foot">{footerNote}</div>
      {:else}
        <div class="usage-status">{statusMessage(snapshot)}</div>
        <div class="usage-foot">{footerNote}</div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .usage-wrap {
    position: relative;
    margin-left: auto;
  }
  .usage-chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 26px;
    padding: 0 9px;
    border: 1px solid #ECE1D4;
    border-radius: 8px;
    background: #FFFDFB;
    color: #6B5E4C;
    font-size: 12px;
    cursor: pointer;
    transition: border-color 0.15s ease, background 0.15s ease;
  }
  .usage-chip:hover,
  .usage-chip.open {
    border-color: #D9C9B4;
    background: #FBF6EF;
  }
  .usage-ring {
    width: 20px;
    height: 20px;
    border-radius: 50%;
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  .usage-ring-center {
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: #fff;
  }
  .usage-word {
    color: #6B5E4C;
  }
  .usage-pct {
    font-weight: 600;
  }
  .usage-pct.muted {
    color: #A99C88;
  }
  .usage-chev {
    font-size: 10px;
    opacity: 0.5;
    transition: transform 0.15s ease;
  }
  .usage-chip.open .usage-chev {
    transform: rotate(180deg);
  }

  .usage-pop {
    position: absolute;
    bottom: calc(100% + 10px);
    right: 0;
    width: 322px;
    padding: 14px;
    background: #FFFDFB;
    border: 1px solid #ECE1D4;
    border-radius: 14px;
    box-shadow: 0 12px 32px rgba(60, 45, 25, 0.16);
    z-index: 40;
  }
  .usage-pop-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 12px;
  }
  .usage-pop-title {
    font-size: 11px;
    letter-spacing: 0.08em;
    color: #A2937C;
    font-weight: 600;
  }
  .usage-acct {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: 999px;
    font-weight: 600;
  }
  .usage-row {
    margin-bottom: 14px;
  }
  .usage-row-top {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: 6px;
  }
  .usage-row-label {
    font-size: 13px;
    color: #4A4032;
  }
  .usage-row-pct {
    font-size: 12px;
    font-weight: 600;
  }
  .usage-bar {
    height: 9px;
    border-radius: 5px;
    overflow: hidden;
  }
  .usage-bar-fill {
    height: 100%;
    border-radius: 5px;
    transition: width 0.8s cubic-bezier(0.34, 1.2, 0.5, 1);
  }
  .usage-row-reset {
    display: block;
    margin-top: 5px;
    font-size: 11px;
    color: #93856F;
  }
  .usage-status {
    font-size: 13px;
    color: #6B5E4C;
    padding: 6px 0 12px;
  }
  .usage-foot {
    font-size: 11px;
    color: #A2937C;
    border-top: 1px solid #F0E7DA;
    padding-top: 10px;
    line-height: 1.4;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
</style>
