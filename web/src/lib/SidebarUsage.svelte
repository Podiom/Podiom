<script lang="ts">
  import { onMount } from "svelte";
  import ProviderLogo from "./ProviderLogo.svelte";
  import { PROVIDERS, providerMeta } from "./providers";
  import type { Provider, ProviderAuthStatus, UsageSnapshot, UsageWindow } from "./types";

  const COLLAPSED_KEY = "podiom.sidebar-usage-collapsed";

  let {
    snapshots,
    authStatuses,
    onOpenSignIn,
  }: {
    snapshots: UsageSnapshot[];
    authStatuses: ProviderAuthStatus[];
    onOpenSignIn: (provider: Provider, profile: string) => void;
  } = $props();

  interface UsageRow {
    key: string;
    snapshot: UsageSnapshot;
    authProfile: string;
    label: string;
    sessionWindow?: UsageWindow;
    weeklyWindow?: UsageWindow;
    primaryWindow?: UsageWindow;
    primaryLabel: "5h" | "Weekly";
    expandable: boolean;
  }

  let expandedKey = $state<string | null>(null);
  let collapsed = $state(false);
  let now = $state(Date.now());

  onMount(() => {
    try {
      const saved = window.localStorage.getItem(COLLAPSED_KEY);
      collapsed = saved === "true" ? true : saved === "false" ? false : false;
    } catch {
      collapsed = false;
    }
    const id = window.setInterval(() => (now = Date.now()), 60_000);
    return () => window.clearInterval(id);
  });

  const rows = $derived.by(() => {
    const result: UsageRow[] = [];
    for (const provider of PROVIDERS) {
      const meta = providerMeta(provider.id);
      const accounts = snapshots
        .filter((snapshot) => snapshot.provider === provider.id)
        .sort((a, b) => {
          if (a.default !== b.default) return a.default ? -1 : 1;
          return a.profile.localeCompare(b.profile);
        });

      for (const snapshot of accounts) {
        const sessionWindow = snapshot.windows?.find((window) =>
          meta.usage.sessionKeys.includes(window.key),
        );
        const weeklyWindow = snapshot.windows?.find((window) =>
          meta.usage.weeklyKeys.includes(window.key),
        );
        result.push({
          key: `${snapshot.provider}:${snapshot.profile}`,
          snapshot,
          authProfile: snapshot.default ? "" : snapshot.profile,
          label: snapshot.default ? provider.label : `${provider.label} · ${snapshot.profile}`,
          sessionWindow,
          weeklyWindow,
          primaryWindow: sessionWindow ?? weeklyWindow,
          primaryLabel: sessionWindow ? "5h" : "Weekly",
          expandable: Boolean(sessionWindow && weeklyWindow),
        });
      }
    }
    return result;
  });

  function authFor(row: UsageRow): ProviderAuthStatus | undefined {
    return authStatuses.find(
      (status) => status.provider === row.snapshot.provider && status.profile === row.authProfile,
    );
  }

  function usageNeedsSignIn(snapshot: UsageSnapshot): boolean {
    return snapshot.status === "no_credentials"
      || snapshot.status === "stale_credentials"
      || snapshot.status === "unauthorized";
  }

  function rowNeedsSignIn(row: UsageRow): boolean {
    const auth = authFor(row);
    return usageNeedsSignIn(row.snapshot) || Boolean(auth?.found && auth.login_checked && !auth.logged_in);
  }

  const hasSignInWarning = $derived(rows.some(rowNeedsSignIn));

  function clampPercent(percent: number): number {
    if (!Number.isFinite(percent)) return 0;
    return Math.min(100, Math.max(0, percent));
  }

  function tone(percent: number): { fill: string; track: string; ink: string } {
    const value = clampPercent(percent);
    if (value >= 85) return { fill: "#D2402F", track: "#F2D8D2", ink: "#C0392B" };
    if (value >= 60) return { fill: "#C6912F", track: "#F1E6CE", ink: "#8A6E22" };
    return { fill: "#4F9E78", track: "#DBEAE0", ink: "#3E7E5F" };
  }

  function resetText(iso?: string): string {
    if (!iso) return "";
    const target = new Date(iso).getTime();
    if (Number.isNaN(target)) return "";
    const diff = target - now;
    if (diff <= 0) return "Resets now";
    const minutes = Math.ceil(diff / 60_000);
    const days = Math.floor(minutes / 1440);
    const hours = Math.floor((minutes % 1440) / 60);
    const mins = minutes % 60;
    if (days > 0) return `Resets in ${days}d ${hours}h`;
    if (hours > 0) return `Resets in ${hours}h ${mins}m`;
    return `Resets in ${mins}m`;
  }

  function absoluteReset(iso?: string): string | undefined {
    if (!iso) return undefined;
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) return undefined;
    return date.toLocaleString();
  }

  function statusMessage(snapshot: UsageSnapshot): string {
    switch (snapshot.status) {
      case "no_credentials":
        return "Not signed in";
      case "stale_credentials":
        return "Credentials need refresh";
      case "unauthorized":
        return "Sign-in expired";
      case "rate_limited":
        return "Temporarily rate limited";
      case "unsupported":
        return "Usage unavailable";
      case "error":
        return "Couldn't load usage";
      default:
        return "No usage data";
    }
  }

  function toggle(row: UsageRow) {
    if (!row.expandable) return;
    expandedKey = expandedKey === row.key ? null : row.key;
  }

  function toggleCollapsed() {
    collapsed = !collapsed;
    if (collapsed) expandedKey = null;
    try {
      window.localStorage.setItem(COLLAPSED_KEY, String(collapsed));
    } catch {
      // Storage can be blocked; the control still works for this page load.
    }
  }

  function openSignIn(row: UsageRow) {
    onOpenSignIn(row.snapshot.provider, row.authProfile);
  }
