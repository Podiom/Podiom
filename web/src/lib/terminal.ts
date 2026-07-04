import { apiUrl } from "./base";
import type { Provider } from "./types";

const PROFILE_RE = /^[A-Za-z0-9._-]{1,64}$/;

export type TerminalFlow = Provider | "onboard" | "shell";

export function terminalUrl(flow: TerminalFlow, profile = ""): string {
  const cleanProfile = profile.trim();
  const parts = ["terminal", flow];
  if ((flow === "claude" || flow === "codex") && cleanProfile && PROFILE_RE.test(cleanProfile)) {
    parts.push(cleanProfile);
  }
  return apiUrl(`${parts.join("/")}/`).toString();
}

export function validTerminalProfile(profile: string): boolean {
  const trimmed = profile.trim();
  return trimmed === "" || PROFILE_RE.test(trimmed);
}
