<script lang="ts">
  import type { UsageEstimate } from "./types";

  let {
    usage,
    compact = false,
  }: {
    usage: UsageEstimate;
    // compact drops the row labels (for tight spots like list cards).
    compact?: boolean;
  } = $props();

  // Threshold palette shared with UsageChip: green < 60, amber 60–85, red ≥ 85.
  function tone(pct: number): { fill: string; track: string; ink: string } {
    if (pct >= 85) return { fill: "#D2402F", track: "#F2D8D2", ink: "#C0392B" };
    if (pct >= 60) return { fill: "#C6912F", track: "#F1E6CE", ink: "#8A6E22" };
    return { fill: "#4F9E78", track: "#DBEAE0", ink: "#3E7E5F" };
  }

  const five = $derived(Math.max(0, usage?.five_hour_percent ?? 0));
  const week = $derived(Math.max(0, usage?.weekly_percent ?? 0));

  function fmtPct(p: number): string {
    if (p > 0 && p < 1) return "<1%";
    return `${Math.round(p)}%`;
  }

  function fmtTokens(n: number): string {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
    return `${n}`;
  }

  const tip = $derived(
    `≈${fmtPct(five)} of your 5-hour limit · ≈${fmtPct(week)} of your weekly limit\n` +
      `${fmtTokens(usage?.tokens ?? 0)} tokens consumed` +
      (usage?.calibrated ? " (estimated)" : " (estimated — calibrating)"),
  );
</script>

<div class="usage-bars" title={tip} aria-label={tip}>
  <div class="ub-row">
    {#if !compact}<span class="ub-label">5h</span>{/if}
    <div class="ub-track" style={`background:${tone(five).track}`}>
      <div class="ub-fill" style={`width:${Math.min(100, five)}%;background:${tone(five).fill}`}></div>
    </div>
    <span class="ub-pct mono" style={`color:${tone(five).ink}`}>~{fmtPct(five)}</span>
  </div>
  <div class="ub-row">
    {#if !compact}<span class="ub-label">wk</span>{/if}
    <div class="ub-track" style={`background:${tone(week).track}`}>
      <div class="ub-fill" style={`width:${Math.min(100, week)}%;background:${tone(week).fill}`}></div>
    </div>
    <span class="ub-pct mono" style={`color:${tone(week).ink}`}>~{fmtPct(week)}</span>
  </div>
</div>

<style>
  .usage-bars {
    display: flex;
    flex-direction: column;
    gap: 4px;
    width: 100%;
  }
  .ub-row {
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .ub-label {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.04em;
    color: #93856f;
    width: 16px;
    flex: none;
    text-transform: uppercase;
  }
  .ub-track {
    position: relative;
    flex: 1;
    height: 6px;
    border-radius: 4px;
    overflow: hidden;
  }
  .ub-fill {
    height: 100%;
    border-radius: 4px;
    transition: width 0.6s cubic-bezier(0.34, 1.2, 0.5, 1);
  }
  .ub-pct {
    font-size: 10.5px;
    font-weight: 600;
    min-width: 34px;
    text-align: right;
    flex: none;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
</style>
