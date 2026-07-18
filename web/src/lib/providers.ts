import type { Component } from "svelte";
import ClaudeLogo from "./logos/ClaudeLogo.svelte";
import CodexLogo from "./logos/CodexLogo.svelte";

// One entry per supported provider. Adding a provider on the backend means one
// entry here plus a logo component in ./logos — every provider toggle, badge,
// color, usage-window mapping, and question-flow gate derives from this list.
interface ProviderMetaShape {
  id: string;
  label: string; // "Claude"
  logo: Component<{ size?: number }, Record<string, never>, "">;
  // Pill/badge palette (UsageChip, Skills MCP badge).
  accent: { ink: string; bg: string; bd: string };
  // providerChip() palette (theme.ts) — historically distinct from accent.
  chip: { ink: string; bg: string; bd: string };
  // RunTargetPicker compact dot (color = accent.ink).
  dotShape: "circle" | "diamond";
  // Skills MCP source badge glyph.
  badgeGlyph: "circle" | "square" | "diamond";
  profileDir: {
    bodyKey: "config_dir" | "home_dir"; // profile create/update request field
    infoKey: "ConfigDir" | "HomeDir"; // ProfileInfo response field
    placeholder: string; // profile dir input placeholder
  };
  usage: {
    sessionKeys: readonly string[]; // UsageWindow keys for the session bar
    weeklyKeys: readonly string[]; // UsageWindow keys for the weekly bar
    footnote: string; // usage popover footer copy
  };
  // true = the provider's user-input questions end the turn (the pending
  // modal survives "done" and the answer is sent as a follow-up turn).
  // false = questions block mid-turn (the answer resumes via the broker and
  // the modal is stale after "done").
  questionEndsTurn: boolean;
}

export const PROVIDERS = [
  {
    id: "claude",
    label: "Claude",
    logo: ClaudeLogo,
    accent: { ink: "#B0572F", bg: "#F8EBE2", bd: "#ECD3C2" },
    chip: { ink: "#B14E2A", bg: "#FBEAE0", bd: "#F2D6C5" },
    dotShape: "circle",
    badgeGlyph: "square",
    profileDir: {
      bodyKey: "config_dir",
      infoKey: "ConfigDir",
      placeholder: "CLAUDE_CONFIG_DIR — optional",
    },
    usage: {
      sessionKeys: ["five_hour"],
      weeklyKeys: ["seven_day"],
      footnote: "Counts usage across claude.ai, Claude Code & Podiom for this profile.",
    },
    questionEndsTurn: true,
  },
  {
    id: "codex",
    label: "Codex",
    logo: CodexLogo,
    accent: { ink: "#4B5560", bg: "#EAEEF1", bd: "#D6DCE2" },
    chip: { ink: "#2F6E60", bg: "#E2F0EC", bd: "#C7E2DA" },
    dotShape: "diamond",
    badgeGlyph: "diamond",
    profileDir: {
      bodyKey: "home_dir",
      infoKey: "HomeDir",
      placeholder: "CODEX_HOME — optional",
    },
    usage: {
      sessionKeys: ["primary"],
      weeklyKeys: ["secondary"],
      footnote: "Counts usage across ChatGPT, Codex & Podiom for this profile.",
    },
    questionEndsTurn: false,
  },
] as const satisfies readonly ProviderMetaShape[];

export type Provider = (typeof PROVIDERS)[number]["id"]; // "claude" | "codex"
export type ProviderMeta = ProviderMetaShape;

export const DEFAULT_PROVIDER: Provider = PROVIDERS[0].id;

export function isProvider(v: string): v is Provider {
  return PROVIDERS.some((p) => p.id === v);
}

// Unknown/empty ids fall back to the first entry — matching the historical
// "else → Claude" branches, so a provider the UI doesn't know yet degrades
// gracefully instead of crashing.
export function providerMeta(id: string | null | undefined): ProviderMeta {
  return PROVIDERS.find((p) => p.id === id) ?? PROVIDERS[0];
}

export function questionEndsTurn(provider: string | null | undefined): boolean {
  return providerMeta(provider).questionEndsTurn;
}
