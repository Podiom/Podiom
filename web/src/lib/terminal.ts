import { apiUrl } from "./base";

export type TerminalFlow = "onboard" | "shell";

export function terminalUrl(flow: TerminalFlow): string {
  return apiUrl(`terminal/${flow}/`).toString();
}