</script>

{#snippet warningTriangle(label: string)}
  <span class="warning-triangle" title={label} aria-label={label}>
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.3" stroke-linecap="round" stroke-linejoin="round"><path d="M10.3 3.7 2.4 18a2 2 0 0 0 1.8 3h15.6a2 2 0 0 0 1.8-3L13.7 3.7a2 2 0 0 0-3.4 0Z" /><path d="M12 9v4" /><path d="M12 17h.01" /></svg>
  </span>
{/snippet}

{#snippet accountName(row: UsageRow, showChevron = false)}
  <span class="account-name" style={`color:${providerMeta(row.snapshot.provider).accent.ink}`}>
    <ProviderLogo provider={row.snapshot.provider} size={13} />
    <span>{row.label}</span>
    {#if rowNeedsSignIn(row)}
      {@render warningTriangle(`${row.label} requires sign-in`)}
    {/if}
  </span>
  {#if showChevron && row.expandable}
    <span class="chevron" class:expanded={expandedKey === row.key} aria-hidden="true">⌄</span>
  {/if}
{/snippet}

{#snippet meter(window: UsageWindow, label: "5h" | "Weekly")}
  {@const percent = clampPercent(window.used_percent)}
  <div class="meter-head">
    <span>{label}</span>
    <span class="meter-percent mono" style={`color:${tone(percent).ink}`}>{Math.round(percent)}%</span>
  </div>
  <div class="meter-track" style={`background:${tone(percent).track}`}>
    <span class="meter-fill" style={`width:${percent}%;background:${tone(percent).fill}`}></span>
  </div>
  {#if resetText(window.resets_at)}
    <span class="reset-text" title={absoluteReset(window.resets_at)}>{resetText(window.resets_at)}</span>
  {/if}
{/snippet}

{#snippet accountContent(row: UsageRow)}
  <span class="account-head">
    {@render accountName(row, true)}
  </span>
  {#if row.primaryWindow}
    <span class="meter">
      {@render meter(row.primaryWindow, row.primaryLabel)}
    </span>
  {/if}
{/snippet}

{#if rows.length > 0}
  <section class="usage-card" aria-label="Provider usage">
    <button
      class="usage-header"
      type="button"
      aria-expanded={!collapsed}
      aria-controls="sidebar-usage-list"
      onclick={toggleCollapsed}
    >
      <span class="usage-title mono">USAGE</span>
      <span class="usage-header-icons">
        {#if hasSignInWarning}
          {@render warningTriangle("One or more provider accounts require sign-in")}
        {/if}
        <span class="usage-chevron" class:collapsed aria-hidden="true">⌄</span>
      </span>
    </button>
    {#if !collapsed}
      <div class="usage-list" id="sidebar-usage-list">
        {#each rows as row, index (row.key)}
          <div class="account-row">
            {#if row.snapshot.status === "ok" && row.primaryWindow}
              {#if row.expandable}
                <button
                  class="account-summary interactive"
                  type="button"
                  aria-expanded={expandedKey === row.key}
                  aria-controls={`sidebar-usage-${index}`}
                  onclick={() => toggle(row)}
                >
                  {@render accountContent(row)}
                </button>
              {:else}
                <div class="account-summary">
                  {@render accountContent(row)}
                </div>
              {/if}

              {#if rowNeedsSignIn(row)}
                <button class="signin-link usage-signin-link" type="button" onclick={() => openSignIn(row)}>
                  Sign-in required
                </button>
              {/if}

              {#if row.expandable && expandedKey === row.key && row.weeklyWindow}
                <div class="weekly-detail" id={`sidebar-usage-${index}`}>
                  {@render meter(row.weeklyWindow, "Weekly")}
                </div>
              {/if}
            {:else}
              <div class="status-row">
                <span class="account-head">{@render accountName(row)}</span>
                {#if usageNeedsSignIn(row.snapshot)}
                  <button class="signin-link status-text" type="button" onclick={() => openSignIn(row)}>
                    {statusMessage(row.snapshot)}
                  </button>
                {:else}
                  <span class="status-text">{statusMessage(row.snapshot)}</span>
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </section>
{/if}

<style>
  .usage-card {
    overflow: hidden;
    border: 1px solid var(--line-3);
    border-radius: 12px;
    background: var(--surface-3);
  }

  .usage-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 9px 10px 7px;
    border: 0;
    background: transparent;
    color: inherit;
    text-align: left;
    cursor: pointer;
  }

  .usage-header:hover {
    background: #f6efe6;
  }

  .usage-header:focus-visible {
    outline: 2px solid var(--teal);
    outline-offset: -2px;
  }

  .usage-title {
    color: var(--faint);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.08em;
  }

  .usage-header-icons {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .warning-triangle {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
    color: #a86b18;
  }

  .usage-chevron {
    color: var(--faint);
    font-size: 14px;
    line-height: 1;
    transition: transform 0.15s ease;
  }

  .usage-chevron.collapsed {
    transform: rotate(-90deg);
  }

  .usage-list {
    max-height: min(320px, 40dvh);
    overflow-y: auto;
    overscroll-behavior: contain;
  }

  .account-row {
    border-top: 1px solid var(--line-3);
  }

  .account-summary,
  .status-row {
    display: block;
    width: 100%;
    padding: 9px 10px 10px;
    border: 0;
    background: transparent;
    color: inherit;
    text-align: left;
  }

  .account-summary.interactive {
    cursor: pointer;
  }

  .account-summary.interactive:hover {
    background: #f6efe6;
  }

  .account-summary.interactive:focus-visible {
    outline: 2px solid var(--teal);
    outline-offset: -2px;
  }

  .account-head,
  .account-name {
    display: flex;
    align-items: center;
    min-width: 0;
  }

  .account-head {
    justify-content: space-between;
    gap: 8px;
  }

  .account-name {
    gap: 6px;
    font: 700 11.5px "Hanken Grotesk";
  }

  .account-name > span:not(.warning-triangle) {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .chevron {
    flex: none;
    color: var(--faint);
    font-size: 14px;
    line-height: 1;
    transition: transform 0.15s ease;
  }

  .chevron.expanded {
    transform: rotate(180deg);
  }

  .meter {
    display: block;
    margin-top: 7px;
  }

  .meter-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: 4px;
    color: var(--muted-2);
    font-size: 10px;
    font-weight: 600;
  }

  .meter-percent {
    font-size: 10px;
    font-weight: 700;
  }

  .meter-track {
    height: 6px;
    overflow: hidden;
    border-radius: 999px;
  }

  .meter-fill {
    display: block;
    height: 100%;
    border-radius: inherit;
    transition: width 0.5s ease;
  }

  .reset-text,
  .status-text {
    display: block;
    margin-top: 5px;
    color: var(--faint);
    font: 400 10px/1.3 "Hanken Grotesk";
  }

  .signin-link {
    padding: 0;
    border: 0;
    background: transparent;
    color: #8d5e17;
    font-family: "Hanken Grotesk";
    text-align: left;
    text-decoration: underline;
    text-underline-offset: 2px;
    cursor: pointer;
  }

  .signin-link:hover {
    color: #68440e;
  }

  .signin-link:focus-visible {
    border-radius: 2px;
    outline: 2px solid var(--teal);
    outline-offset: 2px;
  }

  .usage-signin-link {
    display: block;
    margin: -4px 10px 9px 29px;
    font-size: 10px;
    line-height: 1.3;
  }

  .weekly-detail {
    padding: 8px 10px 10px 29px;
    border-top: 1px dashed var(--line-3);
    background: rgba(255, 253, 251, 0.5);
  }
</style>
