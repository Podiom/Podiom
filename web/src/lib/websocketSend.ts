import type { ClientMessage } from "./types";

interface SendableWebSocket {
  readonly readyState: number;
  send(data: string): void;
}

// WebSocket.OPEN is 1. Keeping the value local makes this transport helper
// testable in Vitest's Node environment, where the browser WebSocket global is
// not available.
const SOCKET_OPEN = 1;

export function sendWebSocketMessage(socket: SendableWebSocket | null, message: ClientMessage): boolean {
  if (!socket || socket.readyState !== SOCKET_OPEN) return false;
  try {
    socket.send(JSON.stringify(message));
    return true;
  } catch {
    // The socket can move from OPEN to CLOSING between the readyState check and
    // send(). Callers need a synchronous failure signal so optimistic UI state
    // does not wait forever for a server event that can never arrive.
    return false;
  }
}
