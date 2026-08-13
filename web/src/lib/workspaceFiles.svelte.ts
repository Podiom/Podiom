import { writable } from "svelte/store";

export const workspaceFileViewer = writable<string | null>(null);
let returnFocus: HTMLElement | null = null;

export function openWorkspaceFile(id: string) {
  returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  workspaceFileViewer.set(id);
}

export function closeWorkspaceFile() {
  workspaceFileViewer.set(null);
  const target = returnFocus;
  returnFocus = null;
  queueMicrotask(() => target?.focus());
}

export interface WorkspaceFileLink {
  id: string;
  label: string;
}

const snapshotLink = /\[((?:\\.|[^\]\\])+)\]\((?:\.\/)?api\/workspace-files\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\)/gi;

export function extractWorkspaceFileLinks(markdown: string): WorkspaceFileLink[] {
  const links: WorkspaceFileLink[] = [];
  const seen = new Set<string>();
  for (const match of markdown.matchAll(snapshotLink)) {
    const id = match[2].toLowerCase();
    if (seen.has(id)) continue;
    seen.add(id);
    links.push({ id, label: match[1].replace(/\\([\\\[\]])/g, "$1") });
  }
  return links;
}

export function workspaceFileIDFromHref(href: string): string | null {
  try {
    const url = new URL(href, document.baseURI);
    const base = new URL(".", document.baseURI);
    if (url.search || url.hash || url.origin !== base.origin || !url.pathname.startsWith(base.pathname)) return null;
    const relative = url.pathname.slice(base.pathname.length);
    const match = relative.match(/^api\/workspace-files\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/i);
    return match?.[1] ?? null;
  } catch {
    return null;
  }
}
