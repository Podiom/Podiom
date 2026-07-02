// Pure helpers for rendering an agent's MEMORY.md. The file on disk is clean
// user-editable markdown (## sections, - bullets); per-item metadata (since
// dates, NEW badges) lives DB-side in the dream journal and is matched back to
// items here by text, so the file never carries inline annotations.

import type { Dream, DreamNewItem } from "./types";

export interface MemoryItem {
  text: string;
  since?: string; // ISO date the item first appeared, from the dream journal
  isNew?: boolean; // added by the most recent successful dream
}

export interface MemorySection {
  title: string;
  items: MemoryItem[];
}

export interface ParsedMemory {
  intro: string; // any prose before the first section (rare)
  sections: MemorySection[];
}

// normalize collapses whitespace so item text matches across the file and the
// dream journal even if wrapping differs.
function normalize(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

// parseMemory splits MEMORY.md into sections and bullet items, ignoring the
// `# Memory` heading and HTML comments. Dreams (newest first) supply the since
// date and NEW flag for each item, matched by normalized text.
export function parseMemory(md: string, dreams: Dream[] = []): ParsedMemory {
  const sections: MemorySection[] = [];
  const introLines: string[] = [];
  let current: MemorySection | null = null;

  for (const rawLine of (md ?? "").split("\n")) {
    const line = rawLine.trim();
    if (line === "" || line.startsWith("<!--")) continue;
    if (line.startsWith("# ")) continue; // top-level title
    if (line.startsWith("## ")) {
      current = { title: line.slice(3).trim(), items: [] };
      sections.push(current);
      continue;
    }
    const bullet = line.match(/^[-*]\s+(.*)$/);
    if (bullet) {
      const text = normalize(bullet[1].replace(/<!--.*?-->/g, ""));
      if (!text) continue;
      const item: MemoryItem = { text };
      const meta = matchItemMeta(current?.title ?? "", text, dreams);
      if (meta.since) item.since = meta.since;
      if (meta.isNew) item.isNew = true;
      if (current) current.items.push(item);
      continue;
    }
    if (!current) introLines.push(line);
  }

  return { intro: introLines.join(" ").trim(), sections };
}

// matchItemMeta finds the oldest dream that introduced an item (its since date)
// and whether the newest successful dream introduced it (NEW badge).
function matchItemMeta(
  section: string,
  text: string,
  dreams: Dream[],
): { since?: string; isNew?: boolean } {
  const wantSection = normalize(section);
  const wantText = text; // already normalized
  let since: string | undefined;
  let isNew = false;

  const successful = dreams
    .filter((d) => d.Status === "success")
    .slice()
    .sort((a, b) => a.RanAt.localeCompare(b.RanAt)); // oldest first

  successful.forEach((dream, idx) => {
    const items: DreamNewItem[] = dream.NewItems ?? [];
    const hit = items.some(
      (it) => normalize(it.text) === wantText && normalize(it.section) === wantSection,
    );
    if (hit) {
      if (!since) since = dream.RanAt;
      if (idx === successful.length - 1) isNew = true; // most recent dream
    }
  });

  return { since, isNew };
}

// relativeTime renders an ISO timestamp as a short "2h ago" style label.
export function relativeTime(iso: string | undefined | null): string {
  if (!iso) return "never";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "never";
  const delta = Date.now() - then;
  const min = Math.floor(delta / 60000);
  if (min < 1) return "just now";
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const days = Math.floor(hr / 24);
  return `${days}d ago`;
}

// shortDate renders an ISO date as "Jun 26" for the item since-marker.
export function shortDate(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}
