<script lang="ts">
  // A small conic-gradient ring showing how full the active session's context
  // window is (tokens used / model window), colour-coded by fullness. Mirrors the
  // ring technique + threshold palette used by UsageChip, but expresses a
  // different concept (context fill vs. provider plan quota), so it stays its own
  // component. Hidden entirely until the provider reports a window (max > 0).

  let { used, max }: { used: number; max: number } = $props();

  const pct = $derived(max > 0 ? Math.min(100, (used / max) * 100) : 0);

  // Threshold palette: green under 60 %, amber 60–85 %, red at/above 85 %.
  function tone(p: number): { fill: string; ink: string } {
    if (p >= 85) return { fill: "#D2402F", ink: "#C0392B" };
    if (p >= 60) return { fill: "#C6912F", ink: "#8A6E22" };
    return { fill: "#4F9E78", ink: "#3E7E5F" };
  }
  const ringStyle = $derived(
    `background: conic-gradient(${tone(pct).fill} ${pct * 3.6}deg, #EFE6D8 0);`,
  );

  function fmt(n: number): string {
    if (n >= 1000) return `${Math.round(n / 1000)}k`;
    return `${n}`;
  }
  const label = $derived(`Context: ${fmt(used)} / ${fmt(max)} tokens · ${Math.round(pct)}%`);
</script>

{#if max > 0}
  <span class="ctx-ring-wrap" title={label} aria-label={label}>
    <span class="ctx-ring" style={ringStyle}><span class="ctx-ring-center"></span></span>
    <span class="ctx-ring-pct mono" style={`color:${tone(pct).ink}`}>{Math.round(pct)}%</span>
  </span>
{/if}

<style>
  .ctx-ring-wrap {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    flex: 0 0 auto;
    cursor: default;
    user-select: none;
  }
  .ctx-ring {
    width: 18px;
    height: 18px;
    border-radius: 50%;
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  .ctx-ring-center {
    width: 11px;
    height: 11px;
    border-radius: 50%;
    background: #fff;
  }
  .ctx-ring-pct {
    font-size: 11px;
    font-weight: 600;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
</style>
