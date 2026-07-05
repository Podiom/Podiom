// Shared visual helpers + formatting for the skill marketplace pages. Colors
// come from the same warm-parchment palette the rest of the dashboard uses.
import type { SkillRegistry } from "../../lib/types";

export interface ChipStyle {
  label: string;
  fg: string;
  bg: string;
  bd: string;
}

// REGISTRY badges. `anthropics` renders as the "Verified" badge (it is the
// curated source); skillsmp/github get their own muted chips.
export const REGISTRY: Record<SkillRegistry, ChipStyle> = {
  skillsmp: { label: "SkillsMP", fg: "#4B5560", bg: "#EAEEF1", bd: "#D6DCE2" },
  anthropics: { label: "Verified", fg: "#2F6E60", bg: "#E7F0EC", bd: "#C7DBD2" },
  github: { label: "GitHub", fg: "#8A7560", bg: "#F3ECE2", bd: "#E6DBCC" },
};

export function chip(fg: string, bg: string, bd: string): string {
  return `display:inline-flex;align-items:center;gap:6px;padding:4px 9px;border-radius:8px;font:600 11px 'JetBrains Mono',monospace;color:${fg};background:${bg};border:1px solid ${bd};white-space:nowrap`;
}

export function registryChip(registry: SkillRegistry): string {
  const r = REGISTRY[registry] ?? REGISTRY.github;
  return chip(r.fg, r.bg, r.bd);
}

export function registryLabel(registry: SkillRegistry): string {
  return (REGISTRY[registry] ?? REGISTRY.github).label;
}

// scriptsChip is the "Contains executable scripts" warning badge (FR-9).
export function scriptsChip(): string {
  return chip("#9A6B1A", "#FBF1DD", "#ECD8A6");
}

export function shortSHA(sha?: string): string {
  return sha ? sha.slice(0, 7) : "";
}

export function formatDate(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

export function popularity(stars?: number, installs?: number): string {
  const parts: string[] = [];
  if (stars) parts.push(`★ ${compact(stars)}`);
  if (installs) parts.push(`${compact(installs)} installs`);
  return parts.join(" · ");
}

function compact(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}k`;
  return String(n);
}

// installPath is where a skill of the given name lands on disk (FR-13).
export function installPath(name: string): string {
  return `~/.agents/skills/${kebab(name)}/`;
}

export function kebab(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
