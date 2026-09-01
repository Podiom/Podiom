import { describe, expect, it, vi } from "vitest";

import type { ClientMessage } from "./types";
import { sendWebSocketMessage } from "./websocketSend";

const message: ClientMessage = {
  type: "send_turn",
  request_id: "req-1",
  session_id: "session-1",
  message: "hello",
};

describe("sendWebSocketMessage", () => {
  it("serializes and sends a message through an open socket", () => {
    const send = vi.fn();

    expect(sendWebSocketMessage({ readyState: 1, send }, message)).toBe(true);
    expect(send).toHaveBeenCalledOnce();
    expect(JSON.parse(send.mock.calls[0][0])).toEqual(message);
  });

  it.each([0, 2, 3])("rejects a socket in readyState %i", (readyState) => {
    const send = vi.fn();

    expect(sendWebSocketMessage({ readyState, send }, message)).toBe(false);
    expect(send).not.toHaveBeenCalled();
  });

  it("returns false when the socket closes during send", () => {
    const send = vi.fn(() => {
      throw new DOMException("The socket is closing", "InvalidStateError");
    });

    expect(sendWebSocketMessage({ readyState: 1, send }, message)).toBe(false);
    expect(send).toHaveBeenCalledOnce();
  });

  it("rejects a missing socket", () => {
    expect(sendWebSocketMessage(null, message)).toBe(false);
  });
});
