// Visual helpers ported from the Podiom design comp. The backend agent model
// has no avatar/gradient/soul fields, so we derive a stable gradient and
// initial from the agent name — same name always maps to the same look.

import type { Provider, PermissionMode, SessionOrigin } from "./types";

export const GRADIENTS = [
  "linear-gradient(150deg,#E8763B,#C24528)",
  "linear-gradient(150deg,#46A08C,#2F6E60)",
  "linear-gradient(150deg,#7C6FE0,#5847B8)",
  "linear-gradient(150deg,#4A8FD6,#2D5FA8)",
  "linear-gradient(150deg,#D85C7E,#A8395A)",
  "linear-gradient(150deg,#C99A3C,#9A6E22)",
  "linear-gradient(150deg,#5BAE8A,#37806A)",
  "linear-gradient(150deg,#8A8276,#5A5248)",
];

export const PROJECT_COLORS = [
  "#D9663D",
  "#3F8F7E",
  "#7C6FE0",
  "#C99A3C",
  "#B07CC9",
  "#5AA9C9",
  "#D08770",
  "#6E86C9",
  "#4F9E78",
  "#C4607F",
];

function hash(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return h;
}

export function agentGradient(name: string): string {
  return GRADIENTS[hash(name || "?") % GRADIENTS.length];
}

export function projectColor(id: string): string {
  return PROJECT_COLORS[hash(id || "?") % PROJECT_COLORS.length];
}

export function initial(name: string): string {
  return (name || "?").trim().slice(0, 1).toUpperCase();
}

// Square gradient avatar with centered monogram.
export function avatarStyle(grad: string, size: number, radius: number, fs: number): string {
  return [
    `width:${size}px`,
    `height:${size}px`,
    "flex:none",
    `border-radius:${radius}px`,
    `background:${grad}`,
    "color:#fff",
    "display:flex",
    "align-items:center",
    "justify-content:center",
    `font:800 ${fs}px 'Hanken Grotesk'`,
    "box-shadow:0 8px 18px -8px rgba(80,40,20,.45)",
  ].join(";");
}

export function providerChip(p: Provider | string): string {
  const base =
    "padding:3px 9px;border-radius:999px;font:600 10px 'JetBrains Mono',monospace;";
  return p === "codex"
    ? base + "background:#E2F0EC;border:1px solid #C7E2DA;color:#2F6E60"
    : base + "background:#FBEAE0;border:1px solid #F2D6C5;color:#B14E2A";
}

export function modeChip(m: PermissionMode | string): string {
  const base =
    "padding:3px 9px;border-radius:999px;font:600 10px 'JetBrains Mono',monospace;";
  return m === "yolo"
    ? base + "background:#F8E0D6;border:1px solid #EFC3AF;color:#B14E2A"
    : base + "background:#EAF1ED;border:1px solid #CFE3D8;color:#3F7A5F";
}

const ORIGIN_MAP: Record<string, string> = {
  web: "background:#FBEAE0;color:#B14E2A",
  cli: "background:#EEEAFB;color:#5847B8",
  onboarding: "background:#EAF1ED;color:#3F7A5F",
  schedule: "background:#FBF1DD;color:#9A6E1E",
  roadmap: "background:#E3F1EC;color:#2F6E60",
  goal: "background:#EEEAFB;color:#5847B8",
};

export function originStyle(o: SessionOrigin | string): string {
  return (
    "display:inline-flex;align-items:center;gap:5px;padding:4px 10px;border-radius:999px;" +
    "font:600 10.5px 'JetBrains Mono',monospace;white-space:nowrap;" +
    (ORIGIN_MAP[o] || ORIGIN_MAP.web)
  );
}

export function originLabel(o: SessionOrigin | string): string {
  if (o === "onboarding") return "✦ onboarding";
  if (o === "schedule") return "⟳ schedule";
  if (o === "roadmap") return "▤ roadmap";
  if (o === "goal") return "◎ goal";
  return String(o);
}
